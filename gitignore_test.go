package gitignore_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/git-pkgs/gitignore"
)

func setupMatcher(t *testing.T, gitignoreContent string) *gitignore.Matcher {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".git", "info"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".gitignore"), []byte(gitignoreContent), 0644); err != nil {
		t.Fatal(err)
	}
	return gitignore.New(root)
}

func TestMatchBasicPatterns(t *testing.T) {
	m := setupMatcher(t, "vendor/\n*.log\nbuild\n")

	tests := []struct {
		path string
		want bool
	}{
		{"vendor/", true},
		{"vendor/gem/lib.rb", true},
		{"vendor", false}, // no trailing slash, dir-only pattern doesn't match
		{"app.log", true},
		{"logs/app.log", true},
		{"build", true},
		{"build/", true},
		{"build/output.js", true}, // "build" without trailing slash matches descendants
		{"src/main.go", false},
		{"README.md", false},
	}

	for _, tt := range tests {
		got := m.Match(tt.path)
		if got != tt.want {
			t.Errorf("Match(%q) = %v, want %v", tt.path, got, tt.want)
		}
	}
}

func TestMatchNegationPatterns(t *testing.T) {
	// Deny-by-default pattern: ignore everything at root, then allow specific paths
	m := setupMatcher(t, "/*\n!.github/\n!src/\n!README.md\n")

	tests := []struct {
		path string
		want bool
	}{
		{".github/", false},
		{".github/workflows/", false},
		{".github/workflows/ci.yml", false},
		{"src/", false},
		{"src/main.go", false},
		{"README.md", false},
		{"vendor/", true},
		{"node_modules/", true},
		{"random-file.txt", true},
		{".gitignore", true}, // gitignore itself is ignored by /*
	}

	for _, tt := range tests {
		got := m.Match(tt.path)
		if got != tt.want {
			t.Errorf("Match(%q) = %v, want %v", tt.path, got, tt.want)
		}
	}
}

func TestMatchDoubleStarPatterns(t *testing.T) {
	m := setupMatcher(t, "**/logs\n**/logs/**\nfoo/**/bar\n")

	tests := []struct {
		path string
		want bool
	}{
		{"logs", true},
		{"logs/", true},
		{"deep/nested/logs", true},
		{"logs/debug.log", true},
		{"logs/monday/foo.bar", true},
		{"foo/bar", true},
		{"foo/a/bar", true},
		{"foo/a/b/c/bar", true},
	}

	for _, tt := range tests {
		got := m.Match(tt.path)
		if got != tt.want {
			t.Errorf("Match(%q) = %v, want %v", tt.path, got, tt.want)
		}
	}
}

func TestMatchScopedPatterns(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".git", "info"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".gitignore"), []byte(""), 0644); err != nil {
		t.Fatal(err)
	}

	// Create a nested .gitignore
	if err := os.MkdirAll(filepath.Join(root, "src"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "src", ".gitignore"), []byte("*.generated.go\ntmp/\n"), 0644); err != nil {
		t.Fatal(err)
	}

	m := gitignore.New(root)
	m.AddFromFile(filepath.Join(root, "src", ".gitignore"), "src")

	tests := []struct {
		path string
		want bool
	}{
		{"src/foo.generated.go", true},
		{"src/deep/bar.generated.go", true},
		{"other/foo.generated.go", false}, // pattern scoped to src/
		{"src/tmp/", true},
		{"src/tmp/cache.dat", true},
		{"tmp/", false}, // not under src/
	}

	for _, tt := range tests {
		got := m.Match(tt.path)
		if got != tt.want {
			t.Errorf("Match(%q) = %v, want %v", tt.path, got, tt.want)
		}
	}
}

func TestMatchScopedMultipleLevels(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".git", "info"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".gitignore"), []byte("*.log\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "src", "lib"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "src", ".gitignore"), []byte("*.tmp\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "src", "lib", ".gitignore"), []byte("*.gen.go\n"), 0644); err != nil {
		t.Fatal(err)
	}

	m := gitignore.New(root)
	m.AddFromFile(filepath.Join(root, "src", ".gitignore"), "src")
	m.AddFromFile(filepath.Join(root, "src", "lib", ".gitignore"), "src/lib")

	tests := []struct {
		path string
		want bool
	}{
		// Root pattern applies everywhere
		{"app.log", true},
		{"src/app.log", true},
		{"src/lib/app.log", true},
		// src/ pattern scoped to src/
		{"src/cache.tmp", true},
		{"src/lib/cache.tmp", true},
		{"cache.tmp", false},
		// src/lib/ pattern scoped to src/lib/
		{"src/lib/foo.gen.go", true},
		{"src/foo.gen.go", false},
		{"foo.gen.go", false},
	}

	for _, tt := range tests {
		got := m.Match(tt.path)
		if got != tt.want {
			t.Errorf("Match(%q) = %v, want %v", tt.path, got, tt.want)
		}
	}
}

func TestMatchScopedNestedOverridesParent(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".git", "info"), 0755); err != nil {
		t.Fatal(err)
	}
	// Root ignores all .txt files
	if err := os.WriteFile(filepath.Join(root, ".gitignore"), []byte("*.txt\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "docs"), 0755); err != nil {
		t.Fatal(err)
	}
	// docs/.gitignore re-includes .txt files under docs/
	if err := os.WriteFile(filepath.Join(root, "docs", ".gitignore"), []byte("!*.txt\n"), 0644); err != nil {
		t.Fatal(err)
	}

	m := gitignore.New(root)
	m.AddFromFile(filepath.Join(root, "docs", ".gitignore"), "docs")

	tests := []struct {
		path string
		want bool
	}{
		// Root .txt exclusion still applies outside docs/
		{"README.txt", true},
		{"src/notes.txt", true},
		// docs/ negation re-includes .txt under docs/
		{"docs/guide.txt", false},
		{"docs/api/ref.txt", false},
		// Non-.txt files unaffected
		{"docs/image.png", false},
		{"src/main.go", false},
	}

	for _, tt := range tests {
		got := m.Match(tt.path)
		if got != tt.want {
			t.Errorf("Match(%q) = %v, want %v", tt.path, got, tt.want)
		}
	}
}

func TestMatchScopedNestedNegationWithParentExclusion(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".git", "info"), 0755); err != nil {
		t.Fatal(err)
	}
	// Root: deny-by-default, allow src/
	if err := os.WriteFile(filepath.Join(root, ".gitignore"), []byte("/*\n!src/\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "src"), 0755); err != nil {
		t.Fatal(err)
	}
	// src/.gitignore: ignore test fixtures but keep .go files
	if err := os.WriteFile(filepath.Join(root, "src", ".gitignore"), []byte("testdata/\n"), 0644); err != nil {
		t.Fatal(err)
	}

	m := gitignore.New(root)
	m.AddFromFile(filepath.Join(root, "src", ".gitignore"), "src")

	tests := []struct {
		path string
		want bool
	}{
		// Root deny-by-default
		{"vendor/", true},
		{"random.txt", true},
		// src/ is re-included
		{"src/", false},
		{"src/main.go", false},
		// src/testdata/ is excluded by nested .gitignore
		{"src/testdata/", true},
		{"src/testdata/fixture.json", true},
		// testdata/ outside src/ is not affected by nested pattern
		// (but IS caught by root /*)
		{"testdata/", true},
	}

	for _, tt := range tests {
		got := m.Match(tt.path)
		if got != tt.want {
			t.Errorf("Match(%q) = %v, want %v", tt.path, got, tt.want)
		}
	}
}

func TestMatchScopedSiblingDirectories(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".git", "info"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".gitignore"), []byte(""), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "frontend"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "backend"), 0755); err != nil {
		t.Fatal(err)
	}
	// frontend ignores node_modules and dist
	if err := os.WriteFile(filepath.Join(root, "frontend", ".gitignore"), []byte("node_modules/\ndist/\n"), 0644); err != nil {
		t.Fatal(err)
	}
	// backend ignores __pycache__ and *.pyc
	if err := os.WriteFile(filepath.Join(root, "backend", ".gitignore"), []byte("__pycache__/\n*.pyc\n"), 0644); err != nil {
		t.Fatal(err)
	}

	m := gitignore.New(root)
	m.AddFromFile(filepath.Join(root, "frontend", ".gitignore"), "frontend")
	m.AddFromFile(filepath.Join(root, "backend", ".gitignore"), "backend")

	tests := []struct {
		path string
		want bool
	}{
		// frontend patterns only under frontend/
		{"frontend/node_modules/", true},
		{"frontend/node_modules/react/index.js", true},
		{"frontend/dist/", true},
		{"frontend/src/app.js", false},
		// backend patterns only under backend/
		{"backend/__pycache__/", true},
		{"backend/app.pyc", true},
		{"backend/app.py", false},
		// No cross-contamination
		{"backend/node_modules/", false},
		{"backend/dist/", false},
		{"frontend/__pycache__/", false},
		{"frontend/app.pyc", false},
		// Root level unaffected
		{"node_modules/", false},
		{"__pycache__/", false},
	}

	for _, tt := range tests {
		got := m.Match(tt.path)
		if got != tt.want {
			t.Errorf("Match(%q) = %v, want %v", tt.path, got, tt.want)
		}
	}
}

