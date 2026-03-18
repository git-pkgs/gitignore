package gitignore

// posixClassOffset is the number of characters in the POSIX class delimiters
// "[:" and ":]", used when skipping past them during bracket parsing.
const posixClassOffset = 2

// matchSegments matches path segments against pattern segments using two-pointer
// backtracking. A doubleStar segment matches zero or more path segments.
func matchSegments(patSegs []segment, pathSegs []string) bool {
	px, tx := 0, 0
	// Backtrack point for the most recent ** we passed.
	starPx, starTx := -1, -1

	for tx < len(pathSegs) {
		if px < len(patSegs) && patSegs[px].doubleStar {
			// Save backtrack point: try matching zero path segments first.
			starPx = px
			starTx = tx
			px++
			continue
		}
		if px < len(patSegs) && !patSegs[px].doubleStar && matchSegment(patSegs[px].raw, pathSegs[tx]) {
			px++
			tx++
			continue
		}
		// Mismatch. Backtrack: consume one more path segment with the last **.
		if starPx >= 0 {
			starTx++
			tx = starTx
			px = starPx + 1
			continue
		}
		return false
	}

	// Remaining pattern segments must all be ** to match.
	for px < len(patSegs) {
		if !patSegs[px].doubleStar {
			return false
		}
		px++
	}
	return true
}

// matchSegment matches a single path component against a glob pattern segment.
// Handles *, ?, [...], and \-escapes. Uses two-pointer backtracking for *.
func matchSegment(glob, text string) bool {
	gx, tx := 0, 0
	starGx, starTx := -1, -1

	for tx < len(text) {
		if gx < len(glob) {
			ch := glob[gx]
			switch {
			case ch == '\\' && gx+1 < len(glob):
				// Escaped character: match literally.
				gx++
				if text[tx] == glob[gx] {
					gx++
					tx++
					continue
				}
			case ch == '?':
				gx++
				tx++
				continue
			case ch == '*':
				// Save backtrack point and try matching zero chars.
				starGx = gx
				starTx = tx
				gx++
				continue
			case ch == '[':
				matched, newGx, ok := matchBracket(glob, gx, text[tx])
				if ok && matched {
					gx = newGx
					tx++
					continue
				}
				if !ok && text[tx] == '[' {
					// Invalid bracket (no closing ]); treat [ as literal.
					gx++
					tx++
					continue
				}
			default:
				if text[tx] == ch {
					gx++
					tx++
					continue
				}
			}
		}

		// Mismatch. Backtrack if we have a saved *.
		if starGx >= 0 {
			starTx++
			tx = starTx
			gx = starGx + 1
			continue
		}
		return false
	}

	// Consume trailing *'s in the pattern.
	for gx < len(glob) && glob[gx] == '*' {
		gx++
	}
	return gx == len(glob)
}

// matchBracket checks if byte ch matches the bracket expression starting at
// glob[pos] (the '['). Returns (matched, posAfterBracket, valid).
// If the bracket has no closing ']', valid is false.
func matchBracket(glob string, pos int, ch byte) (bool, int, bool) {
	i := pos + 1 // skip opening [
	if i >= len(glob) {
		return false, 0, false
	}

	negate := false
	if glob[i] == '!' || glob[i] == '^' {
		negate = true
		i++
	}

	matched := false
	first := true // ] is literal when it's the first char after [, [!, or [^

	for i < len(glob) {
		if glob[i] == ']' && !first {
			if negate {
				matched = !matched
			}
			return matched, i + 1, true
		}
		first = false

		var hit bool
		hit, i = matchBracketElement(glob, i, ch)
		if hit {
			matched = true
		}
	}

	return false, 0, false
}

// matchBracketElement matches a single element inside a bracket expression:
// a POSIX class ([:name:]), a range (lo-hi), or a literal character.
// Returns whether ch matched and the new index past the element.
func matchBracketElement(glob string, i int, ch byte) (bool, int) {
	// POSIX character class: [:name:]
	if glob[i] == '[' && i+1 < len(glob) && glob[i+1] == ':' {
		end := findPosixClassEnd(glob, i+posixClassOffset)
		if end >= 0 {
			name := glob[i+posixClassOffset : end]
			return matchPosixClass(name, ch), end + posixClassOffset
		}
	}

	lo, next := readBracketChar(glob, i)
	i = next

	// Check for range: lo-hi
	if i+1 < len(glob) && glob[i] == '-' && glob[i+1] != ']' {
		i++ // skip -
		hi, next := readBracketChar(glob, i)
		return ch >= lo && ch <= hi, next
	}
	return ch == lo, i
}

// readBracketChar reads a single (possibly escaped) character from a bracket
// expression and returns the character and the index after it.
func readBracketChar(glob string, i int) (byte, int) {
	if glob[i] == '\\' && i+1 < len(glob) {
		return glob[i+1], i + posixClassOffset
	}
	return glob[i], i + 1
}

// findPosixClassEnd finds the position of ':' in ":]" after startPos.
// Returns -1 if not found.
func findPosixClassEnd(glob string, startPos int) int {
	for i := startPos; i+1 < len(glob); i++ {
		if glob[i] == ':' && glob[i+1] == ']' {
			return i
		}
	}
	return -1
}

// posixClassMatchers maps POSIX character class names to their match functions.
var posixClassMatchers = map[string]func(byte) bool{
	"alnum": func(ch byte) bool {
		return ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z' || ch >= '0' && ch <= '9'
	},
	"alpha": func(ch byte) bool { return ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z' },
	"blank": func(ch byte) bool { return ch == ' ' || ch == '\t' },
	"cntrl": func(ch byte) bool { return ch < 0x20 || ch == 0x7f },
	"digit": func(ch byte) bool { return ch >= '0' && ch <= '9' },
	"graph": func(ch byte) bool { return ch > 0x20 && ch < 0x7f },
	"lower": func(ch byte) bool { return ch >= 'a' && ch <= 'z' },
	"print": func(ch byte) bool { return ch >= 0x20 && ch < 0x7f },
	"punct": func(ch byte) bool {
		return ch > 0x20 && ch < 0x7f &&
			(ch < 'a' || ch > 'z') && (ch < 'A' || ch > 'Z') && (ch < '0' || ch > '9')
	},
	"space": func(ch byte) bool {
		return ch == ' ' || ch == '\t' || ch == '\n' || ch == '\r' || ch == '\f' || ch == '\v'
	},
	"upper":  func(ch byte) bool { return ch >= 'A' && ch <= 'Z' },
	"xdigit": func(ch byte) bool { return ch >= '0' && ch <= '9' || ch >= 'a' && ch <= 'f' || ch >= 'A' && ch <= 'F' },
}

// matchPosixClass checks whether byte ch belongs to the named POSIX character class.
func matchPosixClass(name string, ch byte) bool {
	if fn, ok := posixClassMatchers[name]; ok {
		return fn(ch)
	}
	return false
}
