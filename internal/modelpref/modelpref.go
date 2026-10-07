// Package modelpref implements reversible edits of the `model` field inside an
// OpenCode agent Markdown frontmatter block, together with the preference and
// receipt types that agent-sync persists in its state.
//
// An agent file is YAML frontmatter delimited by lines that are exactly "---",
// followed by the Markdown body. Only the top-level scalar `model:` key is ever
// touched: the body, other keys, comments, indentation and line endings are
// preserved byte for byte. Every edit returns a Receipt that can later Undo the
// exact same edit, and Undo fails closed when the file changed in a way that
// makes the reversal unsafe.
package modelpref

import (
	"errors"
	"fmt"
	"strings"
)

// Mode is the kind of model preference recorded for a managed agent.
type Mode string

const (
	// ModeExplicit pins the agent to a concrete provider/model[#variant].
	ModeExplicit Mode = "explicit"
	// ModeInherit removes the upstream model so the agent inherits its parent.
	ModeInherit Mode = "inherit"
)

// Pref is the stored choice for one managed agent path.
type Pref struct {
	Mode    Mode    `json:"mode"`
	Model   string  `json:"model,omitempty"`
	Receipt Receipt `json:"receipt"`
}

// Receipt records exactly how the last edit transformed the raw upstream bytes
// so that it can be reversed without a general-purpose YAML serializer.
type Receipt struct {
	HadFrontmatter     bool   `json:"had_frontmatter"`
	CreatedFrontmatter bool   `json:"created_frontmatter"`
	RemovedFrontmatter bool   `json:"removed_frontmatter"`
	HadKey             bool   `json:"had_key"`
	RemovedKey         bool   `json:"removed_key"`
	AppliedLine        string `json:"applied_line,omitempty"`
	OriginalLine       string `json:"original_line,omitempty"`
	PrevLine           string `json:"prev_line,omitempty"`
	NextLine           string `json:"next_line,omitempty"`
	EOL                string `json:"eol,omitempty"`
}

// ParseRef splits a model reference of the form provider/model[#variant].
func ParseRef(s string) (provider, model, variant string, err error) {
	if s == "" {
		return "", "", "", errors.New("empty model reference")
	}
	if strings.ContainsAny(s, " \t\r\n") {
		return "", "", "", fmt.Errorf("model reference %q contains whitespace", s)
	}
	i := strings.IndexByte(s, '/')
	if i <= 0 || i == len(s)-1 {
		return "", "", "", fmt.Errorf("model reference %q must be provider/model", s)
	}
	provider = s[:i]
	rest := s[i+1:]
	if j := strings.IndexByte(rest, '#'); j >= 0 {
		model = rest[:j]
		variant = rest[j+1:]
		if model == "" {
			return "", "", "", fmt.Errorf("model reference %q has an empty model", s)
		}
		if variant == "" {
			return "", "", "", fmt.Errorf("model reference %q has an empty variant", s)
		}
	} else {
		model = rest
	}
	if model == "" {
		return "", "", "", fmt.Errorf("model reference %q has an empty model", s)
	}
	return provider, model, variant, nil
}

// ValidateRef reports whether s is a syntactically valid model reference. It
// does not consult any catalog: manual identifiers are accepted here and only
// marked unverified by callers when the catalog is checked.
func ValidateRef(s string) error {
	_, _, _, err := ParseRef(s)
	return err
}

// ValidatePref checks a preference before it is rendered or stored.
func ValidatePref(p Pref) error {
	switch p.Mode {
	case ModeExplicit:
		return ValidateRef(p.Model)
	case ModeInherit:
		if p.Model != "" {
			return errors.New("inherit preference must not carry a model")
		}
		return nil
	default:
		return fmt.Errorf("unknown model preference mode %q", p.Mode)
	}
}

type line struct {
	text string // without the line ending
	eol  string // "\n", "\r\n" or "" for a final line without ending
}