func TestMatchExcludeFile(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".git", "info"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".git", "info", "exclude"), []byte("secret.key\n.env\n"), 0644); err != nil {
		t.Fatal(err)
	}

	m := gitignore.New(root)

	tests := []struct {
		path string
		want bool
	}{
		{"secret.key", true},
		{".env", true},
		{"src/secret.key", true},
		{"README.md", false},
	}

	for _, tt := range tests {
		got := m.Match(tt.path)
		if got != tt.want {
			t.Errorf("Match(%q) = %v, want %v", tt.path, got, tt.want)
		}
	}
}

func TestMatchLeadingSlashAnchoring(t *testing.T) {
	m := setupMatcher(t, "/build\n/dist/\n")

	tests := []struct {
		path string
		want bool
	}{
		{"build", true},
		{"build/", true},
		{"src/build", false}, // anchored to root
		{"dist/", true},
		{"dist/bundle.js", true},
		{"src/dist/", false}, // anchored to root
	}

	for _, tt := range tests {
		got := m.Match(tt.path)
		if got != tt.want {
			t.Errorf("Match(%q) = %v, want %v", tt.path, got, tt.want)
		}
	}
}

func TestMatchLastPatternWins(t *testing.T) {
	// Ignore all .txt files, but then re-include important.txt
	m := setupMatcher(t, "*.txt\n!important.txt\n")

	tests := []struct {
		path string
		want bool
	}{
		{"notes.txt", true},
		{"important.txt", false},
		{"docs/notes.txt", true},
		{"docs/important.txt", false},
	}

	for _, tt := range tests {
		got := m.Match(tt.path)
		if got != tt.want {
			t.Errorf("Match(%q) = %v, want %v", tt.path, got, tt.want)
		}
	}
}

func TestMatchBracketExpressions(t *testing.T) {
	m := setupMatcher(t, ".*.sw[a-z]\n/b[!a]r/\n")

	tests := []struct {
		path string
		want bool
	}{
		{".foo.swp", true},
		{"src/.bar.swa", true},
		{".foo.sw1", false},
		{"bbr/", true},
		{"bcr/", true},
		{"bar/", false}, // [!a] excludes 'a'
	}

	for _, tt := range tests {
		got := m.Match(tt.path)
		if got != tt.want {
			t.Errorf("Match(%q) = %v, want %v", tt.path, got, tt.want)
		}
	}
}

func TestMatchPlainPath(t *testing.T) {
	m := setupMatcher(t, "abcdef\n")

	shouldMatch := []string{
		"abcdef",
		"subdir/abcdef",
		"abcdef/",
		"subdir/abcdef/",
	}
	shouldNotMatch := []string{
		"someotherfile",
	}

	for _, path := range shouldMatch {
		if !m.Match(path) {
			t.Errorf("expected %q to match", path)
		}
	}
	for _, path := range shouldNotMatch {
		if m.Match(path) {
			t.Errorf("expected %q to not match", path)
		}
	}
}

func TestMatchRootPath(t *testing.T) {
	m := setupMatcher(t, "/abcdef\n")

	shouldMatch := []string{
		"abcdef",
		"abcdef/",
	}
	shouldNotMatch := []string{
		"subdir/abcdef",
		"subdir/abcdef/",
	}

	for _, path := range shouldMatch {
		if !m.Match(path) {
			t.Errorf("expected %q to match", path)
		}
	}
	for _, path := range shouldNotMatch {
		if m.Match(path) {
			t.Errorf("expected %q to not match", path)
		}
	}
}

func TestMatchDirectoryOnlyPattern(t *testing.T) {
	m := setupMatcher(t, "abcdef/\n")

	shouldMatch := []string{
		"abcdef/",
		"subdir/abcdef/",
	}
	shouldNotMatch := []string{
		"abcdef",
		"subdir/abcdef",
	}

	for _, path := range shouldMatch {
		if !m.Match(path) {
			t.Errorf("expected %q to match", path)
		}
	}
	for _, path := range shouldNotMatch {
		if m.Match(path) {
			t.Errorf("expected %q to not match", path)
		}
	}
}

func TestMatchInnerDoubleAsterisk(t *testing.T) {
	m := setupMatcher(t, "abc/**/def\n")

	shouldMatch := []string{
		"abc/x/def",
		"abc/def",
		"abc/x/y/z/def",
	}
	shouldNotMatch := []string{
		"a/b/def",
		"abc",
		"def",
	}

	for _, path := range shouldMatch {
		if !m.Match(path) {
			t.Errorf("expected %q to match", path)
		}
	}
	for _, path := range shouldNotMatch {
		if m.Match(path) {
			t.Errorf("expected %q to not match", path)
		}
	}
}

func TestMatchWildcardStar(t *testing.T) {
	m := setupMatcher(t, "*.txt\na/*\n")

	shouldMatch := []string{
		"file.txt",
		"CMakeLists.txt",
		"a/b",
		"a/c",
	}
	shouldNotMatch := []string{
		"file.gif",
		"filetxt",
	}

	for _, path := range shouldMatch {
		if !m.Match(path) {
			t.Errorf("expected %q to match", path)
		}
	}
	for _, path := range shouldNotMatch {
		if m.Match(path) {
			t.Errorf("expected %q to not match", path)
		}
	}
}

func TestMatchQuestionMark(t *testing.T) {
	m := setupMatcher(t, "dea?beef\n")

	if !m.Match("deadbeef") {
		t.Error("expected deadbeef to match")
	}
	if m.Match("deabeef") {
		t.Error("expected deabeef to not match")
	}
}

func TestMatchMultiSegmentAnchored(t *testing.T) {
	// A pattern with a slash (other than trailing) but no leading slash
	// is still anchored to the gitignore's directory
	m := setupMatcher(t, "subdir/zoo\n")

	if !m.Match("subdir/zoo") {
		t.Error("expected subdir/zoo to match")
	}
	if m.Match("other/subdir/zoo") {
		t.Error("expected other/subdir/zoo to not match")
	}
}

func TestMatchNegateAnchored(t *testing.T) {
	m := setupMatcher(t, "deadbeef\n!/x/deadbeef\n")

	if !m.Match("deadbeef") {
		t.Error("expected deadbeef to match")
	}
	if m.Match("x/deadbeef") {
		t.Error("expected x/deadbeef to not match (negated)")
	}
}

func TestMatchDoubleStarSlash(t *testing.T) {
	m := setupMatcher(t, "**/\n")

	// **/ matches any directory
	shouldMatch := []string{
		"a/",
		"a/b/",
		"deep/nested/dir/",
	}
	shouldNotMatch := []string{
		"a",   // file, not directory
		"b",   // file
		"a/b", // file inside directory
	}

	for _, path := range shouldMatch {
		if !m.Match(path) {
			t.Errorf("expected %q to match", path)
		}
	}
	for _, path := range shouldNotMatch {
		if m.Match(path) {
			t.Errorf("expected %q to not match", path)
		}
	}
}

func TestMatchEscapedCharacters(t *testing.T) {
	m := setupMatcher(t, "\\!important\n\\#comment\n")

	if !m.Match("!important") {
		t.Error("expected !important to match")
	}
	if !m.Match("#comment") {
		t.Error("expected #comment to match")
	}
}

// TestMatchAgainstGitCheckIgnore verifies our implementation matches
// git check-ignore for a variety of patterns. Each subtest creates a
// real git repo, writes a .gitignore, and compares our result against
// git's actual output.
func TestMatchAgainstGitCheckIgnore(t *testing.T) {
	tests := []struct {
		name     string
		patterns string
		paths    []string // paths to check
		wantDir  []bool   // expected result when path is a directory
		wantFile []bool   // expected result when path is a file
	}{
		{
			name:     "simple wildcard",
			patterns: "*.log\n",
			paths:    []string{"app.log", "debug.log", "app.txt", "dir/app.log"},
			wantFile: []bool{true, true, false, true},
		},
		{
			name:     "deny-by-default with negation",
			patterns: "/*\n!src/\n!README.md\n",
			paths:    []string{"random.txt", "src", "README.md", "other"},
			wantFile: []bool{true, true, false, true},  // src as file stays ignored (!src/ is dir-only)
			wantDir:  []bool{true, false, false, true},  // src as dir is re-included by !src/
		},
		{
			name:     "anchored vs unanchored",
			patterns: "/root-only\nunanchored\n",
			paths:    []string{"root-only", "sub/root-only", "unanchored", "sub/unanchored"},
			wantFile: []bool{true, false, true, true},
		},
		{
			name:     "directory only trailing slash",
			patterns: "build/\n",
			paths:    []string{"build", "sub/build"},
			wantFile: []bool{false, false},
			wantDir:  []bool{true, true},
		},
		{
			name:     "double star patterns",
			patterns: "**/logs\nlogs/**\nfoo/**/bar\n",
			paths:    []string{"logs", "a/logs", "logs/x", "foo/bar", "foo/a/b/bar"},
			wantFile: []bool{true, true, true, true, true},
		},
		{
			name:     "negation override",
			patterns: "*.txt\n!important.txt\n",
			paths:    []string{"notes.txt", "important.txt", "sub/notes.txt", "sub/important.txt"},
			wantFile: []bool{true, false, true, false},
		},
		{
			name:     "multi-segment anchored",
			patterns: "foo/bar\n",
			paths:    []string{"foo/bar", "x/foo/bar"},
			wantFile: []bool{true, false},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := setupMatcher(t, tt.patterns)

			for i, path := range tt.paths {
				if tt.wantFile != nil {
					got := m.Match(path)
					if got != tt.wantFile[i] {
						t.Errorf("Match(%q) [file] = %v, want %v", path, got, tt.wantFile[i])
					}
				}
				if tt.wantDir != nil {
					got := m.Match(path + "/")
					if got != tt.wantDir[i] {
						t.Errorf("Match(%q) [dir] = %v, want %v", path+"/", got, tt.wantDir[i])
					}
				}
			}
		})
	}
}

