package store

// matchPattern implements Redis' glob-style key matching: '*' matches any run
// of characters, '?' matches a single character, '[...]' matches a character
// class, and '\\' escapes the next character. It operates on bytes, which is
// sufficient for key matching.
func matchPattern(pattern, s string) bool {
	return globMatch(pattern, s)
}

func globMatch(p, s string) bool {
	for len(p) > 0 {
		switch p[0] {
		case '*':
			// Collapse consecutive stars, then try to match the rest at every
			// suffix of s.
			for len(p) > 1 && p[1] == '*' {
				p = p[1:]
			}
			if len(p) == 1 {
				return true // trailing star matches everything
			}
			for i := 0; i <= len(s); i++ {
				if globMatch(p[1:], s[i:]) {
					return true
				}
			}
			return false
		case '?':
			if len(s) == 0 {
				return false
			}
			s = s[1:]
			p = p[1:]
		case '[':
			if len(s) == 0 {
				return false
			}
			matched, rest := matchClass(p, s[0])
			if !matched {
				return false
			}
			p = rest
			s = s[1:]
		case '\\':
			if len(p) >= 2 {
				p = p[1:]
			}
			if len(s) == 0 || s[0] != p[0] {
				return false
			}
			s = s[1:]
			p = p[1:]
		default:
			if len(s) == 0 || s[0] != p[0] {
				return false
			}
			s = s[1:]
			p = p[1:]
		}
	}
	return len(s) == 0
}

// matchClass evaluates a "[...]" class against c and returns whether it matched
// plus the remaining pattern after the closing ']'.
func matchClass(p string, c byte) (bool, string) {
	p = p[1:] // skip '['
	negate := false
	if len(p) > 0 && p[0] == '^' {
		negate = true
		p = p[1:]
	}
	matched := false
	for len(p) > 0 && p[0] != ']' {
		if len(p) >= 3 && p[1] == '-' && p[2] != ']' {
			lo, hi := p[0], p[2]
			if lo <= c && c <= hi {
				matched = true
			}
			p = p[3:]
		} else {
			if p[0] == c {
				matched = true
			}
			p = p[1:]
		}
	}
	if len(p) > 0 { // skip ']'
		p = p[1:]
	}
	if negate {
		matched = !matched
	}
	return matched, p
}
