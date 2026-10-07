//go:build windows

package userpath

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unicode/utf16"
	"unsafe"
)

const (
	// HKEY_CURRENT_USER, registry value types and access masks.
	hkeyCurrentUser = 0x80000001
	regSZ           = 1
	regExpandSZ     = 2
	keyRead         = 0x20019
	keyWrite        = 0x20006

	envKey    = "Environment"
	pathValue = "Path"
)

var (
	advapi32             = syscall.NewLazyDLL("advapi32.dll")
	procRegOpenKeyExW    = advapi32.NewProc("RegOpenKeyExW")
	procRegCreateKeyExW  = advapi32.NewProc("RegCreateKeyExW")
	procRegQueryValueExW = advapi32.NewProc("RegQueryValueExW")
	procRegSetValueExW   = advapi32.NewProc("RegSetValueExW")
	procRegCloseKey      = advapi32.NewProc("RegCloseKey")
)

func lastErr(r uintptr) error {
	if syscall.Errno(r) == syscall.ERROR_FILE_NOT_FOUND {
		return os.ErrNotExist
	}
	return syscall.Errno(r)
}

func openEnvKey() (uintptr, error) {
	name, err := syscall.UTF16PtrFromString(envKey)
	if err != nil {
		return 0, err
	}
	var h uintptr
	r, _, _ := procRegOpenKeyExW.Call(
		hkeyCurrentUser, uintptr(unsafe.Pointer(name)), 0, keyRead,
		uintptr(unsafe.Pointer(&h)),
	)
	if r != 0 {
		return 0, lastErr(r)
	}
	return h, nil
}

func createEnvKey() (uintptr, error) {
	name, err := syscall.UTF16PtrFromString(envKey)
	if err != nil {
		return 0, err
	}
	var h uintptr
	var disp uint32
	r, _, _ := procRegCreateKeyExW.Call(
		hkeyCurrentUser, uintptr(unsafe.Pointer(name)), 0, 0, 0,
		keyRead|keyWrite, 0,
		uintptr(unsafe.Pointer(&h)), uintptr(unsafe.Pointer(&disp)),
	)
	if r != 0 {
		return 0, syscall.Errno(r)
	}
	return h, nil
}

// registry is the seam that keeps tests away from the real HKCU hive.
type registry interface {
	query(name string) (typ uint32, raw []byte, exists bool, err error)
	set(name string, typ uint32, raw []byte) error
}

type winRegistry struct{}

func (winRegistry) query(name string) (uint32, []byte, bool, error) {
	h, err := openEnvKey()
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return 0, nil, false, nil
		}
		return 0, nil, false, err
	}
	defer procRegCloseKey.Call(h)
	vname, err := syscall.UTF16PtrFromString(name)
	if err != nil {
		return 0, nil, false, err
	}
	var typ, cbData uint32
	r, _, _ := procRegQueryValueExW.Call(
		h, uintptr(unsafe.Pointer(vname)), 0,
		uintptr(unsafe.Pointer(&typ)), 0, uintptr(unsafe.Pointer(&cbData)),
	)
	if r != 0 {
		if errors.Is(lastErr(r), os.ErrNotExist) {
			return 0, nil, false, nil
		}
		return 0, nil, false, lastErr(r)
	}
	if cbData == 0 {
		return typ, nil, true, nil
	}
	buf := make([]byte, cbData)
	r, _, _ = procRegQueryValueExW.Call(
		h, uintptr(unsafe.Pointer(vname)), 0,
		uintptr(unsafe.Pointer(&typ)), uintptr(unsafe.Pointer(&buf[0])),
		uintptr(unsafe.Pointer(&cbData)),
	)
	if r != 0 {
		return 0, nil, false, lastErr(r)
	}
	return typ, buf[:cbData], true, nil
}