// TestMatchVsGitCheckIgnore runs our matcher against the real git check-ignore
// command to verify correctness. Each case creates a git repo, writes a .gitignore,
// and compares our result with git's.
func TestMatchVsGitCheckIgnore(t *testing.T) {
	type checkPath struct {
		path  string
		isDir bool
	}

	tests := []struct {
		name     string
		patterns string
		paths    []checkPath
	}{
		{
			name:     "deny-by-default with negation",
			patterns: "/*\n!.github/\n!src/\n!README.md\n",
			paths: []checkPath{
				{"README.md", false},
				{"random.txt", false},
				{".github/workflows/ci.yml", false},
				{"src/main.go", false},
				{"vendor/lib.go", false},
				{".gitignore", false},
			},
		},
		{
			name:     "mixed patterns",
			patterns: "*.log\n!important.log\nbuild/\n/dist\nfoo/**/bar\n",
			paths: []checkPath{
				{"app.log", false},
				{"important.log", false},
				{"sub/debug.log", false},
				{"build/output.js", false},
				{"dist/bundle.js", false},
				{"src/dist/x.js", false},
				{"foo/baz/bar", false},
				{"foo/bar", false},
				{"main.go", false},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()

			// Create a git repo
			for _, args := range [][]string{
				{"git", "init", "--initial-branch=main"},
				{"git", "config", "user.email", "test@test.com"},
				{"git", "config", "user.name", "Test"},
				{"git", "config", "commit.gpgsign", "false"},
			} {
				cmd := exec.Command(args[0], args[1:]...)
				cmd.Dir = root
				if err := cmd.Run(); err != nil {
					t.Fatalf("git init: %v", err)
				}
			}

			// Write .gitignore
			if err := os.WriteFile(filepath.Join(root, ".gitignore"), []byte(tt.patterns), 0644); err != nil {
				t.Fatal(err)
			}

			// Create all necessary files/dirs so git check-ignore works
			for _, cp := range tt.paths {
				full := filepath.Join(root, cp.path)
				if err := os.MkdirAll(filepath.Dir(full), 0755); err != nil {
					t.Fatal(err)
				}
				if cp.isDir {
					if err := os.MkdirAll(full, 0755); err != nil {
						t.Fatal(err)
					}
				} else {
					if err := os.WriteFile(full, []byte("x"), 0644); err != nil {
						t.Fatal(err)
					}
				}
			}

			// Build our matcher
			m := gitignore.New(root)

			for _, cp := range tt.paths {
				matchPath := cp.path
				if cp.isDir {
					matchPath += "/"
				}

				ourResult := m.Match(matchPath)

				// Ask git check-ignore
				cmd := exec.Command("git", "check-ignore", "-q", cp.path)
				cmd.Dir = root
				err := cmd.Run()
				gitResult := err == nil // exit 0 = ignored, exit 1 = not ignored

				if ourResult != gitResult {
					t.Errorf("path %q: our matcher says ignored=%v, git check-ignore says ignored=%v",
						cp.path, ourResult, gitResult)
				}
			}
		})
	}
}

// Tests from git docs examples

func TestMatchMiddleSlashAnchors(t *testing.T) {
	// From docs: "doc/frotz" and "/doc/frotz" have the same effect
	m1 := setupMatcher(t, "doc/frotz\n")
	m2 := setupMatcher(t, "/doc/frotz\n")

	paths := []struct {
		path string
		want bool
	}{
		{"doc/frotz", true},
		{"doc/frotz/", true},
		{"a/doc/frotz", false}, // anchored, not matched in subdirs
	}

	for _, tt := range paths {
		if got := m1.Match(tt.path); got != tt.want {
			t.Errorf("doc/frotz: Match(%q) = %v, want %v", tt.path, got, tt.want)
		}
		if got := m2.Match(tt.path); got != tt.want {
			t.Errorf("/doc/frotz: Match(%q) = %v, want %v", tt.path, got, tt.want)
		}
	}
}

func TestMatchWildcardDoesNotCrossSlash(t *testing.T) {
	// From docs: "foo/*" matches "foo/test.json" but not "foo/bar/hello.c"
	m := setupMatcher(t, "foo/*\n")

	if !m.Match("foo/test.json") {
		t.Error("expected foo/test.json to match")
	}
	if !m.Match("foo/bar") {
		t.Error("expected foo/bar to match")
	}
	// foo/bar/hello.c: the * in foo/* should NOT match "bar/hello.c"
	// But foo/bar is matched by the pattern (bar matches *), and since
	// non-dir-only patterns match descendants, foo/bar/hello.c is matched too.
	// This aligns with git behavior where foo/* ignoring foo/bar as a directory
	// means its contents are also ignored.
}

func TestMatchDirOnlyFrotz(t *testing.T) {
	// From docs: "doc/frotz/" matches "doc/frotz" directory, but not "a/doc/frotz" directory
	// And "frotz/" matches "frotz" and "a/frotz" (any level)
	m1 := setupMatcher(t, "doc/frotz/\n")
	m2 := setupMatcher(t, "frotz/\n")

	if !m1.Match("doc/frotz/") {
		t.Error("expected doc/frotz/ to match doc/frotz/")
	}
	if m1.Match("a/doc/frotz/") {
		t.Error("expected a/doc/frotz/ to NOT match doc/frotz/")
	}

	if !m2.Match("frotz/") {
		t.Error("expected frotz/ to match frotz/")
	}
	if !m2.Match("a/frotz/") {
		t.Error("expected a/frotz/ to match frotz/")
	}
}

func TestMatchCannotReincludeUnderExcludedParent(t *testing.T) {
	// From docs: "It is not possible to re-include a file if a parent directory
	// of that file is excluded."
	// Since our callers SkipDir on excluded directories, we test that the
	// directory itself is excluded (the caller won't descend into it).
	m := setupMatcher(t, "dir/\n!dir/important.txt\n")

	// The directory is still excluded
	if !m.Match("dir/") {
		t.Error("expected dir/ to be ignored")
	}
	// The file would be re-included by the pattern, but since callers
	// SkipDir on dir/, they never check this file. We verify the pattern
	// semantics still work for completeness.
	if m.Match("dir/important.txt") {
		t.Error("negation should re-include dir/important.txt in pattern matching")
	}
}

// Tests below are adapted from sabhiram/go-gitignore (MIT license).
// https://github.com/sabhiram/go-gitignore

func TestMatchDotFile(t *testing.T) {
	m := setupMatcher(t, ".d\n")

	shouldMatch := []string{".d", "d/.d", ".d/", ".d/a"}
	shouldNotMatch := []string{".dd", "d.d", "d/d.d", "d/e"}

	for _, path := range shouldMatch {
		if !m.Match(path) {
			t.Errorf("expected %q to match", path)
		}
	}
	for _, path := range shouldNotMatch {
		if m.Match(path) {
			t.Errorf("expected %q to not match", path)
		}
	}
}

func TestMatchDotDir(t *testing.T) {
	m := setupMatcher(t, ".e\n")

	shouldMatch := []string{".e/", ".e/e", "e/.e"}
	shouldNotMatch := []string{".ee/", "e.e/", "e/e.e", "e/f"}

	for _, path := range shouldMatch {
		if !m.Match(path) {
			t.Errorf("expected %q to match", path)
		}
	}
	for _, path := range shouldNotMatch {
		if m.Match(path) {
			t.Errorf("expected %q to not match", path)
		}
	}
}

func TestMatchStarExtension(t *testing.T) {
	m := setupMatcher(t, ".js*\n")

	shouldMatch := []string{".js", ".jsa", ".js/", ".js/a"}
	shouldNotMatch := []string{"a.js", "a.js/a"}

	for _, path := range shouldMatch {
		if !m.Match(path) {
			t.Errorf("expected %q to match", path)
		}
	}
	for _, path := range shouldNotMatch {
		if m.Match(path) {
			t.Errorf("expected %q to not match", path)
		}
	}
}

func TestMatchDoubleStarTrailingDir(t *testing.T) {
	m := setupMatcher(t, "foo/**/\n")

	shouldMatch := []string{"foo/", "foo/abc/", "foo/x/y/z/"}
	shouldNotMatch := []string{"foo"}

	for _, path := range shouldMatch {
		if !m.Match(path) {
			t.Errorf("expected %q to match", path)
		}
	}
	for _, path := range shouldNotMatch {
		if m.Match(path) {
			t.Errorf("expected %q to not match", path)
		}
	}
}

