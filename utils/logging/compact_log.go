package logging

import (
	"encoding/json"
	"fmt"
	"strings"
)

// CompactJSONForLog marshals v to compact JSON and truncates to maxRunes (Unicode runes).
// maxRunes <= 0 defaults to 8000.
func CompactJSONForLog(v any, maxRunes int) string {
	if v == nil {
		return ""
	}
	if maxRunes <= 0 {
		maxRunes = 8000
	}
	b, err := json.Marshal(v)
	s := string(b)
	if err != nil {
		s = fmt.Sprintf("%v", v)
	}
	return TruncateRunes(s, maxRunes)
}

// TruncateRunes returns s truncated to maxRunes runes, with an ellipsis if shortened.
func TruncateRunes(s string, maxRunes int) string {
	if maxRunes <= 0 {
		return ""
	}
	r := []rune(strings.TrimSpace(s))
	if len(r) <= maxRunes {
		return s
	}
	return string(r[:maxRunes]) + "…"
}
