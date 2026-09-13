package gitignore_test

import (
	"errors"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/git-pkgs/gitignore"
)

func writeIgnoreFile(t *testing.T, path, data string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestIgnoreFileSizeBoundary(t *testing.T) {
	isolateGitEnv(t)
	root := t.TempDir()
	path := filepath.Join(root, ".gitignore")
	writeIgnoreFile(t, path, "*.log")
	for _, limit := range []int64{-1, 0, 4, 5, 6, math.MaxInt64} {
		m := gitignore.New(root, gitignore.MaxIgnoreFileSize(limit))
		if got := m.Match("app.log"); got != (limit != 4) {
			t.Errorf("limit %d: Match = %v", limit, got)
		}
		if limit == 4 {
			checkSizeDiagnostic(t, m, path)
		} else if len(m.Errors()) != 0 {
			t.Errorf("limit %d: Errors = %v", limit, m.Errors())
		}
	}
	m := gitignore.New("", gitignore.MaxIgnoreFileSize(4))
	m.AddFromFile(path, "src")
	checkSizeDiagnostic(t, m, path)
	if m.Match("src/app.log") {
		t.Fatal("oversized file was partially applied")
	}
	m.AddPatterns([]byte("*.log"), "src")
	if !m.Match("src/app.log") {
		t.Fatal("file limit applied to AddPatterns")
	}
	if !gitignore.New(root).Match("app.log") {
		t.Fatal("default constructor changed")
	}
}

func checkSizeDiagnostic(t *testing.T, m *gitignore.Matcher, path string) {
	t.Helper()
	errs := m.Errors()
	if len(errs) != 1 {
		t.Fatalf("Errors = %v", errs)
	}
	if errs[0].Source != path || errs[0].Line != 0 || errs[0].Pattern != "" {
		t.Errorf("diagnostic = %+v", errs[0])
	}
	if !strings.Contains(errs[0].Error(), "size limit") || strings.Contains(errs[0].Error(), "invalid pattern") {
		t.Errorf("diagnostic text = %s", errs[0].Error())
	}
}

func TestIgnoreFileSizeSources(t *testing.T) {
	isolateGitEnv(t)
	for _, source := range []string{".gitignore", ".git/info/exclude", ".global/git/ignore", "src/.gitignore"} {
		t.Run(source, func(t *testing.T) { checkSizeLimitedSource(t, source) })
	}
}

func checkSizeLimitedSource(t *testing.T, source string) {
	t.Helper()
	root := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(root, ".global"))
	path := filepath.Join(root, source)
	writeIgnoreFile(t, path, "*.log\n")
	writeIgnoreFile(t, filepath.Join(root, "src/deep/file.log"), "")
	opt := gitignore.MaxIgnoreFileSize(5)
	m := gitignore.NewFromDirectory(root, opt)
	checkSizeDiagnostic(t, m, path)
	if m.Match("src/deep/file.log") {
		t.Fatal("skipped rules were applied")
	}
	for _, start := range []string{"", ".", "src", "src/deep"} {
		err := gitignore.WalkFrom(root, start, func(path string, _ os.DirEntry) error {
			if filepath.ToSlash(path) == "src/deep/file.log" {
				t.Error("walk continued past oversized ignore file")
			}
			return nil
		}, opt)
		checkSizeError(t, err, path, 5)
	}
	checkSizeError(t, gitignore.Walk(root, nil, opt), path, 5)
	if err := gitignore.Walk(root, nil); err != nil {
		t.Fatal(err)
	}
}

func checkSizeError(t *testing.T, err error, path string, limit int64) {
	t.Helper()
	var sizeErr *gitignore.IgnoreFileSizeError
	if !errors.As(err, &sizeErr) {
		t.Fatalf("error = %v, want IgnoreFileSizeError", err)
	}
	if sizeErr.Path != path || sizeErr.Limit != limit {
		t.Errorf("error = %+v", sizeErr)
	}
}

func TestSizeLimitedDiscoveryContinues(t *testing.T) {
	isolateGitEnv(t)
	root := t.TempDir()
	writeIgnoreFile(t, filepath.Join(root, "a/.gitignore"), "*.log\n")
	writeIgnoreFile(t, filepath.Join(root, "b/.gitignore"), "*.go")
	m := gitignore.NewFromDirectory(root, gitignore.MaxIgnoreFileSize(5))
	if !m.Match("b/main.go") {
		t.Fatal("discovery stopped after oversized file")
	}
	checkSizeDiagnostic(t, m, filepath.Join(root, "a/.gitignore"))
}
