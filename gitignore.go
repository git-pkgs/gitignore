package gitignore

import (
	"bufio"
	"bytes"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type segment struct {
	doubleStar bool
	raw        string // original glob text; empty if doubleStar
}

type pattern struct {
	segments      []segment
	negate        bool
	dirOnly       bool   // trailing slash pattern
	hasConcrete   bool   // has at least one non-** segment
	anchored      bool
	prefix        string // directory scope for nested .gitignore
	text          string // original pattern text before compilation
	source        string // file path this pattern came from, empty for programmatic
	line          int    // 1-based line number in source file
	literalSuffix string // fast-reject: last segment must end with this (e.g. ".log" from "*.log")
}

// Matcher checks paths against gitignore rules collected from .gitignore files,
// .git/info/exclude, and any additional patterns. Patterns from subdirectory
// .gitignore files are scoped to paths within that directory.
//
// Paths passed to Match should use forward slashes. Directory paths must
// have a trailing slash (e.g. "vendor/") so that directory-only patterns
// (those written with a trailing slash in .gitignore) match correctly.
//
// A Matcher is safe for concurrent use by multiple goroutines once
// construction is complete (after New, NewFromDirectory, or the last
// AddPatterns/AddFromFile call). Do not call AddPatterns or AddFromFile
// concurrently with Match.
type Matcher struct {
	patterns []pattern
	errors   []PatternError
}

// PatternError records a pattern that could not be compiled.
type PatternError struct {
	Pattern string // the original pattern text
	Source  string // file path, empty for programmatic patterns
	Line    int    // 1-based line number
	Message string
}

func (e PatternError) Error() string {
	if e.Source != "" {
		return e.Source + ":" + itoa(e.Line) + ": invalid pattern: " + e.Pattern + ": " + e.Message
	}
	return "invalid pattern: " + e.Pattern + ": " + e.Message
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}

// Errors returns any pattern compilation errors encountered while loading
// patterns. Invalid patterns are silently skipped during matching; this
// method lets callers detect and report them.
func (m *Matcher) Errors() []PatternError {
	return m.errors
}

// New creates a Matcher that reads patterns from the user's global
// excludes file (core.excludesfile), the repository's .git/info/exclude,
// and the root .gitignore. Patterns are loaded in priority order: global
// excludes first (lowest priority), then .git/info/exclude, then
// .gitignore (highest priority). Last-match-wins semantics means later
// patterns override earlier ones.
//
// The root parameter should be the repository working directory
// (containing .git/).
func New(root string) *Matcher {
	m := &Matcher{}

	// Read global excludes (lowest priority)
	if gef := globalExcludesFile(); gef != "" {
		if data, err := os.ReadFile(gef); err == nil {
			m.addPatterns(data, "", gef)
		}
	}

	// Read .git/info/exclude
	excludePath := filepath.Join(root, ".git", "info", "exclude")
	if data, err := os.ReadFile(excludePath); err == nil {
		m.addPatterns(data, "", excludePath)
	}

	// Read root .gitignore (highest priority)
	ignorePath := filepath.Join(root, ".gitignore")
	if data, err := os.ReadFile(ignorePath); err == nil {
		m.addPatterns(data, "", ignorePath)
	}

	return m
}

// globalExcludesFile returns the path to the user's global gitignore file.
// It checks (in order): git config core.excludesfile, $XDG_CONFIG_HOME/git/ignore,
// ~/.config/git/ignore. Returns empty string if none found.
func globalExcludesFile() string {
	// Try git config first.
	out, err := exec.Command("git", "config", "--global", "core.excludesfile").Output()
	if err == nil {
		path := strings.TrimSpace(string(out))
		if path != "" {
			return expandTilde(path)
		}
	}

	// Try XDG_CONFIG_HOME/git/ignore.
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		path := filepath.Join(xdg, "git", "ignore")
		if _, err := os.Stat(path); err == nil {
			return path
		}
	}

	// Fall back to ~/.config/git/ignore.
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	path := filepath.Join(home, ".config", "git", "ignore")
	if _, err := os.Stat(path); err == nil {
		return path
	}

	return ""
}

// expandTilde replaces a leading ~ with the user's home directory.
func expandTilde(path string) string {
	if !strings.HasPrefix(path, "~") {
		return path
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return path
	}
	return filepath.Join(home, path[1:])
}

