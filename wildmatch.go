package gitignore

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
			// End of bracket expression.
			if negate {
				matched = !matched
			}
			return matched, i + 1, true
		}
		first = false

		// POSIX character class: [:name:]
		if glob[i] == '[' && i+1 < len(glob) && glob[i+1] == ':' {
			end := findPosixClassEnd(glob, i+2)
			if end >= 0 {
				name := glob[i+2 : end]
				if matchPosixClass(name, ch) {
					matched = true
				}
				i = end + 2 // skip past :]
				continue
			}
			// No closing :], treat [ as literal.
		}

		// Resolve the current character (possibly escaped).
		var lo byte
		if glob[i] == '\\' && i+1 < len(glob) {
			i++
			lo = glob[i]
		} else {
			lo = glob[i]
		}
		i++

		// Check for range: lo-hi
		if i+1 < len(glob) && glob[i] == '-' && glob[i+1] != ']' {
			i++ // skip -
			var hi byte
			if glob[i] == '\\' && i+1 < len(glob) {
				i++
				hi = glob[i]
			} else {
				hi = glob[i]
			}
			i++
			if ch >= lo && ch <= hi {
				matched = true
			}
		} else {
			if ch == lo {
				matched = true
			}
		}
	}

	// No closing ] found.
	return false, 0, false
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

// matchPosixClass checks whether byte ch belongs to the named POSIX character class.
func matchPosixClass(name string, ch byte) bool {
	switch name {
	case "alnum":
		return ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z' || ch >= '0' && ch <= '9'
	case "alpha":
		return ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z'
	case "blank":
		return ch == ' ' || ch == '\t'
	case "cntrl":
		return ch < 0x20 || ch == 0x7f
	case "digit":
		return ch >= '0' && ch <= '9'
	case "graph":
		return ch > 0x20 && ch < 0x7f
	case "lower":
		return ch >= 'a' && ch <= 'z'
	case "print":
		return ch >= 0x20 && ch < 0x7f
	case "punct":
		return ch > 0x20 && ch < 0x7f &&
			(ch < 'a' || ch > 'z') && (ch < 'A' || ch > 'Z') && (ch < '0' || ch > '9')
	case "space":
		return ch == ' ' || ch == '\t' || ch == '\n' || ch == '\r' || ch == '\f' || ch == '\v'
	case "upper":
		return ch >= 'A' && ch <= 'Z'
	case "xdigit":
		return ch >= '0' && ch <= '9' || ch >= 'a' && ch <= 'f' || ch >= 'A' && ch <= 'F'
	}
	return false
}
