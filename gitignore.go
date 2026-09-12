package gitignore

import (
	"bytes"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
)

type segment struct {
	doubleStar bool
	raw        string // original glob text; empty if doubleStar
}

type pattern struct {
	segments       []segment
	negate         bool
	dirOnly        bool   // trailing slash pattern
	tailDoubleStar bool   // pattern ends in "/**"
	baseOnly       bool   // ** followed by one concrete segment
	text           string // original pattern text before compilation
	source         string // file path this pattern came from, empty for programmatic
	line           int    // 1-based line number in source file
	literalSuffix  string // fast-reject: last segment must end with this (e.g. ".log" from "*.log")
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
	maxIgnoreFileSize int64
	patterns          []pattern
	groups            []patternGroup
	errors            []PatternError
}

type patternGroup struct {
	prefix     []string
	start, end int
}

// PatternError records a pattern compilation error or a skipped oversized file.
type PatternError struct {
	Pattern string // the original pattern text
	Source  string // file path, empty for programmatic patterns
	Line    int    // 1-based line number; zero for a file-size error
	Message string
}

func (e PatternError) Error() string {
	if e.Line == 0 && e.Source != "" {
		return e.Source + ": " + e.Message
	}
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

// Errors returns pattern compilation errors and skipped oversized files.
// File-size errors have a source path and a zero line number.
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
// (containing .git/). If root is empty, no filesystem patterns are
// loaded and the returned Matcher is empty. Use AddPatterns or
// AddFromFile to add patterns programmatically.
//
// Options such as MaxIgnoreFileSize apply to files loaded here and to
// later AddFromFile calls; oversized files are skipped and recorded in
// Errors with Line set to zero.
func New(root string, opts ...Option) *Matcher {
	m, _ := newMatcher(root, opts)
	return m
}

func newMatcher(root string, opts []Option) (*Matcher, error) {
	m := &Matcher{}
	for _, opt := range opts {
		opt(m)
	}

	if root == "" {
		return m, nil
	}

	var firstErr error
	for _, path := range []string{
		globalExcludesFile(),
		filepath.Join(root, ".git", "info", "exclude"),
		filepath.Join(root, ".gitignore"),
	} {
		if path == "" {
			continue
		}
		if err := m.addFromFile(path, ""); firstErr == nil {
			firstErr = err
		}
	}
	return m, firstErr
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
// skipped. Oversized files under MaxIgnoreFileSize are skipped and recorded
// in Errors.
func NewFromDirectory(root string, opts ...Option) *Matcher {
	w := walker{root: root, matcher: New(root, opts...), retainPatterns: true}
	_ = w.walk("")
	return w.matcher
}

// Walk walks the directory tree rooted at root, calling fn for each file
// and directory that is not ignored by gitignore rules. It loads .gitignore
// files as it descends, so patterns from deeper directories take effect for
// their subtrees. The .git directory is always skipped.
//
// Paths passed to fn are relative to root and use the OS path separator.
// The root directory itself is not passed to fn.
//
// With MaxIgnoreFileSize set, an oversized ignore file stops the walk and
// is returned as an *IgnoreFileSizeError.
func Walk(root string, fn func(path string, d fs.DirEntry) error, opts ...Option) error {
	m, err := newMatcher(root, opts)
	if err != nil {
		return err
	}
	w := walker{root: root, matcher: m, fn: fn, stopOnSizeError: true}
	return w.walk("")
}

// WalkFrom walks the directory tree starting at a subdirectory of root,
// calling fn for each file and directory that is not ignored by gitignore
// rules. Unlike Walk, it separates the repository root (used to find
// .git/info/exclude and the root .gitignore) from the directory where the
// walk begins. Any .gitignore files between root and start are loaded
// before the walk begins, so their patterns apply correctly.
//
// The start parameter is a path relative to root (e.g. "src/pkg"),
// using either forward slashes or the OS path separator. Paths passed
// to fn are relative to root (not to start) and use the OS path
// separator. The start directory itself is passed to fn.
//
// With MaxIgnoreFileSize set, an oversized ignore file stops the walk and
// is returned as an *IgnoreFileSizeError.
func WalkFrom(root, start string, fn func(path string, d fs.DirEntry) error, opts ...Option) error {
	if start == "" || start == "." {
		return Walk(root, fn, opts...)
	}

	start = filepath.Clean(start)
	if start == "." {
		return Walk(root, fn, opts...)
	}

	m, err := newMatcher(root, opts)
	if err != nil {
		return err
	}

	// Load .gitignore from each ancestor directory between root and start
	// (exclusive of start itself, which the walker loads).
	{
		slashed := filepath.ToSlash(start)
		for off := 0; ; {
			i := strings.IndexByte(slashed[off:], '/')
			if i == -1 {
				break
			}
			prefix := slashed[:off+i]
			if err := m.addFromFile(filepath.Join(root, prefix, ".gitignore"), prefix); err != nil {
				return err
			}
			off += i + 1
		}
	}

	startDir := filepath.Join(root, start)
	info, err := os.Stat(startDir)
	if err != nil {
		return err
	}

	w := walker{root: root, start: start, matcher: m, fn: fn, stopOnSizeError: true}
	if err := w.visit(start, fs.FileInfoToDirEntry(info)); err != nil {
		return err
	}

	return w.walk(start)
}

type walker struct {
	root, start     string
	matcher         *Matcher
	fn              func(string, fs.DirEntry) error
	retainPatterns  bool
	stopOnSizeError bool
}

func (w *walker) visit(path string, entry fs.DirEntry) error {
	if w.fn == nil {
		return nil
	}
	return w.fn(path, entry)
}

func (w *walker) walk(rel string) error {
	m := w.matcher
	if !w.retainPatterns {
		defer m.restorePatterns(len(m.patterns), len(m.groups))
	}
	dir := w.root

	// Load .gitignore for this directory before processing entries.
	if rel != "" {
		dir = filepath.Join(w.root, rel)
		if err := m.addFromFile(filepath.Join(dir, ".gitignore"), filepath.ToSlash(rel)); err != nil && w.stopOnSizeError {
			return err
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

		entryRel := filepath.Join(rel, name)
		// Descendant directories have already passed their parent checks.
		if m.match(filepath.ToSlash(entryRel), entry.IsDir(), rel == w.start) {
			continue
		}

		if err := w.visit(entryRel, entry); err != nil {
			return err
		}

		if entry.IsDir() {
			if err := w.walk(entryRel); err != nil {
				return err
			}
		}
	}

	return nil
}

func (m *Matcher) restorePatterns(patternCount, groupCount int) {
	clear(m.patterns[patternCount:])
	m.patterns = m.patterns[:patternCount]
	clear(m.groups[groupCount:])
	m.groups = m.groups[:groupCount]
	if groupCount > 0 {
		m.groups[groupCount-1].end = patternCount
	}
}

// AddPatterns parses gitignore pattern lines from data and scopes them to
// the given relative directory. Pass an empty dir for root-level patterns.
func (m *Matcher) AddPatterns(data []byte, dir string) {
	m.addPatterns(data, dir, "")
}

// AddFromFile reads a .gitignore file at the given absolute path and scopes
// its patterns to the given relative directory. It uses the matcher's file-size
// limit, if set, and records oversized files in Errors without applying any rules.
func (m *Matcher) AddFromFile(absPath, relDir string) {
	_ = m.addFromFile(absPath, relDir)
}

func (m *Matcher) addFromFile(absPath, relDir string) error {
	data, err := readIgnoreFile(absPath, m.maxIgnoreFileSize)
	if err != nil {
		if sizeErr, ok := err.(*IgnoreFileSizeError); ok {
			m.errors = append(m.errors, PatternError{Source: absPath, Message: sizeErr.message()})
			return err
		}
		return nil
	}
	m.addPatterns(data, relDir, absPath)
	return nil
}

// Match returns true if the given path should be ignored.
// The path should be slash-separated and relative to the repository root.
// For directories, append a trailing slash (e.g. "vendor/").
// An ignored parent directory makes its descendants ignored. Otherwise,
// the last matching rule determines whether the path is ignored.
func (m *Matcher) Match(relPath string) bool {
	isDir := strings.HasSuffix(relPath, "/")
	if isDir {
		relPath = relPath[:len(relPath)-1]
	}
	return m.match(relPath, isDir, true)
}

// MatchPath returns true if the given path should be ignored.
// Unlike Match, it takes an explicit isDir flag instead of requiring
// a trailing slash convention. The path should be slash-separated,
// relative to the repository root, and should not have a trailing slash.
func (m *Matcher) MatchPath(relPath string, isDir bool) bool {
	return m.match(relPath, isDir, true)
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

// match reports whether relPath is ignored. Git decides ignore status
// while walking the tree and does not enter an excluded directory, so a
// path is ignored if any of its parent directories is. That is checked
// here unless the directory walk has already checked the parents.
func (m *Matcher) match(relPath string, isDir, checkParents bool) bool {
	if len(m.patterns) == 0 {
		return false
	}
	var buf [pathBufferSize]string
	pathSegs := splitPath(relPath, buf[:0])
	if checkParents {
		for end := 1; end < len(pathSegs); end++ {
			if idx := m.findMatch(pathSegs[:end], true); idx >= 0 && !m.patterns[idx].negate {
				return true
			}
		}
	}
	idx := m.findMatch(pathSegs, isDir)
	return idx >= 0 && !m.patterns[idx].negate
}

func (m *Matcher) matchDetail(relPath string, isDir bool) MatchResult {
	if len(m.patterns) == 0 {
		return MatchResult{}
	}
	var buf [pathBufferSize]string
	pathSegs := splitPath(relPath, buf[:0])
	for end := 1; end < len(pathSegs); end++ {
		if idx := m.findMatch(pathSegs[:end], true); idx >= 0 && !m.patterns[idx].negate {
			return m.resultFor(idx)
		}
	}
	if idx := m.findMatch(pathSegs, isDir); idx >= 0 {
		return m.resultFor(idx)
	}
	return MatchResult{}
}

const pathBufferSize = 16

func splitPath(path string, segments []string) []string {
	for part := range strings.SplitSeq(path, "/") {
		if len(segments) == cap(segments) {
			return strings.Split(path, "/")
		}
		segments = append(segments, part)
	}
	return segments
}

// findMatch returns the index of the last pattern that matches pathSegs,
// or -1 if none does. Patterns are scanned from last to first because
// gitignore uses last-match-wins ordering.
func (m *Matcher) findMatch(pathSegs []string, isDir bool) int {
	for g := len(m.groups) - 1; g >= 0; g-- {
		group := &m.groups[g]
		if !matchScope(pathSegs, group.prefix) {
			continue
		}
		segs := pathSegs[len(group.prefix):]
		if idx := findPattern(m.patterns[group.start:group.end], segs, isDir); idx >= 0 {
			return group.start + idx
		}
	}
	return -1
}

func findPattern(patterns []pattern, segs []string, isDir bool) int {
	lastSeg := segs[len(segs)-1]
	for i := len(patterns) - 1; i >= 0; i-- {
		p := &patterns[i]
		if p.literalSuffix != "" && !strings.HasSuffix(lastSeg, p.literalSuffix) {
			continue
		}
		if matchPattern(p, segs, isDir) {
			return i
		}
	}
	return -1
}

func (m *Matcher) resultFor(idx int) MatchResult {
	p := &m.patterns[idx]
	return MatchResult{
		Ignored: !p.negate,
		Matched: true,
		Pattern: p.text,
		Source:  p.source,
		Line:    p.line,
		Negate:  p.negate,
	}
}

// Nested rules cannot match their containing directory.
func matchScope(segs, prefix []string) bool {
	if len(segs) <= len(prefix) {
		return false
	}
	for i, part := range prefix {
		if segs[i] != part {
			return false
		}
	}
	return true
}

func matchPattern(p *pattern, segs []string, isDir bool) bool {
	if p.dirOnly && !isDir {
		return false
	}
	if p.baseOnly {
		return matchSegment(p.segments[len(p.segments)-1].raw, segs[len(segs)-1])
	}
	return matchSegments(p.segments, segs, p.tailDoubleStar)
}

func (m *Matcher) addPatterns(data []byte, dir, source string) {
	start := len(m.patterns)
	var prefix []string
	if dir != "" {
		prefix = strings.Split(dir, "/")
	}
	lineNum := 0
	for len(data) > 0 {
		var raw []byte
		raw, data, _ = bytes.Cut(data, []byte{'\n'})
		raw = bytes.TrimSuffix(raw, []byte{'\r'})
		lineNum++
		line := trimTrailingSpaces(string(raw))
		if line == "" || line[0] == '#' {
			continue
		}
		p, errMsg := compilePattern(line)
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
	if len(m.patterns) > start {
		if n := len(m.groups); n > 0 && slices.Equal(m.groups[n-1].prefix, prefix) {
			m.groups[n-1].end = len(m.patterns)
		} else {
			m.groups = append(m.groups, patternGroup{prefix: prefix, start: start, end: len(m.patterns)})
		}
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
func compilePattern(line string) (pattern, string) {
	var p pattern

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

	// A pattern ending "/**" matches everything inside the named directory
	// but not the directory itself. Record that here so matchSegments can
	// require the trailing ** to consume at least one path segment. Git
	// treats any run of two or more asterisks as ** when it forms a whole
	// segment, so "/***" and beyond count too.
	if i := strings.LastIndexByte(line, '/'); i >= 0 {
		p.tailDoubleStar = allStars(line[i+1:])
	}

	segs := buildSegments(line, hasLeadingSlash)

	if msg := validateSegmentBrackets(segs); msg != "" {
		return pattern{}, msg
	}

	p.segments = segs
	const basePatternSegments = 2
	p.baseOnly = len(segs) == basePatternSegments && segs[0].doubleStar && !segs[1].doubleStar
	p.literalSuffix = extractLiteralSuffix(segs)
	return p, ""
}

// buildSegments splits a pattern line into segments, prepends ** for unanchored
// patterns, and collapses consecutive ** segments.
func buildSegments(line string, hasLeadingSlash bool) []segment {
	rawSegs := strings.Split(line, "/")
	anchored := hasLeadingSlash || len(rawSegs) > 1

	const extraStarSegments = 2
	segs := make([]segment, 0, len(rawSegs)+extraStarSegments)

	if !anchored {
		segs = append(segs, segment{doubleStar: true})
	}

	for _, raw := range rawSegs {
		if allStars(raw) {
			segs = append(segs, segment{doubleStar: true})
		} else {
			segs = append(segs, segment{raw: raw})
		}
	}

	collapsed := segs[:1]
	for i := 1; i < len(segs); i++ {
		if segs[i].doubleStar && collapsed[len(collapsed)-1].doubleStar {
			continue
		}
		collapsed = append(collapsed, segs[i])
	}
	return collapsed
}

// allStars reports whether s consists of two or more '*' bytes and nothing
// else. Git's wildmatch treats such a segment the same as **.
func allStars(s string) bool {
	if len(s) < 2 {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] != '*' {
			return false
		}
	}
	return true
}

// validateSegmentBrackets checks bracket expressions in all concrete segments.
func validateSegmentBrackets(segs []segment) string {
	for _, seg := range segs {
		if seg.doubleStar {
			continue
		}
		if msg := validateBrackets(seg.raw); msg != "" {
			return msg
		}
	}
	return ""
}

// extractLiteralSuffix finds the literal trailing portion of the last concrete
// segment, for fast rejection. For example, "*.log" yields ".log", "test_*.go"
// yields ".go". Literal segments use the entire name. Suffixes containing
// brackets, escapes, or question marks are excluded.
//
// The suffix is only extracted when the last segment is concrete (not **),
// because the fast-reject check compares against the final path segment.
// When the pattern ends with **, the concrete segment could match any path
// segment, making a last-segment-only check incorrect.
func extractLiteralSuffix(segs []segment) string {
	if len(segs) == 0 || segs[len(segs)-1].doubleStar {
		return ""
	}

	// The last segment is concrete; use it for suffix extraction.
	last := segs[len(segs)-1].raw
	if last == "" {
		return ""
	}

	// Find the last * in the segment. Everything after it must be literal.
	starIdx := strings.LastIndex(last, "*")
	suffix := last[starIdx+1:]
	if suffix == "" {
		return ""
	}

	// Bail if the suffix contains wildcards, brackets, or escapes. A ']'
	// here means the '*' found above was inside a bracket expression and
	// is literal, so the suffix boundary is wrong.
	for i := 0; i < len(suffix); i++ {
		switch suffix[i] {
		case '*', '?', '[', ']', '\\':
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
		msg, end := validateBracketAt(glob, i)
		if msg != "" {
			return msg
		}
		if end >= 0 {
			i = end
		}
	}
	return ""
}

// validateBracketAt validates the bracket expression starting at glob[pos].
// Returns an error message if invalid, and the index of the closing ']' (or -1
// if the bracket has no closing ']' and should be treated as literal).
func validateBracketAt(glob string, pos int) (string, int) {
	j, _ := bracketStart(glob, pos)
	if j < len(glob) && glob[j] == ']' {
		j++ // ] as first char is literal
	}
	for j < len(glob) && glob[j] != ']' {
		if end := posixClassEnd(glob, j); end >= 0 {
			name := glob[j+posixClassOffset : end]
			if _, ok := posixClassMatchers[name]; !ok {
				return "unknown POSIX class [:" + name + ":]", -1
			}
			j = end + posixClassOffset
			continue
		}
		_, j = readBracketChar(glob, j)
	}
	if j >= len(glob) {
		return "unclosed bracket expression", -1
	}
	return "", j
}
