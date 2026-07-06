package dashboard

import (
	"encoding/base64"
	"fmt"
	"unicode/utf8"
)

// jsonStr makes an arbitrary byte string safe to ship as JSON. Go's JSON
// encoder silently replaces invalid UTF-8 with U+FFFD, which would corrupt
// binary keys/values on an edit round-trip; instead, anything that is not
// clean printable UTF-8 is wrapped in a {"$b64": ..., "len": n} envelope the
// frontend renders as binary (hex view, no inline editing).
func jsonStr(s string) any {
	if cleanUTF8(s) {
		return s
	}
	return map[string]any{
		"$b64": base64.StdEncoding.EncodeToString([]byte(s)),
		"len":  len(s),
	}
}

// cleanUTF8 reports whether s is valid UTF-8 free of C0 control characters
// (tab, newline, and carriage return excepted).
func cleanUTF8(s string) bool {
	if !utf8.ValidString(s) {
		return false
	}
	for i := 0; i < len(s); i++ {
		if c := s[i]; c < 0x20 && c != '\t' && c != '\n' && c != '\r' {
			return false
		}
	}
	return true
}

// decodeStr accepts the inbound counterpart: either a plain JSON string or
// the {"$b64": ...} envelope produced by jsonStr.
func decodeStr(v any) (string, error) {
	switch x := v.(type) {
	case string:
		return x, nil
	case map[string]any:
		b64, ok := x["$b64"].(string)
		if !ok {
			return "", fmt.Errorf("object value must carry $b64")
		}
		raw, err := base64.StdEncoding.DecodeString(b64)
		if err != nil {
			return "", fmt.Errorf("bad base64: %w", err)
		}
		return string(raw), nil
	default:
		return "", fmt.Errorf("expected string or {$b64} object, got %T", v)
	}
}