func splitLines(data []byte) []line {
	s := string(data)
	var out []line
	for len(s) > 0 {
		i := strings.IndexByte(s, '\n')
		if i < 0 {
			out = append(out, line{text: s})
			break
		}
		t := s[:i]
		eol := "\n"
		if strings.HasSuffix(t, "\r") {
			t = t[:len(t)-1]
			eol = "\r\n"
		}
		out = append(out, line{text: t, eol: eol})
		s = s[i+1:]
	}
	return out
}

func joinLines(ls []line) []byte {
	var b strings.Builder
	for _, l := range ls {
		b.WriteString(l.text)
		b.WriteString(l.eol)
	}
	return []byte(b.String())
}

func defaultEOL(raw []byte) string {
	if strings.Contains(string(raw), "\r\n") {
		return "\r\n"
	}
	return "\n"
}

type fmInfo struct {
	exists   bool
	openIdx  int
	closeIdx int
	eol      string
}

// findFrontmatter locates a leading frontmatter block. It errors on a block
// that opens with "---" but never closes, so we never guess at malformed YAML.
func findFrontmatter(ls []line) (fmInfo, error) {
	if len(ls) == 0 || ls[0].text != "---" || ls[0].eol == "" {
		return fmInfo{}, nil
	}
	for i := 1; i < len(ls); i++ {
		if ls[i].text == "---" {
			return fmInfo{exists: true, openIdx: 0, closeIdx: i, eol: ls[0].eol}, nil
		}
	}
	return fmInfo{}, errors.New("unterminated YAML frontmatter (missing closing ---)")
}

type modelKey struct {
	idx      int
	line     string // full line text without EOL
	prefix   string // "model:" plus any separating spaces
	scalar   string // exact scalar token, including quotes when present
	trailing string // any spaces plus inline comment after the scalar
}

func isTopLevel(t string) bool { return t != "" && t[0] != ' ' && t[0] != '\t' }

// scanModelKey returns the top-level model keys in the frontmatter body. It
// rejects duplicate keys and merge keys instead of silently picking one.
func scanModelKey(ls []line, fm fmInfo) ([]modelKey, error) {
	if !fm.exists {
		return nil, nil
	}
	var keys []modelKey
	for i := fm.openIdx + 1; i < fm.closeIdx; i++ {
		t := ls[i].text
		if !isTopLevel(t) {
			continue
		}
		if t == "<<" || strings.HasPrefix(t, "<<:") {
			return nil, errors.New("unsupported YAML merge key (<<) in frontmatter")
		}
		if !strings.HasPrefix(t, "model:") {
			continue
		}
		rest := t[len("model:"):]
		if rest != "" && rest[0] != ' ' && rest[0] != '\t' {
			continue
		}
		k, err := parseModelLine(t)
		if err != nil {
			return nil, err
		}
		k.idx = i
		keys = append(keys, k)
	}
	if len(keys) > 1 {
		return nil, errors.New("duplicate model fields in frontmatter")
	}
	return keys, nil
}

func parseModelLine(t string) (modelKey, error) {
	rest := t[len("model:"):]
	i := 0
	for i < len(rest) && (rest[i] == ' ' || rest[i] == '\t') {
		i++
	}
	prefix := t[:len("model:")+i]
	v := rest[i:]
	var scalar, trailing string
	switch {
	case v == "":
	case v[0] == '\'' || v[0] == '"':
		q := v[0]
		j := 1
		for j < len(v) && v[j] != q {
			j++
		}
		if j >= len(v) {
			return modelKey{}, errors.New("unterminated quoted model value")
		}
		scalar = v[:j+1]
		trailing = v[j+1:]
	default:
		j := len(v)
		for k := 0; k+1 < len(v); k++ {
			if v[k] == ' ' && v[k+1] == '#' {
				j = k
				break
			}
		}
		scalar = strings.TrimRight(v[:j], " \t")
		trailing = v[j:]
	}
	if scalar != "" {
		switch scalar[0] {
		case '{', '[', '|', '>', '&', '*', '!':
			return modelKey{}, fmt.Errorf("unsupported complex or aliased model value %q", scalar)
		}
	}
	if tt := strings.TrimLeft(trailing, " \t"); tt != "" && !strings.HasPrefix(tt, "#") {
		return modelKey{}, fmt.Errorf("unsupported trailing content after model value %q", trailing)
	}
	return modelKey{line: t, prefix: prefix, scalar: scalar, trailing: trailing}, nil
}