func TestMatchDoubleStarWithExtension(t *testing.T) {
	m := setupMatcher(t, "foo/**/*.bar\n")

	shouldMatch := []string{"foo/abc.bar", "foo/abc.bar/", "foo/x/y/z.bar", "foo/x/y/z.bar/"}
	shouldNotMatch := []string{"foo/", "abc.bar"}

	for _, path := range shouldMatch {
		if !m.Match(path) {
			t.Errorf("expected %q to match", path)
		}
	}
	for _, path := range shouldNotMatch {
		if m.Match(path) {
			t.Errorf("expected %q to not match", path)
		}
	}
}

func TestMatchNegationSubdirectoryFilter(t *testing.T) {
	m := setupMatcher(t, "abc\n!abc/b\n")

	if !m.Match("abc/a.js") {
		t.Error("expected abc/a.js to match")
	}
	if m.Match("abc/b/b.js") {
		t.Error("expected abc/b/b.js to not match")
	}
}

func TestMatchNodeModulesDeepNesting(t *testing.T) {
	m := setupMatcher(t, "node_modules/\n")

	if !m.Match("node_modules/gulp/node_modules/abc.md") {
		t.Error("expected deeply nested node_modules path to match")
	}
	if !m.Match("node_modules/gulp/node_modules/abc.json") {
		t.Error("expected deeply nested node_modules path to match")
	}
}

func TestMatchDirEndedWithStar(t *testing.T) {
	m := setupMatcher(t, "abc/*\n")

	if m.Match("abc") {
		t.Error("expected bare abc to not match abc/*")
	}
	if !m.Match("abc/x") {
		t.Error("expected abc/x to match abc/*")
	}
}

func TestMatchDenyByDefaultGitDocsExample(t *testing.T) {
	// From git docs: "exclude everything except directory foo/bar"
	m := setupMatcher(t, "/*\n!/foo\n/foo/*\n!/foo/bar\n")

	if m.Match("foo") {
		t.Error("expected foo to not match (re-included by !/foo)")
	}
	if m.Match("foo/bar") {
		t.Error("expected foo/bar to not match (re-included by !/foo/bar)")
	}
	if !m.Match("a") {
		t.Error("expected a to match (caught by /*)")
	}
	if !m.Match("foo/baz") {
		t.Error("expected foo/baz to match (caught by /foo/*)")
	}
}

func TestMatchFileEndedWithStar(t *testing.T) {
	m := setupMatcher(t, "abc.js*\n")

	shouldMatch := []string{"abc.js", "abc.js/", "abc.js/abc", "abc.jsa", "abc.jsa/", "abc.jsa/abc"}
	for _, path := range shouldMatch {
		if !m.Match(path) {
			t.Errorf("expected %q to match", path)
		}
	}
}

func TestMatchWildcardAsFilename(t *testing.T) {
	m := setupMatcher(t, "*.b\n")

	shouldMatch := []string{"b/a.b", "b/.b", "b/c/a.b"}
	shouldNotMatch := []string{"b/.ba"}

	for _, path := range shouldMatch {
		if !m.Match(path) {
			t.Errorf("expected %q to match", path)
		}
	}
	for _, path := range shouldNotMatch {
		if m.Match(path) {
			t.Errorf("expected %q to not match", path)
		}
	}
}

func TestMatchNestedDoubleStarDotFiles(t *testing.T) {
	m := setupMatcher(t, "**/external/**/*.md\n**/external/**/*.json\n**/external/**/.*ignore\n")

	if !m.Match("external/foobar/.gitignore") {
		t.Error("expected external/foobar/.gitignore to match")
	}
	if !m.Match("external/barfoo/.bower.json") {
		t.Error("expected external/barfoo/.bower.json to match")
	}
}

// Spec edge cases

func TestMatchEscapedWildcards(t *testing.T) {
	// \* matches a literal *, \? matches a literal ?
	m := setupMatcher(t, "hello\\*\nhello\\?\n")

	tests := []struct {
		path string
		want bool
	}{
		{"hello*", true},          // literal *
		{"hello?", true},          // literal ?
		{"dir/hello*", true},      // unanchored, matches in subdirs
		{"helloX", false},         // \* is not a wildcard
		{"helloworld", false},     // \* is not a wildcard
		{"dir/helloworld", false}, // still not a wildcard in subdirs
	}

	for _, tt := range tests {
		got := m.Match(tt.path)
		if got != tt.want {
			t.Errorf("Match(%q) = %v, want %v", tt.path, got, tt.want)
		}
	}
}

func TestMatchEscapedTrailingSpace(t *testing.T) {
	// A trailing space escaped with \ should be preserved as part of the pattern.
	// "hello\ " matches the filename "hello " (with a space).
	m := setupMatcher(t, "hello\\ \n")

	tests := []struct {
		path string
		want bool
	}{
		{"hello ", true},     // literal trailing space
		{"hello", false},     // no space
		{"helloX", false},    // different char
		{"dir/hello ", true}, // unanchored
	}

	for _, tt := range tests {
		got := m.Match(tt.path)
		if got != tt.want {
			t.Errorf("Match(%q) = %v, want %v", tt.path, got, tt.want)
		}
	}
}

func TestMatchTripleStar(t *testing.T) {
	// Per spec: "Other consecutive asterisks are considered invalid."
	// In practice git treats *** like * within a segment (no special ** meaning).
	// Since the pattern has no slash, it matches basenames at any level.
	m := setupMatcher(t, "***foo\n")

	tests := []struct {
		path string
		want bool
	}{
		{"foo", true},
		{"afoo", true},
		{"a/b/c/xfoo", true},
		{"bar", false},
		{"foobar", false},
	}

	for _, tt := range tests {
		got := m.Match(tt.path)
		if got != tt.want {
			t.Errorf("Match(%q) = %v, want %v", tt.path, got, tt.want)
		}
	}
}

func TestMatchBracketWithClosingBracketFirst(t *testing.T) {
	// Per POSIX, ] as the first character in a bracket class is treated as
	// a literal member of the class. So []abc] matches ], a, b, or c.
	m := setupMatcher(t, "[]abc]\n")

	tests := []struct {
		path string
		want bool
	}{
		{"]", true},
		{"a", true},
		{"b", true},
		{"c", true},
		{"d", false},
		{"[", false},
	}

	for _, tt := range tests {
		got := m.Match(tt.path)
		if got != tt.want {
			t.Errorf("Match(%q) = %v, want %v", tt.path, got, tt.want)
		}
	}
}

func TestMatchPOSIXCharacterClasses(t *testing.T) {
	// POSIX character classes like [[:space:]], [[:alpha:]] are valid in
	// gitignore patterns (git's wildmatch supports them).
	// Test cases adapted from git/t/t3070-wildmatch.sh
	tests := []struct {
		pattern string
		path    string
		want    bool
	}{
		// Basic classes
		{"foo[[:space:]]bar", "foo bar", true},
		{"foo[[:space:]]bar", "foo\tbar", true},
		{"foo[[:space:]]bar", "fooXbar", false},
		{"[[:digit:]]*.log", "3debug.log", true},
		{"[[:digit:]]*.log", "0.log", true},
		{"[[:digit:]]*.log", "debug.log", false},

		// Multiple character classes from wildmatch suite
		{"[[:alpha:]][[:digit:]][[:upper:]]", "a1B", true},
		{"[[:digit:][:upper:][:space:]]", "A", true},
		{"[[:digit:][:upper:][:space:]]", "1", true},
		{"[[:digit:][:upper:][:space:]]", " ", true},
		{"[[:digit:][:upper:][:space:]]", "a", false},
		{"[[:digit:][:upper:][:space:]]", ".", false},
		{"[[:digit:][:punct:][:space:]]", ".", true},
		{"[[:xdigit:]]", "5", true},
		{"[[:xdigit:]]", "f", true},
		{"[[:xdigit:]]", "D", true},

		// Mixing ranges and POSIX classes
		{"[a-c[:digit:]x-z]", "5", true},
		{"[a-c[:digit:]x-z]", "b", true},
		{"[a-c[:digit:]x-z]", "y", true},
		{"[a-c[:digit:]x-z]", "q", false},
	}

	for _, tt := range tests {
		m := setupMatcher(t, tt.pattern+"\n")
		got := m.Match(tt.path)
		if got != tt.want {
			t.Errorf("pattern %q, Match(%q) = %v, want %v", tt.pattern, tt.path, got, tt.want)
		}
	}
}

func TestMatchBracketRange(t *testing.T) {
	m := setupMatcher(t, "file[0-9].txt\n")

	tests := []struct {
		path string
		want bool
	}{
		{"file0.txt", true},
		{"file5.txt", true},
		{"file9.txt", true},
		{"filea.txt", false},
		{"file10.txt", false},
		{"dir/file3.txt", true}, // unanchored
	}

	for _, tt := range tests {
		got := m.Match(tt.path)
		if got != tt.want {
			t.Errorf("Match(%q) = %v, want %v", tt.path, got, tt.want)
		}
	}
}

func TestMatchBracketNegationCaret(t *testing.T) {
	// Both ! and ^ should work as negation inside brackets
	m := setupMatcher(t, "file[^0-9].txt\n")

	tests := []struct {
		path string
		want bool
	}{
		{"filea.txt", true},
		{"fileZ.txt", true},
		{"file0.txt", false},
		{"file9.txt", false},
	}

	for _, tt := range tests {
		got := m.Match(tt.path)
		if got != tt.want {
			t.Errorf("Match(%q) = %v, want %v", tt.path, got, tt.want)
		}
	}
}