// NewFromDirectory creates a Matcher by walking the directory tree rooted
// at root, loading every .gitignore file found along the way. Each nested
// .gitignore is scoped to its containing directory. The .git directory is
// skipped.
func NewFromDirectory(root string) *Matcher {
	m := New(root)
	_ = walkRecursive(root, "", m, nil)
	return m
}

// Walk walks the directory tree rooted at root, calling fn for each file
// and directory that is not ignored by gitignore rules. It loads .gitignore
// files as it descends, so patterns from deeper directories take effect for
// their subtrees. The .git directory is always skipped.
//
// Paths passed to fn are relative to root and use the OS path separator.
// The root directory itself is not passed to fn.
func Walk(root string, fn func(path string, d fs.DirEntry) error) error {
	m := New(root)
	return walkRecursive(root, "", m, fn)
}

func walkRecursive(root, rel string, m *Matcher, fn func(string, fs.DirEntry) error) error {
	dir := root
	if rel != "" {
		dir = filepath.Join(root, rel)
	}

	// Load .gitignore for this directory before processing entries.
	if rel != "" {
		igPath := filepath.Join(dir, ".gitignore")
		if _, err := os.Stat(igPath); err == nil {
			m.AddFromFile(igPath, filepath.ToSlash(rel))
		}
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}

	for _, entry := range entries {
		name := entry.Name()

		// Always skip .git directories.
		if name == ".git" && entry.IsDir() {
			continue
		}

		entryRel := name
		if rel != "" {
			entryRel = filepath.Join(rel, name)
		}
		matchPath := filepath.ToSlash(entryRel)
		if entry.IsDir() {
			matchPath += "/"
		}

		if m.Match(matchPath) {
			continue
		}

		if fn != nil {
			if err := fn(entryRel, entry); err != nil {
				return err
			}
		}

		if entry.IsDir() {
			if err := walkRecursive(root, entryRel, m, fn); err != nil {
				return err
			}
		}
	}

	return nil
}

// AddPatterns parses gitignore pattern lines from data and scopes them to
// the given relative directory. Pass an empty dir for root-level patterns.
func (m *Matcher) AddPatterns(data []byte, dir string) {
	m.addPatterns(data, dir, "")
}

// AddFromFile reads a .gitignore file at the given absolute path and scopes
// its patterns to the given relative directory.
func (m *Matcher) AddFromFile(absPath, relDir string) {
	data, err := os.ReadFile(absPath)
	if err != nil {
		return
	}
	m.addPatterns(data, relDir, absPath)
}

// Match returns true if the given path should be ignored.
// The path should be slash-separated and relative to the repository root.
// For directories, append a trailing slash (e.g. "vendor/").
// Uses last-match-wins semantics: iterates patterns in reverse and returns
// on the first match.
func (m *Matcher) Match(relPath string) bool {
	isDir := strings.HasSuffix(relPath, "/")
	if isDir {
		relPath = relPath[:len(relPath)-1]
	}
	return m.match(relPath, isDir)
}

// MatchPath returns true if the given path should be ignored.
// Unlike Match, it takes an explicit isDir flag instead of requiring
// a trailing slash convention. The path should be slash-separated,
// relative to the repository root, and should not have a trailing slash.
func (m *Matcher) MatchPath(relPath string, isDir bool) bool {
	return m.match(relPath, isDir)
}

// MatchResult describes which pattern matched a path and whether
// the path is ignored.
type MatchResult struct {
	Ignored bool   // true if the path should be ignored
	Matched bool   // true if any pattern matched (false means no pattern applied)
	Pattern string // original pattern text (empty if no match)
	Source  string // file the pattern came from (empty for programmatic patterns)
	Line    int    // 1-based line number in Source (0 if no match)
	Negate  bool   // true if the matching pattern was a negation (!)
}

// MatchDetail returns detailed information about which pattern matched
// the given path. If no pattern matches, Matched is false and Ignored
// is false. The path uses the same trailing-slash convention as Match.
func (m *Matcher) MatchDetail(relPath string) MatchResult {
	isDir := strings.HasSuffix(relPath, "/")
	if isDir {
		relPath = relPath[:len(relPath)-1]
	}
	return m.matchDetail(relPath, isDir)
}

func (m *Matcher) match(relPath string, isDir bool) bool {
	pathSegs := strings.Split(relPath, "/")
	lastSeg := pathSegs[len(pathSegs)-1]

	for i := len(m.patterns) - 1; i >= 0; i-- {
		p := &m.patterns[i]
		if p.literalSuffix != "" && !strings.HasSuffix(lastSeg, p.literalSuffix) {
			continue
		}
		if !matchPattern(p, pathSegs, isDir) {
			continue
		}
		return !p.negate
	}
	return false
}

