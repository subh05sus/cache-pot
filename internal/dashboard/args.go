package dashboard

import (
	"fmt"
	"strconv"
	"strings"
)

// tokenizeCommand splits a workbench command line into arguments using
// redis-cli's quoting rules: whitespace separates tokens; double quotes
// support \xNN hex and \n \r \t \a \b escapes; single quotes are literal
// except \' and \\.
func tokenizeCommand(line string) ([]string, error) {
	var args []string
	i := 0
	n := len(line)
	for {
		// Skip whitespace between tokens.
		for i < n && (line[i] == ' ' || line[i] == '\t') {
			i++
		}
		if i >= n {
			break
		}
		var b strings.Builder
		switch line[i] {
		case '"':
			i++
			closed := false
			for i < n {
				c := line[i]
				if c == '\\' && i+1 < n {
					next := line[i+1]
					if next == 'x' && i+3 < n {
						if v, err := strconv.ParseUint(line[i+2:i+4], 16, 8); err == nil {
							b.WriteByte(byte(v))
							i += 4
							continue
						}
					}
					switch next {
					case 'n':
						b.WriteByte('\n')
					case 'r':
						b.WriteByte('\r')
					case 't':
						b.WriteByte('\t')
					case 'a':
						b.WriteByte('\a')
					case 'b':
						b.WriteByte('\b')
					default:
						b.WriteByte(next)
					}
					i += 2
					continue
				}
				if c == '"' {
					closed = true
					i++
					break
				}
				b.WriteByte(c)
				i++
			}
			if !closed {
				return nil, fmt.Errorf("unbalanced double quotes")
			}
			if i < n && line[i] != ' ' && line[i] != '\t' {
				return nil, fmt.Errorf("expected whitespace after closing quote")
			}
		case '\'':
			i++
			closed := false
			for i < n {
				c := line[i]
				if c == '\\' && i+1 < n && (line[i+1] == '\'' || line[i+1] == '\\') {
					b.WriteByte(line[i+1])
					i += 2
					continue
				}
				if c == '\'' {
					closed = true
					i++
					break
				}
				b.WriteByte(c)
				i++
			}
			if !closed {
				return nil, fmt.Errorf("unbalanced single quotes")
			}
			if i < n && line[i] != ' ' && line[i] != '\t' {
				return nil, fmt.Errorf("expected whitespace after closing quote")
			}
		default:
			for i < n && line[i] != ' ' && line[i] != '\t' {
				b.WriteByte(line[i])
				i++
			}
		}
		args = append(args, b.String())
	}
	if len(args) == 0 {
		return nil, fmt.Errorf("empty command")
	}
	return args, nil
}