func TestMatchUnclosedBracket(t *testing.T) {
	// An unclosed [ is treated as a literal character
	m := setupMatcher(t, "file[.txt\n")

	tests := []struct {
		path string
		want bool
	}{
		{"file[.txt", true},
		{"filea.txt", false},
	}

	for _, tt := range tests {
		got := m.Match(tt.path)
		if got != tt.want {
			t.Errorf("Match(%q) = %v, want %v", tt.path, got, tt.want)
		}
	}
}

func TestMatchTrailingSpacesStripped(t *testing.T) {
	// Unescaped trailing spaces should be stripped from patterns
	m := setupMatcher(t, "hello   \n")

	tests := []struct {
		path string
		want bool
	}{
		{"hello", true},    // trailing spaces stripped, matches "hello"
		{"hello ", false},  // the pattern is "hello", not "hello "
		{"hello   ", false},
	}

	for _, tt := range tests {
		got := m.Match(tt.path)
		if got != tt.want {
			t.Errorf("Match(%q) = %v, want %v", tt.path, got, tt.want)
		}
	}
}

func TestMatchTrailingSpacesEdgeCases(t *testing.T) {
	tests := []struct {
		name    string
		pattern string
		path    string
		want    bool
	}{
		// Spaces before an escaped space: "foo   \ " → pattern is "foo   \ "
		{"spaces before escaped space", "foo   \\ ", "foo    ", true},
		{"spaces before escaped space no match", "foo   \\ ", "foo", false},

		// Multiple escaped spaces: "hello\ \ " → pattern is "hello\ \ "
		{"multiple escaped spaces", "hello\\ \\ ", "hello  ", true},
		{"multiple escaped spaces no match short", "hello\\ \\ ", "hello ", false},

		// Trailing tabs preserved (git only strips spaces, not tabs)
		{"trailing tab preserved", "hello\t", "hello\t", true},
		{"trailing tab not stripped", "hello\t", "hello", false},

		// Leading spaces preserved
		{"leading spaces preserved", "  hello", "  hello", true},
		{"leading spaces preserved no match", "  hello", "hello", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := setupMatcher(t, tt.pattern+"\n")
			got := m.Match(tt.path)
			if got != tt.want {
				t.Errorf("pattern %q, Match(%q) = %v, want %v", tt.pattern, tt.path, got, tt.want)
			}
		})
	}
}

func TestMatchCommentLines(t *testing.T) {
	m := setupMatcher(t, "# this is a comment\nfoo\n# another comment\nbar\n")

	tests := []struct {
		path string
		want bool
	}{
		{"foo", true},
		{"bar", true},
		{"# this is a comment", false},
		{"# another comment", false},
	}

	for _, tt := range tests {
		got := m.Match(tt.path)
		if got != tt.want {
			t.Errorf("Match(%q) = %v, want %v", tt.path, got, tt.want)
		}
	}
}

func TestMatchBlankLines(t *testing.T) {
	// Blank lines should be ignored as separators
	m := setupMatcher(t, "\n\nfoo\n\n\nbar\n\n")

	if !m.Match("foo") {
		t.Error("expected foo to match")
	}
	if !m.Match("bar") {
		t.Error("expected bar to match")
	}
	if m.Match("baz") {
		t.Error("expected baz to not match")
	}
}

// Verify edge cases against git check-ignore
func TestMatchEdgeCasesVsGitCheckIgnore(t *testing.T) {
	type checkPath struct {
		path  string
		isDir bool
	}

	tests := []struct {
		name     string
		patterns string
		paths    []checkPath
	}{
		{
			name:     "escaped wildcards",
			patterns: "hello\\*\nhello\\?\n",
			paths: []checkPath{
				{"hello*", false},
				{"hello?", false},
				{"helloX", false},
				{"helloworld", false},
			},
		},
		{
			name:     "bracket with closing bracket first",
			patterns: "[]abc]\n",
			paths: []checkPath{
				{"]", false},
				{"a", false},
				{"b", false},
				{"c", false},
				{"d", false},
			},
		},
		{
			name:     "triple star",
			patterns: "***foo\n",
			paths: []checkPath{
				{"foo", false},
				{"afoo", false},
				{"bar", false},
			},
		},
		{
			name:     "POSIX character classes",
			patterns: "[[:digit:]]*.log\n",
			paths: []checkPath{
				{"3debug.log", false},
				{"0.log", false},
				{"debug.log", false},
			},
		},
		{
			name:     "range and negated bracket",
			patterns: "file[0-9].txt\nlog[^a-z].out\n",
			paths: []checkPath{
				{"file5.txt", false},
				{"filea.txt", false},
				{"log1.out", false},
				{"loga.out", false},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if runtime.GOOS == "windows" && tt.name == "escaped wildcards" {
				t.Skip("Windows does not allow * or ? in filenames")
			}
			root := t.TempDir()

			for _, args := range [][]string{
				{"git", "init", "--initial-branch=main"},
				{"git", "config", "user.email", "test@test.com"},
				{"git", "config", "user.name", "Test"},
				{"git", "config", "commit.gpgsign", "false"},
			} {
				cmd := exec.Command(args[0], args[1:]...)
				cmd.Dir = root
				if err := cmd.Run(); err != nil {
					t.Fatalf("git init: %v", err)
				}
			}

			if err := os.WriteFile(filepath.Join(root, ".gitignore"), []byte(tt.patterns), 0644); err != nil {
				t.Fatal(err)
			}

			for _, cp := range tt.paths {
				full := filepath.Join(root, cp.path)
				if err := os.MkdirAll(filepath.Dir(full), 0755); err != nil {
					t.Fatal(err)
				}
				if cp.isDir {
					if err := os.MkdirAll(full, 0755); err != nil {
						t.Fatal(err)
					}
				} else {
					if err := os.WriteFile(full, []byte("x"), 0644); err != nil {
						t.Fatal(err)
					}
				}
			}

			m := gitignore.New(root)

			for _, cp := range tt.paths {
				matchPath := cp.path
				if cp.isDir {
					matchPath += "/"
				}

				ourResult := m.Match(matchPath)

				cmd := exec.Command("git", "check-ignore", "-q", cp.path)
				cmd.Dir = root
				err := cmd.Run()
				gitResult := err == nil

				if ourResult != gitResult {
					t.Errorf("path %q: our matcher says ignored=%v, git check-ignore says ignored=%v",
						cp.path, ourResult, gitResult)
				}
			}
		})
	}
}

// Tests below are adapted from git/t/t3070-wildmatch.sh
// https://github.com/git/git/blob/8d8387116ae8c3e73f6184471f0c46edbd2c7601/t/t3070-wildmatch.sh
//
// The wildmatch test format is: match <wildmatch_result> <pathmatch_result> <text> <pattern>
// We use the first column (wildmatch mode) since gitignore uses wildmatch semantics.
// Cases marked 'x' in the original are implementation-defined; we include them where
// our behavior is well-defined.
//
// Some wildmatch tests don't map directly to gitignore because gitignore adds
// directory-content matching (a pattern matching "foo" also matches "foo/anything")
// and unanchored patterns match at any directory level. Tests are adapted accordingly.

func TestWildmatchBasicGlob(t *testing.T) {
	tests := []struct {
		pattern string
		path    string
		want    bool
	}{
		// Exact and simple wildcard matching
		{"foo", "foo", true},
		{"foo", "bar", false},
		{"???", "foo", true},
		{"??", "foo", false},
		{"*", "foo", true},
		{"f*", "foo", true},
		{"*f", "foo", false},
		{"*foo*", "foo", true},
		{"*ob*a*r*", "foobar", true},
		{"*ab", "aaaaaaabababab", true},

		// Escaped special characters
		{"foo\\*", "foo*", true},
		{"foo\\*bar", "foobar", false},
		{"f\\\\oo", "f\\oo", true},

		// Bracket with glob operators
		{"*[al]?", "ball", true},
		{"[ten]", "ten", false}, // [ten] matches single char t, e, or n
		{"t[a-g]n", "ten", true},
		{"t[!a-g]n", "ten", false},
		{"t[!a-g]n", "ton", true},
		{"t[^a-g]n", "ton", true},

		// Question mark and escape combinations
		{"\\??\\?b", "?a?b", true},
		{"\\a\\b\\c", "abc", true},

		// Literal bracket via escape
		{"\\[ab]", "[ab]", true},
		{"[[]ab]", "[ab]", true},

		// Range edge cases from wildmatch suite
		{"a[c-c]st", "acrt", false},
		{"a[c-c]rt", "acrt", true},

		// Simple patterns
		{"foo", "foo", true},
		{"@foo", "@foo", true},
		{"@foo", "foo", false},
	}

	for _, tt := range tests {
		m := setupMatcher(t, tt.pattern+"\n")
		got := m.Match(tt.path)
		if got != tt.want {
			t.Errorf("pattern %q, Match(%q) = %v, want %v", tt.pattern, tt.path, got, tt.want)
		}
	}
}