func (winRegistry) set(name string, typ uint32, raw []byte) error {
	h, err := createEnvKey()
	if err != nil {
		return err
	}
	defer procRegCloseKey.Call(h)
	vname, err := syscall.UTF16PtrFromString(name)
	if err != nil {
		return err
	}
	var ptr uintptr
	if len(raw) > 0 {
		ptr = uintptr(unsafe.Pointer(&raw[0]))
	}
	r, _, _ := procRegSetValueExW.Call(
		h, uintptr(unsafe.Pointer(vname)), 0, uintptr(typ), ptr, uintptr(uint32(len(raw))),
	)
	if r != 0 {
		return syscall.Errno(r)
	}
	return nil
}

type winState struct {
	exists bool
	typ    uint32
	raw    []byte
}

func readPathValue(reg registry) (winState, error) {
	typ, raw, exists, err := reg.query(pathValue)
	if err != nil {
		return winState{}, err
	}
	return winState{exists: exists, typ: typ, raw: raw}, nil
}

// decodeRegString validates and decodes a REG_SZ/REG_EXPAND_SZ raw value. It
// fails closed on odd-length data, a missing trailing NUL, or unpaired UTF-16
// surrogates, so a malformed value is never rewritten.
func decodeRegString(raw []byte) (string, error) {
	if len(raw)%2 != 0 {
		return "", fmt.Errorf("value is not valid UTF-16 (odd byte length); refusing to modify it")
	}
	us := make([]uint16, len(raw)/2)
	for i := range us {
		us[i] = binary.LittleEndian.Uint16(raw[i*2:])
	}
	if len(us) == 0 || us[len(us)-1] != 0 {
		return "", fmt.Errorf("value is not NUL-terminated; refusing to modify it")
	}
	us = us[:len(us)-1]
	for i := 0; i < len(us); i++ {
		r := rune(us[i])
		if r >= 0xD800 && r <= 0xDBFF {
			if i+1 >= len(us) {
				return "", fmt.Errorf("value has an unpaired UTF-16 surrogate; refusing to modify it")
			}
			lo := rune(us[i+1])
			if lo < 0xDC00 || lo > 0xDFFF {
				return "", fmt.Errorf("value has an unpaired UTF-16 surrogate; refusing to modify it")
			}
			i++
		} else if r >= 0xDC00 && r <= 0xDFFF {
			return "", fmt.Errorf("value has an unpaired UTF-16 surrogate; refusing to modify it")
		}
	}
	return string(utf16.Decode(us)), nil
}

func encodeRegString(s string) []byte {
	us := utf16.Encode([]rune(s))
	us = append(us, 0)
	b := make([]byte, len(us)*2)
	for i, v := range us {
		binary.LittleEndian.PutUint16(b[i*2:], v)
	}
	return b
}

// normalizeWinEntry casefolds a PATH entry and trims surrounding quotes and
// trailing slashes for a conservative membership comparison. Existing entries
// are never rewritten.
func normalizeWinEntry(s string) string {
	s = strings.TrimSpace(s)
	s = strings.Trim(s, `"`)
	s = strings.TrimRight(s, `\/`)
	return strings.ToLower(s)
}

func pathContains(cur string, dir string) bool {
	want := normalizeWinEntry(dir)
	if want == "" {
		return false
	}
	for _, part := range strings.Split(cur, ";") {
		if normalizeWinEntry(part) == want {
			return true
		}
	}
	return false
}

func appendPath(cur, dir string) string {
	if cur != "" && !strings.HasSuffix(cur, ";") {
		return cur + ";" + dir
	}
	return cur + dir
}