func unquote(s string) string {
	if len(s) >= 2 && (s[0] == '\'' || s[0] == '"') && s[len(s)-1] == s[0] {
		return s[1 : len(s)-1]
	}
	return s
}

// ModelOf returns the current model value (unquoted) and whether the key is
// present. It reports an error for malformed or unsupported frontmatter.
func ModelOf(data []byte) (string, bool, error) {
	ls := splitLines(data)
	fm, err := findFrontmatter(ls)
	if err != nil {
		return "", false, err
	}
	keys, err := scanModelKey(ls, fm)
	if err != nil {
		return "", false, err
	}
	if len(keys) == 0 {
		return "", false, nil
	}
	return unquote(keys[0].scalar), true, nil
}

// Render applies pref to the raw upstream bytes and returns the new bytes plus
// the receipt that can reverse the edit.
func Render(raw []byte, pref Pref) ([]byte, Receipt, error) {
	if err := ValidatePref(pref); err != nil {
		return nil, Receipt{}, err
	}
	ls := splitLines(raw)
	fm, err := findFrontmatter(ls)
	if err != nil {
		return nil, Receipt{}, err
	}
	keys, err := scanModelKey(ls, fm)
	if err != nil {
		return nil, Receipt{}, err
	}

	if pref.Mode == ModeInherit {
		if len(keys) == 0 {
			return raw, Receipt{HadFrontmatter: fm.exists, EOL: eolOr(fm.eol)}, nil
		}
		k := keys[0]
		rec := Receipt{HadFrontmatter: true, HadKey: true, RemovedKey: true, OriginalLine: k.line, EOL: fm.eol}
		if k.idx > fm.openIdx+1 {
			rec.PrevLine = ls[k.idx-1].text
		}
		if k.idx < fm.closeIdx-1 {
			rec.NextLine = ls[k.idx+1].text
		}
		if fm.closeIdx == fm.openIdx+2 {
			// The frontmatter held only the model key. Removing it would leave
			// an empty block, so drop the whole block and remember to re-create
			// it on undo.
			rec.RemovedFrontmatter = true
			out := append(append([]line{}, ls[:fm.openIdx]...), ls[fm.closeIdx+1:]...)
			return joinLines(out), rec, nil
		}
		out := append(append([]line{}, ls[:k.idx]...), ls[k.idx+1:]...)
		return joinLines(out), rec, nil
	}

	// Explicit pin.
	val := pref.Model
	if len(keys) == 1 {
		k := keys[0]
		applied := k.prefix + val + k.trailing
		rec := Receipt{HadFrontmatter: true, HadKey: true, OriginalLine: k.line, AppliedLine: applied, EOL: fm.eol}
		out := append([]line{}, ls...)
		out[k.idx] = line{text: applied, eol: ls[k.idx].eol}
		return joinLines(out), rec, nil
	}

	newLine := line{text: "model: " + val}
	if !fm.exists {
		eol := defaultEOL(raw)
		rec := Receipt{HadFrontmatter: false, CreatedFrontmatter: true, AppliedLine: newLine.text, EOL: eol}
		out := []line{{text: "---", eol: eol}, {text: newLine.text, eol: eol}, {text: "---", eol: eol}}
		out = append(out, ls...)
		return joinLines(out), rec, nil
	}
	newLine.eol = fm.eol
	rec := Receipt{HadFrontmatter: true, AppliedLine: newLine.text, EOL: fm.eol}
	if fm.closeIdx > fm.openIdx+1 {
		rec.PrevLine = ls[fm.closeIdx-1].text
	}
	out := append([]line{}, ls[:fm.closeIdx]...)
	out = append(out, newLine)
	out = append(out, ls[fm.closeIdx:]...)
	return joinLines(out), rec, nil
}