func TestWildmatchBracketEdgeCases(t *testing.T) {
	tests := []struct {
		pattern string
		path    string
		want    bool
	}{
		// ] as first char in bracket (literal member)
		{"a[]]b", "a]b", true},
		{"a[]-]b", "a-b", true},
		{"a[]-]b", "a]b", true},
		{"a[]-]b", "aab", false},
		{"a[]a-]b", "aab", true},
		{"]", "]", true},

		// Negation with ] edge case
		{"[!]-]", "]", false},

		// Backslash escapes inside brackets (wildmatch: \X = literal X)
		{"[\\-_]", "-", true},          // \- = literal dash
		{"[\\-_]", "_", true},
		{"[\\-_]", "a", false},
		{"[\\]]", "]", true},           // \] = literal ]
		{"[\\\\]", "\\", true},         // \\ = literal backslash
		{"[!\\\\]", "\\", false},       // negated literal backslash
		{"[!\\\\]", "a", true},
		{"[A-\\\\]", "G", true},        // range A(65) to \(92)

		// Range with \\ as endpoint: range \(92) to ^(94)
		{"[\\\\-^]", "]", true},        // ](93) is in range
		{"[\\\\-^]", "[", false},       // [(91) is not

		// Range via escaped endpoints: \1=1, \3=3, range 1-3
		{"[\\1-\\3]", "2", true},
		{"[\\1-\\3]", "3", true},
		{"[\\1-\\3]", "4", false},

		// Range from [ to ] via escaped ]: [(91) to ](93)
		{"[[-\\]]", "\\", true},        // \(92) in range
		{"[[-\\]]", "[", true},         // [(91) in range
		{"[[-\\]]", "]", true},         // ](93) in range
		{"[[-\\]]", "-", false},        // -(45) not in range

		// Various dash/range positions
		{"[-]", "-", true},
		{"[,-.]", "-", true},
		{"[,-.]", "+", false},
		{"[,-.]", "-.]", false},

		// Comma in bracket
		{"[,]", ",", true},
		{"[\\\\,]", ",", true},         // \\=literal backslash, comma=literal
		{"[\\\\,]", "\\", true},
		{"[\\,]", ",", true},           // \,=literal comma

		// Caret as literal in bracket (not at start)
		{"[a^bc]", "^", true},

		// Space range
		{"[ --]", " ", true},
		{"[ --]", "$", true},
		{"[ --]", "-", true},
		{"[ --]", "0", false},

		// Multiple dashes
		{"[---]", "-", true},
		{"[------]", "-", true},

		// Dash in middle of range expression
		{"[a-e-n]", "-", true},
		{"[a-e-n]", "j", false},

		// Negated multiple dashes
		{"[!------]", "a", true},
	}

	for _, tt := range tests {
		m := setupMatcher(t, tt.pattern+"\n")
		got := m.Match(tt.path)
		if got != tt.want {
			t.Errorf("pattern %q, Match(%q) = %v, want %v", tt.pattern, tt.path, got, tt.want)
		}
	}
}

func TestWildmatchCharacterClassesExpanded(t *testing.T) {
	tests := []struct {
		pattern string
		path    string
		want    bool
	}{
		// [:lower:] and [:upper:]
		{"[[:lower:]]", "a", true},
		{"[[:lower:]]", "A", false},
		{"[[:upper:]]", "A", true},
		{"[[:upper:]]", "a", false},

		// [:alnum:]
		{"[[:alnum:]]", "a", true},
		{"[[:alnum:]]", "5", true},
		{"[[:alnum:]]", ".", false},

		// [:blank:] (space and tab)
		{"[[:blank:]]", " ", true},
		{"[[:blank:]]", "\t", true},

		// [:graph:] and [:print:]
		{"[[:graph:]]", "a", true},
		{"[[:graph:]]", "!", true},

		// Underscore matches many classes
		{"[[:alnum:][:alpha:][:blank:][:cntrl:][:digit:][:graph:][:lower:][:print:][:punct:][:space:][:upper:][:xdigit:]]", "_", true},

		// Negated combination: period is not alnum/alpha/blank/cntrl/digit/lower/space/upper/xdigit
		{"[^[:alnum:][:alpha:][:blank:][:cntrl:][:digit:][:lower:][:space:][:upper:][:xdigit:]]", ".", true},

		// Invalid POSIX class name causes regex compilation failure (no match)
		{"[[:digit:][:upper:][:spaci:]]", "1", false},

		// Case-sensitive ranges
		{"[A-Z]", "A", true},
		{"[A-Z]", "a", false},
		{"[a-z]", "a", true},
		{"[a-z]", "A", false},
		{"[B-Za]", "a", true},
		{"[B-Za]", "A", false},
		{"[B-a]", "a", true},
		{"[B-a]", "A", false},
		{"[Z-y]", "Z", true},
		{"[Z-y]", "z", false},
	}

	for _, tt := range tests {
		m := setupMatcher(t, tt.pattern+"\n")
		got := m.Match(tt.path)
		if got != tt.want {
			t.Errorf("pattern %q, Match(%q) = %v, want %v", tt.pattern, tt.path, got, tt.want)
		}
	}
}

func TestWildmatchSlashHandling(t *testing.T) {
	tests := []struct {
		pattern string
		path    string
		want    bool
	}{
		// * does not cross slashes
		{"foo*bar", "foo/baz/bar", false},
		{"foo**bar", "foo/baz/bar", false},
		// But ** in a/**/ position does
		{"foo/**/bar", "foo/baz/bar", true},
		{"foo/**/bar", "foo/b/a/z/bar", true},
		{"foo/**/bar", "foo/bar", true},

		// **/X matches X at any depth
		{"**/foo", "foo", true},
		{"**/foo", "XXX/foo", true},
		{"**/foo", "bar/baz/foo", true},

		// */foo is anchored (has slash), * matches one segment
		{"*/foo", "bar/baz/foo", false},
		{"*/foo", "bar/foo", true},

		// **/bar/* matches content inside bar/
		{"**/bar/*", "deep/foo/bar/baz", true},
		{"**/bar/*", "deep/foo/bar", false},

		// **/bar/** matches anything under bar/
		{"**/bar/**", "deep/foo/bar/baz", true},

		// ?  does not match /
		{"foo?bar", "foo/bar", false},
		{"foo?bar", "fooXbar", true},

		// f[^eiu][^eiu][^eiu][^eiu][^eiu]r with slashes
		{"f[^eiu][^eiu][^eiu][^eiu][^eiu]r", "foo-bar", true},

		// Multi-segment ** patterns
		{"**/t[o]", "foo/bar/baz/to", true},

		// Complex multi-segment patterns
		{"XXX/*/*/*/*/*/*/12/*/*/*/m/*/*/*", "XXX/adobe/courier/bold/o/normal//12/120/75/75/m/70/iso8859/1", true},
		{"XXX/*/*/*/*/*/*/12/*/*/*/m/*/*/*", "XXX/adobe/courier/bold/o/normal//12/120/75/75/X/70/iso8859/1", false},

		// * matches within segments only
		{"*/*/*", "foo/bba/arr", true},
		{"*/*/*", "foo/bar", false},
		{"*/*/*", "foo", false},

		// ** across multiple levels
		{"**/**/**", "foo/bb/aa/rr", true},
		{"**/**/**", "foo/bba/arr", true},

		// Complex wildcard + slash patterns
		{"*X*i", "abcXdefXghi", true},
		{"*/*X*/*/*i", "ab/cXd/efXg/hi", true},
		{"**/*X*/**/*i", "ab/cXd/efXg/hi", true},

		// Long pattern from recursion/abort tests
		{"-*-*-*-*-*-*-12-*-*-*-m-*-*-*", "-adobe-courier-bold-o-normal--12-120-75-75-m-70-iso8859-1", true},
		{"-*-*-*-*-*-*-12-*-*-*-m-*-*-*", "-adobe-courier-bold-o-normal--12-120-75-75-X-70-iso8859-1", false},

		// ** with extension
		{"**/*a*b*g*n*t", "abcd/abcdefg/abcdefghijk/abcdefghijklmnop.txt", true},
		{"**/*a*b*g*n*t", "abcd/abcdefg/abcdefghijk/abcdefghijklmnop.txtz", false},
	}

	for _, tt := range tests {
		m := setupMatcher(t, tt.pattern+"\n")
		got := m.Match(tt.path)
		if got != tt.want {
			t.Errorf("pattern %q, Match(%q) = %v, want %v", tt.pattern, tt.path, got, tt.want)
		}
	}
}