func writeRegistryBackup(dir string, st winState) (string, error) {
	if !st.exists {
		return "", nil
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	for i := 0; i < 100; i++ {
		name := fmt.Sprintf("environment-path.%d.type%d.bak", time.Now().UnixNano(), st.typ)
		p := filepath.Join(dir, name)
		f, err := os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if errors.Is(err, os.ErrExist) {
			continue
		}
		if err != nil {
			return "", err
		}
		_, werr := f.Write(st.raw)
		cerr := f.Close()
		if werr != nil {
			return "", werr
		}
		if cerr != nil {
			return "", cerr
		}
		return p, nil
	}
	return "", fmt.Errorf("could not allocate a unique backup name")
}

func sameWinState(a, b winState) bool {
	return a.exists == b.exists && a.typ == b.typ && bytes.Equal(a.raw, b.raw)
}

// Add performs the PATH installation on Windows by editing the HKCU\Environment
// "Path" value. A missing value is created as REG_EXPAND_SZ.
func Add(dir string, opts Options) (Report, error) {
	return addWith(dir, opts, winRegistry{})
}

func addWith(dir string, opts Options, reg registry) (Report, error) {
	rep := Report{Dir: dir}
	if opts.Shell != "" {
		return rep, fmt.Errorf("--shell is only valid on Linux")
	}
	if !opts.DryRun && opts.BackupDir == "" {
		return rep, fmt.Errorf("no backup directory configured")
	}
	if strings.ContainsAny(dir, ";\r\n") || strings.ContainsRune(dir, 0) {
		return rep, fmt.Errorf("executable directory %q contains a character that cannot be used in PATH", dir)
	}
	if !opts.DryRun && !opts.AssumeYes {
		if !opts.Interactive {
			rep.Skipped = true
			rep.Note = "skipped: not a terminal"
			return rep, nil
		}
		ok, err := confirmPrompt(opts.Stdin, opts.Stdout, fmt.Sprintf("Add %s to PATH? [y/N] ", dir))
		if err != nil {
			return rep, fmt.Errorf("reading confirmation: %w", err)
		}
		if !ok {
			rep.Skipped = true
			return rep, nil
		}
	}

	st, err := readPathValue(reg)
	if err != nil {
		return rep, err
	}

	typ := uint32(regExpandSZ)
	cur := ""
	if st.exists {
		switch st.typ {
		case regSZ, regExpandSZ:
			typ = st.typ
		default:
			return rep, fmt.Errorf("HKCU\\%s\\%s has unsupported registry type %d; refusing to modify it", envKey, pathValue, st.typ)
		}
		cur, err = decodeRegString(st.raw)
		if err != nil {
			return rep, err
		}
	}

	pv := Preview{Target: `HKCU\` + envKey + `\` + pathValue, Create: !st.exists}
	if st.exists && pathContains(cur, dir) {
		pv.Already = true
		rep.Previews = append(rep.Previews, pv)
		rep.Note = "already present"
		return rep, nil
	}
	if typ == regExpandSZ && strings.Contains(dir, "%") {
		return rep, fmt.Errorf("directory %q contains '%%', which REG_EXPAND_SZ would expand as a variable; refusing to add it", dir)
	}
	pv.Added = []string{dir}
	rep.Previews = append(rep.Previews, pv)
	if opts.DryRun {
		rep.Note = "would append the directory to the user PATH"
		return rep, nil
	}

	backup, err := writeRegistryBackup(opts.BackupDir, st)
	if err != nil {
		return rep, fmt.Errorf("backing up the user PATH: %w", err)
	}
	if backup != "" {
		rep.Backups = append(rep.Backups, backup)
	}

	// Re-read to detect a concurrent change; never overwrite drifted data.
	cur2, err := readPathValue(reg)
	if err != nil {
		return rep, err
	}
	if !sameWinState(cur2, st) {
		return rep, fmt.Errorf("the user PATH changed while planning; refusing to overwrite it")
	}

	newRaw := encodeRegString(appendPath(cur, dir))
	if err := reg.set(pathValue, typ, newRaw); err != nil {
		return rep, err
	}
	rep.Note = "new terminals pick up PATH; a fresh login is the reliable way (an already-open shell keeps the old value)"
	return rep, nil
}
