package gitignore_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/git-pkgs/gitignore"
)

func TestBracketValidationAndMatching(t *testing.T) {
	isolateGitEnv(t)
	for _, tc := range []struct {
		pattern, path, message string
		ignored                bool
	}{
		{"[!][:digit:]]", "a", "", true},
		{"[!][:digit:]]", "]", "", false},
		{"[!][:digit:]]", "5", "", false},
		{`[\[:bogus:]]`, "b]", "", true},
		{"[a-[:unknown:]]", "a", "unknown POSIX class [:unknown:]", false},
		{"[[:unknown:]]", "a", "unknown POSIX class [:unknown:]", false},
		{"[!]]", "a", "", true},
		{"[!]", "a", "unclosed bracket expression", false},
		{`[a\]`, "a", "unclosed bracket expression", false},
	} {
		t.Run(tc.pattern+"/"+tc.path, func(t *testing.T) {
			m := gitignore.New("")
			m.AddPatterns([]byte(tc.pattern+"\n"), "")
			if got := m.Match(tc.path); got != tc.ignored {
				t.Errorf("Match(%q) = %v, want %v", tc.path, got, tc.ignored)
			}
			errs := m.Errors()
			if tc.message == "" {
				if len(errs) != 0 {
					t.Fatalf("Errors() = %v", errs)
				}
			} else if len(errs) != 1 || errs[0].Message != tc.message {
				t.Fatalf("Errors() = %v, want %q", errs, tc.message)
			}
		})
	}
}

func TestWalkCallbackError(t *testing.T) {
	isolateGitEnv(t)
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "src", "nested"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "src", "nested", "file.go"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	for _, start := range []string{"", "src"} {
		for _, stop := range []string{"src", "src/nested/file.go"} {
			t.Run(start+"/"+stop, func(t *testing.T) {
				want := errors.New("callback failed")
				stopped := false
				err := gitignore.WalkFrom(root, start, func(path string, _ os.DirEntry) error {
					if stopped {
						t.Fatal("callback called after error")
					}
					if filepath.ToSlash(path) == stop {
						stopped = true
						return want
					}
					return nil
				})
				if !errors.Is(err, want) {
					t.Fatalf("WalkFrom() = %v, want %v", err, want)
				}
			})
		}
		if err := gitignore.WalkFrom(root, start, nil); err != nil {
			t.Fatalf("WalkFrom with nil callback: %v", err)
		}
	}
}
