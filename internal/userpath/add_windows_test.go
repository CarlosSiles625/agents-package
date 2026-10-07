//go:build windows

package userpath

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

type qres struct {
	typ    uint32
	raw    []byte
	exists bool
	err    error
}

// fakeReg is an in-memory registry seam: tests never touch the real HKCU hive.
type fakeReg struct {
	results  []qres
	idx      int
	setCalls int
	setTyp   uint32
	setRaw   []byte
}

func (f *fakeReg) query(string) (uint32, []byte, bool, error) {
	if len(f.results) == 0 {
		return 0, nil, false, nil
	}
	var r qres
	if f.idx < len(f.results) {
		r = f.results[f.idx]
		f.idx++
	} else {
		r = f.results[len(f.results)-1]
	}
	return r.typ, r.raw, r.exists, r.err
}

func (f *fakeReg) set(_ string, typ uint32, raw []byte) error {
	f.setCalls++
	f.setTyp = typ
	f.setRaw = raw
	return nil
}

func regFrom(st winState) *fakeReg {
	return &fakeReg{results: []qres{{st.typ, st.raw, st.exists, nil}}}
}

func TestAddWindowsCreatesExpandSZ(t *testing.T) {
	reg := &fakeReg{}
	backupDir := filepath.Join(t.TempDir(), "path-backups")
	dir := `C:\tools\agent-sync`
	rep, err := addWith(dir, Options{AssumeYes: true, BackupDir: backupDir}, reg)
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	if reg.setCalls != 1 || reg.setTyp != regExpandSZ {
		t.Fatalf("set calls=%d type=%d, want 1/%d", reg.setCalls, reg.setTyp, regExpandSZ)
	}
	got, err := decodeRegString(reg.setRaw)
	if err != nil || got != dir {
		t.Fatalf("written value = %q, %v", got, err)
	}
	if len(rep.Backups) != 0 {
		t.Fatalf("absent value must not be backed up: %v", rep.Backups)
	}
	if len(rep.Previews) != 1 || !rep.Previews[0].Create {
		t.Fatalf("previews = %+v", rep.Previews)
	}
}

func TestAddWindowsPreservesExistingTypeAndValue(t *testing.T) {
	original := encodeRegString(`C:\old`)
	reg := regFrom(winState{exists: true, typ: regSZ, raw: original})
	backupDir := filepath.Join(t.TempDir(), "path-backups")
	dir := `C:\tools`
	rep, err := addWith(dir, Options{AssumeYes: true, BackupDir: backupDir}, reg)
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	if reg.setTyp != regSZ {
		t.Fatalf("REG_SZ must be preserved, got type %d", reg.setTyp)
	}
	got, _ := decodeRegString(reg.setRaw)
	if got != `C:\old;`+dir {
		t.Fatalf("appended value = %q", got)
	}
	if len(rep.Backups) != 1 {
		t.Fatalf("backups = %v", rep.Backups)
	}
	if b, _ := os.ReadFile(rep.Backups[0]); string(b) != string(original) {
		t.Fatalf("backup must hold the raw original bytes")
	}
}

func TestAddWindowsAlreadyPresentNoWrite(t *testing.T) {
	reg := regFrom(winState{exists: true, typ: regExpandSZ, raw: encodeRegString(`C:\old;"C:\tools\";C:\x`)})
	rep, err := addWith(`c:\tools`, Options{AssumeYes: true, BackupDir: filepath.Join(t.TempDir(), "b")}, reg)
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	if reg.setCalls != 0 {
		t.Fatal("already-present directory must not be rewritten")
	}
	if len(rep.Previews) != 1 || !rep.Previews[0].Already {
		t.Fatalf("previews = %+v", rep.Previews)
	}
}

func TestAddWindowsUnsupportedTypeRefused(t *testing.T) {
	reg := regFrom(winState{exists: true, typ: 3, raw: encodeRegString("x")})
	if _, err := addWith(`C:\tools`, Options{AssumeYes: true, BackupDir: t.TempDir()}, reg); err == nil {
		t.Fatal("unsupported registry type must be refused")
	}
	if reg.setCalls != 0 {
		t.Fatal("unsupported type must not be written")
	}
}