func eolOr(eol string) string {
	if eol == "" {
		return "\n"
	}
	return eol
}

// Verify reports whether rec still safely describes local. It is exactly the
// validation step Undo performs, without building the reversed bytes.
func Verify(local []byte, rec Receipt) error {
	_, err := Undo(local, rec)
	return err
}

// Undo reverses a recorded edit, preserving every byte it did not itself
// change. It fails closed when the model field was modified, duplicated or the
// frontmatter structure no longer matches the receipt.
func Undo(local []byte, rec Receipt) ([]byte, error) {
	ls := splitLines(local)
	fm, err := findFrontmatter(ls)
	if err != nil {
		return nil, err
	}
	keys, err := scanModelKey(ls, fm)
	if err != nil {
		return nil, err
	}

	if rec.RemovedKey {
		if len(keys) != 0 {
			return nil, errors.New("model field was re-added after the recorded edit; refusing to undo")
		}
		if rec.RemovedFrontmatter {
			if fm.exists {
				return nil, errors.New("frontmatter changed after the recorded edit; refusing to undo")
			}
			eol := eolOr(rec.EOL)
			block := []line{{text: "---", eol: eol}, {text: rec.OriginalLine, eol: eol}, {text: "---", eol: eol}}
			return joinLines(append(block, ls...)), nil
		}
		if !fm.exists {
			return nil, errors.New("frontmatter missing; refusing to undo")
		}
		insert := fm.closeIdx
		if rec.PrevLine != "" {
			idx, err := uniqueBodyLine(ls, fm, rec.PrevLine)
			if err != nil {
				return nil, err
			}
			if idx >= 0 {
				insert = idx + 1
			} else if rec.NextLine != "" {
				if idx2, err := uniqueBodyLine(ls, fm, rec.NextLine); err != nil {
					return nil, err
				} else if idx2 >= 0 {
					insert = idx2
				}
			}
		} else if rec.NextLine != "" {
			if idx, err := uniqueBodyLine(ls, fm, rec.NextLine); err != nil {
				return nil, err
			} else if idx >= 0 {
				insert = idx
			}
		}
		nl := line{text: rec.OriginalLine, eol: eolOr(rec.EOL)}
		out := append([]line{}, ls[:insert]...)
		out = append(out, nl)
		out = append(out, ls[insert:]...)
		return joinLines(out), nil
	}

	if rec.AppliedLine == "" {
		return local, nil
	}

	if !fm.exists {
		return nil, errors.New("frontmatter missing; refusing to undo")
	}
	if len(keys) != 1 {
		return nil, errors.New("model field missing or duplicated; refusing to undo")
	}
	k := keys[0]
	if k.line != rec.AppliedLine {
		return nil, errors.New("model field changed after the recorded edit; refusing to undo")
	}
	if rec.OriginalLine == "" {
		if rec.CreatedFrontmatter && fm.closeIdx == fm.openIdx+2 {
			out := append(append([]line{}, ls[:fm.openIdx]...), ls[fm.closeIdx+1:]...)
			return joinLines(out), nil
		}
		out := append(append([]line{}, ls[:k.idx]...), ls[k.idx+1:]...)
		return joinLines(out), nil
	}
	out := append([]line{}, ls...)
	out[k.idx] = line{text: rec.OriginalLine, eol: ls[k.idx].eol}
	return joinLines(out), nil
}

func uniqueBodyLine(ls []line, fm fmInfo, text string) (int, error) {
	found := -1
	for i := fm.openIdx + 1; i < fm.closeIdx; i++ {
		if ls[i].text == text {
			if found >= 0 {
				return -1, fmt.Errorf("anchor line %q is ambiguous; refusing to undo", text)
			}
			found = i
		}
	}
	return found, nil
}
