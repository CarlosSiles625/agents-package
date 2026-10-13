package piexport

import "fmt"

// ParserMode is a compatibility declaration, not verification of a live host.
type ParserMode string

const (
	ParserUpstream ParserMode = "upstream"
	ParserPatched40V1 ParserMode = "patched-40.2.0-v1"
)

func NormalizeParserMode(mode ParserMode) (ParserMode, error) {
	if mode == "" { return ParserUpstream, nil }
	if mode != ParserUpstream && mode != ParserPatched40V1 {
		return "", fmt.Errorf("--permission-parser must be upstream or patched-40.2.0-v1, got %q", mode)
	}
	return mode, nil
}