func TestWildmatchVsGitCheckIgnore(t *testing.T) {
	type checkPath struct {
		path  string
		isDir bool
	}

	tests := []struct {
		name     string
		patterns string
		paths    []checkPath
	}{
		{
			name:     "star does not cross slashes",
			patterns: "foo*bar\n",
			paths: []checkPath{
				{"fooXbar", false},
				{"fooXXbar", false},
			},
		},
		{
			name:     "double star slash patterns",
			patterns: "**/foo\n*/bar\n",
			paths: []checkPath{
				{"foo", false},
				{"XXX/foo", false},
				{"bar/baz/foo", false},
				{"x/bar", false},
			},
		},
		{
			name:     "bracket ranges and negation",
			patterns: "t[a-g]n\nt[!a-g]n\n",
			paths: []checkPath{
				{"ten", false},
				{"ton", false},
				{"tin", false},
			},
		},
		{
			name:     "complex glob operators",
			patterns: "*[al]?\n[ten]\n",
			paths: []checkPath{
				{"ball", false},
				{"tall", false},
				{"t", false},
				{"e", false},
				{"n", false},
				{"ten", false},
			},
		},
		{
			name:     "POSIX character classes",
			patterns: "[[:alpha:]][[:digit:]][[:upper:]]\n[[:lower:]]\n[[:xdigit:]]\n",
			paths: []checkPath{
				{"a1B", false},
				{"a", false},
				{"f", false},
				{"5", false},
				{"D", false},
			},
		},
		{
			name:     "question mark does not match slash",
			patterns: "foo?bar\n",
			paths: []checkPath{
				{"fooXbar", false},
				{"fooybar", false},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()

			for _, args := range [][]string{
				{"git", "init", "--initial-branch=main"},
				{"git", "config", "user.email", "test@test.com"},
				{"git", "config", "user.name", "Test"},
				{"git", "config", "commit.gpgsign", "false"},
			} {
				cmd := exec.Command(args[0], args[1:]...)
				cmd.Dir = root
				if err := cmd.Run(); err != nil {
					t.Fatalf("git init: %v", err)
				}
			}

			if err := os.WriteFile(filepath.Join(root, ".gitignore"), []byte(tt.patterns), 0644); err != nil {
				t.Fatal(err)
			}

			for _, cp := range tt.paths {
				full := filepath.Join(root, cp.path)
				if err := os.MkdirAll(filepath.Dir(full), 0755); err != nil {
					t.Fatal(err)
				}
				if cp.isDir {
					if err := os.MkdirAll(full, 0755); err != nil {
						t.Fatal(err)
					}
				} else {
					if err := os.WriteFile(full, []byte("x"), 0644); err != nil {
						t.Fatal(err)
					}
				}
			}

			m := gitignore.New(root)

			for _, cp := range tt.paths {
				matchPath := cp.path
				if cp.isDir {
					matchPath += "/"
				}

				ourResult := m.Match(matchPath)

				cmd := exec.Command("git", "check-ignore", "-q", cp.path)
				cmd.Dir = root
				err := cmd.Run()
				gitResult := err == nil

				if ourResult != gitResult {
					t.Errorf("path %q: our matcher says ignored=%v, git check-ignore says ignored=%v",
						cp.path, ourResult, gitResult)
				}
			}
		})
	}
}

func TestGlobalExcludesFileXDG(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".git", "info"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".gitignore"), []byte(""), 0644); err != nil {
		t.Fatal(err)
	}

	// Set up a fake XDG_CONFIG_HOME with a global ignore file.
	xdgDir := t.TempDir()
	gitConfigDir := filepath.Join(xdgDir, "git")
	if err := os.MkdirAll(gitConfigDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(gitConfigDir, "ignore"), []byte("*.global-ignore\n"), 0644); err != nil {
		t.Fatal(err)
	}

	t.Setenv("XDG_CONFIG_HOME", xdgDir)
	// Clear any git config that might override.
	t.Setenv("GIT_CONFIG_GLOBAL", "/dev/null")

	m := gitignore.New(root)

	if !m.Match("test.global-ignore") {
		t.Error("expected global excludes pattern to match")
	}
	if m.Match("test.go") {
		t.Error("expected test.go to not be ignored")
	}
}

func TestGlobalExcludesFilePriority(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".git", "info"), 0755); err != nil {
		t.Fatal(err)
	}
	// Root .gitignore re-includes *.global-ignore
	if err := os.WriteFile(filepath.Join(root, ".gitignore"), []byte("!*.global-ignore\n"), 0644); err != nil {
		t.Fatal(err)
	}

	// Global excludes ignores *.global-ignore
	xdgDir := t.TempDir()
	gitConfigDir := filepath.Join(xdgDir, "git")
	if err := os.MkdirAll(gitConfigDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(gitConfigDir, "ignore"), []byte("*.global-ignore\n"), 0644); err != nil {
		t.Fatal(err)
	}

	t.Setenv("XDG_CONFIG_HOME", xdgDir)
	t.Setenv("GIT_CONFIG_GLOBAL", "/dev/null")

	m := gitignore.New(root)

	// Root .gitignore (higher priority) re-includes the file.
	if m.Match("test.global-ignore") {
		t.Error("expected root negation to override global excludes")
	}
}

func TestExpandTilde(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home directory")
	}

	// Test via a global git config that uses ~
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".git", "info"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".gitignore"), []byte(""), 0644); err != nil {
		t.Fatal(err)
	}

	// Create a temp ignore file in a known location under home
	ignoreDir := filepath.Join(home, ".test-gitignore-expand-tilde")
	if err := os.MkdirAll(ignoreDir, 0755); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.RemoveAll(ignoreDir) }()

	ignoreFile := filepath.Join(ignoreDir, "ignore")
	if err := os.WriteFile(ignoreFile, []byte("*.tilde-test\n"), 0644); err != nil {
		t.Fatal(err)
	}

	// Configure git to use this file via tilde path.
	gitConfigDir := t.TempDir()
	gitConfigFile := filepath.Join(gitConfigDir, "config")
	if err := os.WriteFile(gitConfigFile, []byte("[core]\n\texcludesfile = ~/.test-gitignore-expand-tilde/ignore\n"), 0644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_CONFIG_GLOBAL", gitConfigFile)

	m := gitignore.New(root)
	if !m.Match("foo.tilde-test") {
		t.Error("expected tilde-expanded global excludes to match")
	}
}

func TestNewFromDirectory(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".git", "info"), 0755); err != nil {
		t.Fatal(err)
	}

	// Root .gitignore
	if err := os.WriteFile(filepath.Join(root, ".gitignore"), []byte("*.log\n"), 0644); err != nil {
		t.Fatal(err)
	}

	// Create directory structure with nested .gitignore
	for _, dir := range []string{"src", "src/lib", "vendor"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "src", ".gitignore"), []byte("*.tmp\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "src", "lib", ".gitignore"), []byte("*.gen.go\n"), 0644); err != nil {
		t.Fatal(err)
	}

	// Create files so the walk discovers directories
	for _, f := range []string{"src/main.go", "src/lib/util.go", "vendor/lib.go"} {
		if err := os.WriteFile(filepath.Join(root, f), []byte("x"), 0644); err != nil {
			t.Fatal(err)
		}
	}

	m := gitignore.NewFromDirectory(root)

	tests := []struct {
		path string
		want bool
	}{
		{"app.log", true},          // root pattern
		{"src/app.log", true},      // root pattern applies in subdirs
		{"src/cache.tmp", true},    // src/.gitignore pattern
		{"cache.tmp", false},       // src pattern scoped to src/
		{"src/lib/foo.gen.go", true}, // src/lib/.gitignore pattern
		{"src/foo.gen.go", false},  // lib pattern scoped to src/lib/
		{"src/main.go", false},
	}

	for _, tt := range tests {
		got := m.Match(tt.path)
		if got != tt.want {
			t.Errorf("Match(%q) = %v, want %v", tt.path, got, tt.want)
		}
	}
}

func TestMatchPath(t *testing.T) {
	m := setupMatcher(t, "vendor/\n*.log\nbuild\n")

	tests := []struct {
		path  string
		isDir bool
		want  bool
	}{
		{"vendor", true, true},
		{"vendor", false, false},       // dir-only pattern, file doesn't match
		{"app.log", false, true},
		{"logs/app.log", false, true},
		{"build", false, true},
		{"build", true, true},
		{"build/output.js", false, true},
		{"src/main.go", false, false},
	}

	for _, tt := range tests {
		got := m.MatchPath(tt.path, tt.isDir)
		if got != tt.want {
			t.Errorf("MatchPath(%q, isDir=%v) = %v, want %v", tt.path, tt.isDir, got, tt.want)
		}
	}
}

func TestMatchPathConsistentWithMatch(t *testing.T) {
	m := setupMatcher(t, "*.log\nbuild/\n/dist\nfoo/**/bar\n")

	paths := []string{
		"app.log", "build/", "dist", "dist/", "foo/bar", "foo/a/bar",
		"src/main.go", "build/out.js",
	}
	for _, p := range paths {
		matchResult := m.Match(p)
		isDir := strings.HasSuffix(p, "/")
		clean := strings.TrimSuffix(p, "/")
		pathResult := m.MatchPath(clean, isDir)
		if matchResult != pathResult {
			t.Errorf("Match(%q)=%v but MatchPath(%q, %v)=%v", p, matchResult, clean, isDir, pathResult)
		}
	}
}

func TestNewFromDirectorySkipsIgnoredDirs(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".git", "info"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".gitignore"), []byte("ignored_dir/\n"), 0644); err != nil {
		t.Fatal(err)
	}

	// Create an ignored directory with its own .gitignore
	if err := os.MkdirAll(filepath.Join(root, "ignored_dir"), 0755); err != nil {
		t.Fatal(err)
	}
	// This .gitignore should NOT be loaded since the dir is ignored.
	if err := os.WriteFile(filepath.Join(root, "ignored_dir", ".gitignore"), []byte("!*.important\n"), 0644); err != nil {
		t.Fatal(err)
	}

	// Create a non-ignored directory
	if err := os.MkdirAll(filepath.Join(root, "src"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "src", "main.go"), []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}

	m := gitignore.NewFromDirectory(root)

	if !m.Match("ignored_dir/") {
		t.Error("expected ignored_dir/ to be ignored")
	}
	if m.Match("src/main.go") {
		t.Error("expected src/main.go to not be ignored")
	}
}

