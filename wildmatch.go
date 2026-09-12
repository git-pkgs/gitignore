package gitignore

// posixClassOffset is the number of characters in the POSIX class delimiters
// "[:" and ":]", used when skipping past them during bracket parsing.
const posixClassOffset = 2

// matchSegments matches path segments against pattern segments using
// two-pointer backtracking. A doubleStar segment matches zero or more path
// segments. When tailAtLeastOne is set and the pattern ends in a doubleStar,
// that final doubleStar must match at least one path segment; this is how
// "foo/**" matches "foo/x" but not "foo" itself.
func matchSegments(patSegs []segment, pathSegs []string, tailAtLeastOne bool) bool {
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

	// Any remaining pattern segments were not entered by the main loop and
	// so match zero path segments. That is fine for leading and interior **
	// but not for a trailing one when tailAtLeastOne is set. Consecutive **
	// are collapsed at compile time, so at most one segment remains here in
	// the trailing case.
	remaining := px
	for px < len(patSegs) {
		if !patSegs[px].doubleStar {
			return false
		}
		px++
	}
	if tailAtLeastOne && remaining < len(patSegs) {
		return false
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
	i, negate := bracketStart(glob, pos)
	matched := false
	first := i // A leading ] is literal.

	for i < len(glob) {
		if glob[i] == ']' && i != first {
			return matched != negate, i + 1, true
		}

		var hit bool
		hit, i = matchBracketElement(glob, i, ch)
		if hit {
			matched = true
		}
	}

	return false, 0, false
}

func bracketStart(glob string, pos int) (int, bool) {
	i := pos + 1
	if i < len(glob) && (glob[i] == '!' || glob[i] == '^') {
		return i + 1, true
	}
	return i, false
}

func posixClassEnd(glob string, i int) int {
	if glob[i] == '[' && i+1 < len(glob) && glob[i+1] == ':' {
		return findPosixClassEnd(glob, i+posixClassOffset)
	}
	return -1
}

// matchBracketElement matches a single element inside a bracket expression:
// a POSIX class ([:name:]), a range (lo-hi), or a literal character.
// Returns whether ch matched and the new index past the element.
func matchBracketElement(glob string, i int, ch byte) (bool, int) {
	if end := posixClassEnd(glob, i); end >= 0 {
		return matchPosixClass(glob[i+posixClassOffset:end], ch), end + posixClassOffset
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