func (m *Matcher) matchDetail(relPath string, isDir bool) MatchResult {
	pathSegs := strings.Split(relPath, "/")
	lastSeg := pathSegs[len(pathSegs)-1]

	for i := len(m.patterns) - 1; i >= 0; i-- {
		p := &m.patterns[i]
		if p.literalSuffix != "" && !strings.HasSuffix(lastSeg, p.literalSuffix) {
			continue
		}
		if !matchPattern(p, pathSegs, isDir) {
			continue
		}
		return MatchResult{
			Ignored: !p.negate,
			Matched: true,
			Pattern: p.text,
			Source:  p.source,
			Line:    p.line,
			Negate:  p.negate,
		}
	}
	return MatchResult{}
}

// matchPattern checks whether pathSegs matches the compiled pattern,
// including the directory prefix scope and dirOnly handling.
func matchPattern(p *pattern, pathSegs []string, isDir bool) bool {
	segs := pathSegs
	if p.prefix != "" {
		prefixSegs := strings.Split(p.prefix, "/")
		if len(segs) < len(prefixSegs) {
			return false
		}
		for i, ps := range prefixSegs {
			if segs[i] != ps {
				return false
			}
		}
		segs = segs[len(prefixSegs):]
	}

	if p.dirOnly {
		// Dir-only patterns (trailing slash): match the directory itself,
		// or match descendants (files/dirs under the matched directory).
		if matchSegments(p.segments, segs) {
			// Exact match. For non-dir paths, the pattern requires a directory.
			return isDir
		}
		// Only do descendant matching when the pattern identifies a specific
		// directory (has at least one non-** segment). Pure ** patterns like
		// "**/" only match directory paths directly.
		if !p.hasConcrete {
			return false
		}
		// Check if the path is a descendant of a matched directory by trying
		// the pattern against every prefix of the path segments.
		for end := len(segs) - 1; end >= 1; end-- {
			if matchSegments(p.segments, segs[:end]) {
				return true
			}
		}
		return false
	}

	return matchSegments(p.segments, segs)
}

func (m *Matcher) addPatterns(data []byte, dir, source string) {
	scanner := bufio.NewScanner(bytes.NewReader(data))
	lineNum := 0
	for scanner.Scan() {
		lineNum++
		line := trimTrailingSpaces(scanner.Text())
		if line == "" || line[0] == '#' {
			continue
		}
		p, errMsg := compilePattern(line, dir)
		if errMsg != "" {
			m.errors = append(m.errors, PatternError{
				Pattern: line,
				Source:  source,
				Line:    lineNum,
				Message: errMsg,
			})
			continue
		}
		p.text = line
		p.source = source
		p.line = lineNum
		m.patterns = append(m.patterns, p)
	}
}

// trimTrailingSpaces removes unescaped trailing spaces per gitignore spec.
// Tabs are not stripped (git only strips spaces). A backslash before a space
// escapes it, so "foo\ " keeps the trailing "\ ".
func trimTrailingSpaces(s string) string {
	i := len(s)
	for i > 0 && s[i-1] == ' ' {
		if i >= 2 && s[i-2] == '\\' {
			// This space is escaped; stop stripping here.
			break
		}
		i--
	}
	return s[:i]
}