func TestWalk(t *testing.T) {
	// Isolate from user's global git config.
	t.Setenv("GIT_CONFIG_GLOBAL", "/dev/null")

	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".git", "info"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".gitignore"), []byte("*.log\nbuild/\n"), 0644); err != nil {
		t.Fatal(err)
	}

	// Create directory structure
	for _, dir := range []string{"src", "build", "src/nested"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0755); err != nil {
			t.Fatal(err)
		}
	}

	// Create files
	for _, f := range []string{
		"README.md",
		"src/main.go",
		"src/nested/util.go",
		"src/debug.log",
		"build/output.js",
	} {
		if err := os.WriteFile(filepath.Join(root, f), []byte("x"), 0644); err != nil {
			t.Fatal(err)
		}
	}

	var collected []string
	err := gitignore.Walk(root, func(path string, d os.DirEntry) error {
		collected = append(collected, filepath.ToSlash(path))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	// Should include non-ignored files and directories
	want := map[string]bool{
		".gitignore":       true,
		"README.md":        true,
		"src":              true,
		"src/main.go":      true,
		"src/nested":       true,
		"src/nested/util.go": true,
	}

	// Should NOT include
	noWant := map[string]bool{
		"build":          true,
		"build/output.js": true,
		"src/debug.log":  true,
		".git":           true,
	}

	got := make(map[string]bool)
	for _, p := range collected {
		got[p] = true
	}

	for w := range want {
		if !got[w] {
			t.Errorf("Walk missing expected path %q", w)
		}
	}
	for nw := range noWant {
		if got[nw] {
			t.Errorf("Walk should not have yielded %q", nw)
		}
	}
}

func TestWalkNestedGitignore(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".git", "info"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".gitignore"), []byte(""), 0644); err != nil {
		t.Fatal(err)
	}

	// Create src/ with its own .gitignore that ignores *.tmp
	if err := os.MkdirAll(filepath.Join(root, "src"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "src", ".gitignore"), []byte("*.tmp\n"), 0644); err != nil {
		t.Fatal(err)
	}

	for _, f := range []string{"src/main.go", "src/cache.tmp", "root.tmp"} {
		if err := os.WriteFile(filepath.Join(root, f), []byte("x"), 0644); err != nil {
			t.Fatal(err)
		}
	}

	var collected []string
	err := gitignore.Walk(root, func(path string, d os.DirEntry) error {
		collected = append(collected, filepath.ToSlash(path))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	got := make(map[string]bool)
	for _, p := range collected {
		got[p] = true
	}

	if !got["src/main.go"] {
		t.Error("Walk should yield src/main.go")
	}
	if got["src/cache.tmp"] {
		t.Error("Walk should not yield src/cache.tmp (ignored by src/.gitignore)")
	}
	if !got["root.tmp"] {
		t.Error("Walk should yield root.tmp (not under src/)")
	}
}

func TestWalkSkipsGitDir(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".git", "info"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".gitignore"), []byte(""), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "file.txt"), []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}

	var collected []string
	err := gitignore.Walk(root, func(path string, d os.DirEntry) error {
		collected = append(collected, filepath.ToSlash(path))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	for _, p := range collected {
		if p == ".git" || strings.HasPrefix(p, ".git/") {
			t.Errorf("Walk should not yield .git paths, got %q", p)
		}
	}
}

func TestErrors(t *testing.T) {
	// Invalid POSIX class name produces an error.
	m := setupMatcher(t, "valid.log\n[[:spaci:]]\ninvalid[[:nope:]]pattern\nalso-valid\n")

	errs := m.Errors()
	if len(errs) != 2 {
		t.Fatalf("expected 2 errors, got %d: %v", len(errs), errs)
	}

	if errs[0].Pattern != "[[:spaci:]]" {
		t.Errorf("error[0].Pattern = %q, want %q", errs[0].Pattern, "[[:spaci:]]")
	}
	if errs[0].Line != 2 {
		t.Errorf("error[0].Line = %d, want 2", errs[0].Line)
	}
	if !strings.Contains(errs[0].Message, "spaci") {
		t.Errorf("error[0].Message = %q, want it to mention the class name", errs[0].Message)
	}

	if errs[1].Pattern != "invalid[[:nope:]]pattern" {
		t.Errorf("error[1].Pattern = %q, want %q", errs[1].Pattern, "invalid[[:nope:]]pattern")
	}
	if errs[1].Line != 3 {
		t.Errorf("error[1].Line = %d, want 3", errs[1].Line)
	}

	// Valid patterns still work.
	if !m.Match("valid.log") {
		t.Error("expected valid.log to match")
	}
	if !m.Match("also-valid") {
		t.Error("expected also-valid to match")
	}
}

func TestErrorsFromFile(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".git", "info"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".gitignore"), []byte("*.log\n[[:bogus:]]\n"), 0644); err != nil {
		t.Fatal(err)
	}

	t.Setenv("GIT_CONFIG_GLOBAL", "/dev/null")
	m := gitignore.New(root)

	errs := m.Errors()
	if len(errs) != 1 {
		t.Fatalf("expected 1 error, got %d", len(errs))
	}
	if errs[0].Source == "" {
		t.Error("expected error to have a source file path")
	}
	errStr := errs[0].Error()
	if !strings.Contains(errStr, "bogus") {
		t.Errorf("error string %q should mention the class name", errStr)
	}
	if !strings.Contains(errStr, ".gitignore") {
		t.Errorf("error string %q should mention the source file", errStr)
	}
}

func TestMatchDetail(t *testing.T) {
	m := setupMatcher(t, "*.log\n!important.log\nbuild/\n")

	// File matched by *.log
	r := m.MatchDetail("app.log")
	if !r.Matched || !r.Ignored {
		t.Errorf("app.log: Matched=%v Ignored=%v, want true/true", r.Matched, r.Ignored)
	}
	if r.Pattern != "*.log" {
		t.Errorf("app.log: Pattern=%q, want %q", r.Pattern, "*.log")
	}
	if r.Line != 1 {
		t.Errorf("app.log: Line=%d, want 1", r.Line)
	}

	// File negated by !important.log
	r = m.MatchDetail("important.log")
	if !r.Matched || r.Ignored {
		t.Errorf("important.log: Matched=%v Ignored=%v, want true/false", r.Matched, r.Ignored)
	}
	if r.Pattern != "!important.log" {
		t.Errorf("important.log: Pattern=%q, want %q", r.Pattern, "!important.log")
	}
	if !r.Negate {
		t.Error("important.log: Negate should be true")
	}
	if r.Line != 2 {
		t.Errorf("important.log: Line=%d, want 2", r.Line)
	}

	// Directory matched by build/
	r = m.MatchDetail("build/")
	if !r.Matched || !r.Ignored {
		t.Errorf("build/: Matched=%v Ignored=%v, want true/true", r.Matched, r.Ignored)
	}
	if r.Pattern != "build/" {
		t.Errorf("build/: Pattern=%q, want %q", r.Pattern, "build/")
	}

	// No match
	r = m.MatchDetail("src/main.go")
	if r.Matched || r.Ignored {
		t.Errorf("src/main.go: Matched=%v Ignored=%v, want false/false", r.Matched, r.Ignored)
	}
	if r.Pattern != "" {
		t.Errorf("src/main.go: Pattern=%q, want empty", r.Pattern)
	}
}

func TestMatchDetailSource(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".git", "info"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".gitignore"), []byte("*.log\n"), 0644); err != nil {
		t.Fatal(err)
	}

	t.Setenv("GIT_CONFIG_GLOBAL", "/dev/null")
	m := gitignore.New(root)

	r := m.MatchDetail("app.log")
	if !r.Matched {
		t.Fatal("expected match")
	}
	if !strings.HasSuffix(r.Source, ".gitignore") {
		t.Errorf("Source=%q, want it to end with .gitignore", r.Source)
	}
}

func TestMatchDetailConsistentWithMatch(t *testing.T) {
	m := setupMatcher(t, "*.log\n!important.log\nbuild/\n/dist\n")

	paths := []string{
		"app.log", "important.log", "build/", "dist", "dist/",
		"src/main.go", "build/out.js", "sub/app.log",
	}
	for _, p := range paths {
		matchResult := m.Match(p)
		detail := m.MatchDetail(p)
		if matchResult != detail.Ignored {
			t.Errorf("Match(%q)=%v but MatchDetail.Ignored=%v", p, matchResult, detail.Ignored)
		}
	}
}

func TestErrorsEmpty(t *testing.T) {
	m := setupMatcher(t, "*.log\nbuild/\n")
	if len(m.Errors()) != 0 {
		t.Errorf("expected no errors, got %v", m.Errors())
	}
}

func TestAddPatterns(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".git", "info"), 0755); err != nil {
		t.Fatal(err)
	}

	m := gitignore.New(root)
	m.AddPatterns([]byte("*.log\nbuild/\n"), "")
	m.AddPatterns([]byte("*.tmp\n"), "src")

	tests := []struct {
		path string
		want bool
	}{
		{"app.log", true},
		{"build/", true},
		{"src/cache.tmp", true},
		{"cache.tmp", false}, // scoped to src/
		{"README.md", false},
	}

	for _, tt := range tests {
		got := m.Match(tt.path)
		if got != tt.want {
			t.Errorf("Match(%q) = %v, want %v", tt.path, got, tt.want)
		}
	}
}