func TestAddWindowsPercentRefusedForExpandSZ(t *testing.T) {
	reg := &fakeReg{}
	if _, err := addWith(`C:\100%`, Options{AssumeYes: true, BackupDir: t.TempDir()}, reg); err == nil {
		t.Fatal("% must be refused when the value would be REG_EXPAND_SZ")
	}
	if reg.setCalls != 0 {
		t.Fatal("no write expected")
	}
}

func TestAddWindowsMalformedValueRefused(t *testing.T) {
	cases := map[string][]byte{
		"odd length":     {0x01},
		"not terminated": {0x41, 0x00, 0x42, 0x00},
		"unpaired high":  {0x00, 0xD8, 0x41, 0x00, 0x00, 0x00},
		"unpaired low":   {0x00, 0xDC, 0x00, 0x00},
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			reg := regFrom(winState{exists: true, typ: regSZ, raw: raw})
			if _, err := addWith(`C:\tools`, Options{AssumeYes: true, BackupDir: t.TempDir()}, reg); err == nil {
				t.Fatal("malformed value must be refused")
			}
			if reg.setCalls != 0 {
				t.Fatal("malformed value must not be rewritten")
			}
		})
	}
}

func TestAddWindowsConcurrentChangeRefused(t *testing.T) {
	a := encodeRegString(`C:\a`)
	b := encodeRegString(`C:\a;C:\b`)
	backupDir := filepath.Join(t.TempDir(), "path-backups")
	reg := &fakeReg{results: []qres{
		{regSZ, a, true, nil},
		{regSZ, b, true, nil},
	}}
	if _, err := addWith(`C:\tools`, Options{AssumeYes: true, BackupDir: backupDir}, reg); err == nil {
		t.Fatal("a concurrent change must abort before writing")
	}
	if reg.setCalls != 0 {
		t.Fatal("drifted value must not be overwritten")
	}
}

func TestAddWindowsDryRunWritesNothing(t *testing.T) {
	dir := t.TempDir()
	reg := regFrom(winState{exists: true, typ: regSZ, raw: encodeRegString(`C:\old`)})
	rep, err := addWith(`C:\tools`, Options{AssumeYes: true, DryRun: true, BackupDir: dir}, reg)
	if err != nil {
		t.Fatalf("dry-run: %v", err)
	}
	if reg.setCalls != 0 {
		t.Fatal("dry-run wrote the registry")
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Fatalf("dry-run created files: %v", entries)
	}
	if len(rep.Previews) != 1 || len(rep.Previews[0].Added) != 1 {
		t.Fatalf("previews = %+v", rep.Previews)
	}
	if rep.Previews[0].Added[0] != `C:\tools` {
		t.Fatalf("preview added = %q", rep.Previews[0].Added)
	}
}

func TestAddWindowsBadDirectoryRefused(t *testing.T) {
	for _, dir := range []string{"a;b", "a\nb", "a\x00b"} {
		reg := &fakeReg{}
		if _, err := addWith(dir, Options{AssumeYes: true, BackupDir: t.TempDir()}, reg); err == nil {
			t.Fatalf("directory %q must be refused", dir)
		}
		if reg.setCalls != 0 {
			t.Fatal("bad directory must not be written")
		}
	}
}

func TestAppendPathPreservesBlankComponent(t *testing.T) {
	if got := appendPath(`C:\a;;C:\b;`, `C:\c`); got != `C:\a;;C:\b;C:\c` {
		t.Fatalf("appendPath = %q", got)
	}
	if got := appendPath("", `C:\c`); got != `C:\c` {
		t.Fatalf("appendPath empty = %q", got)
	}
}

func TestPathContainsNormalizes(t *testing.T) {
	if !pathContains(`C:\A\;"C:\tools\";C:\x`, `c:\tools`) {
		t.Fatal("quoted/case-variant entry must match")
	}
	if pathContains(`C:\tools2`, `C:\tools`) {
		t.Fatal("prefix must not match")
	}
}

func TestDecodeRegStringRoundTrip(t *testing.T) {
	for _, s := range []string{"", `C:\x`, "emoji 😀"} {
		got, err := decodeRegString(encodeRegString(s))
		if err != nil || got != s {
			t.Fatalf("round trip %q = %q, %v", s, got, err)
		}
	}
	if _, err := decodeRegString(nil); err == nil {
		t.Fatal("empty raw value must be refused")
	}
	if !errors.Is(lastErr(2), os.ErrNotExist) {
		t.Fatal("file-not-found must map to os.ErrNotExist")
	}
}