// compilePattern compiles a gitignore pattern line into a pattern struct.
// Returns the compiled pattern and an empty string on success, or a zero
// pattern and an error message on failure.
func compilePattern(line, dir string) (pattern, string) {
	p := pattern{prefix: dir}

	// Handle negation
	if strings.HasPrefix(line, "!") {
		p.negate = true
		line = line[1:]
	}

	// Handle escaped leading characters (after negation is stripped)
	if len(line) >= 2 && line[0] == '\\' && (line[1] == '#' || line[1] == '!') {
		line = line[1:]
	}

	if line == "" || line == "/" {
		return pattern{}, "empty pattern"
	}

	// Detect and strip trailing slash (directory-only pattern).
	if len(line) > 1 && line[len(line)-1] == '/' {
		p.dirOnly = true
		line = line[:len(line)-1]
	}

	// Detect and strip leading slash (anchoring).
	hasLeadingSlash := line[0] == '/'
	if hasLeadingSlash {
		line = line[1:]
		if line == "" {
			return pattern{}, "empty pattern"
		}
	}

	// Split into segments on '/'.
	rawSegs := strings.Split(line, "/")

	// Determine anchoring: leading slash, or pattern contains a slash.
	p.anchored = hasLeadingSlash || len(rawSegs) > 1

	// Build segment list.
	segs := make([]segment, 0, len(rawSegs)+2)

	// If not anchored, prepend ** so it matches at any directory level.
	if !p.anchored {
		segs = append(segs, segment{doubleStar: true})
	}

	for _, raw := range rawSegs {
		if raw == "**" {
			segs = append(segs, segment{doubleStar: true})
		} else {
			segs = append(segs, segment{raw: raw})
		}
	}

	// Collapse consecutive ** segments.
	collapsed := segs[:1]
	for i := 1; i < len(segs); i++ {
		if segs[i].doubleStar && collapsed[len(collapsed)-1].doubleStar {
			continue
		}
		collapsed = append(collapsed, segs[i])
	}
	segs = collapsed

	// Validate bracket expressions: check closing ] exists and POSIX class names are valid.
	for _, seg := range segs {
		if seg.doubleStar {
			continue
		}
		if msg := validateBrackets(seg.raw); msg != "" {
			return pattern{}, msg
		}
	}

	// Append implicit ** at end for non-dir-only patterns so that matching
	// "foo" also matches "foo/anything". Dir-only patterns handle descendants
	// separately in matchPattern.
	if !p.dirOnly {
		if len(segs) == 0 || !segs[len(segs)-1].doubleStar {
			segs = append(segs, segment{doubleStar: true})
		}
	}

	p.segments = segs
	for _, s := range segs {
		if !s.doubleStar {
			p.hasConcrete = true
			break
		}
	}
	p.literalSuffix = extractLiteralSuffix(segs)
	return p, ""
}

// extractLiteralSuffix finds the literal trailing portion of the last concrete
// segment, for fast rejection. For example, "*.log" yields ".log", "test_*.go"
// yields ".go". Only extracts a suffix when the segment is a simple star-prefix
// glob with no brackets, escapes, or question marks in the suffix portion.
func extractLiteralSuffix(segs []segment) string {
	// Find the last non-** segment.
	var last string
	for i := len(segs) - 1; i >= 0; i-- {
		if !segs[i].doubleStar {
			last = segs[i].raw
			break
		}
	}
	if last == "" {
		return ""
	}

	// Find the last * in the segment. Everything after it must be literal.
	starIdx := strings.LastIndex(last, "*")
	if starIdx < 0 {
		return ""
	}
	suffix := last[starIdx+1:]
	if suffix == "" {
		return ""
	}

	// Bail if the suffix contains wildcards, brackets, or escapes.
	for i := 0; i < len(suffix); i++ {
		switch suffix[i] {
		case '*', '?', '[', '\\':
			return ""
		}
	}
	return suffix
}

// validateBrackets checks that all bracket expressions in a glob segment
// have valid closing brackets and known POSIX class names.
// Returns empty string on success, or an error message.
func validateBrackets(glob string) string {
	for i := 0; i < len(glob); i++ {
		if glob[i] == '\\' && i+1 < len(glob) {
			i++ // skip escaped char
			continue
		}
		if glob[i] != '[' {
			continue
		}
		// Find the matching close bracket.
		j := i + 1
		if j < len(glob) && (glob[j] == '!' || glob[j] == '^') {
			j++
		}
		if j < len(glob) && glob[j] == ']' {
			j++ // ] as first char is literal
		}
		for j < len(glob) && glob[j] != ']' {
			if glob[j] == '\\' && j+1 < len(glob) {
				j += 2
				continue
			}
			if glob[j] == '[' && j+1 < len(glob) && glob[j+1] == ':' {
				end := findPosixClassEnd(glob, j+2)
				if end >= 0 {
					name := glob[j+2 : end]
					if !validPosixClassName(name) {
						return "unknown POSIX class [:" + name + ":]"
					}
					j = end + 2
					continue
				}
			}
			j++
		}
		if j >= len(glob) {
			// No closing bracket; treat [ as literal (this is fine).
			continue
		}
		i = j // skip to closing ]
	}
	return ""
}

func validPosixClassName(name string) bool {
	switch name {
	case "alnum", "alpha", "blank", "cntrl", "digit", "graph",
		"lower", "print", "punct", "space", "upper", "xdigit":
		return true
	}
	return false
}
