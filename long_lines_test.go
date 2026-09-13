package gitignore_test

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/git-pkgs/gitignore"
)

func TestLongIgnoreLines(t *testing.T) {
	requireGit(t)
	isolateGitEnv(t)
	for _, size := range []int{65535, 65536, 70000} {
		for _, prefix := range []string{"", "#"} {
			for _, newline := range []string{"\n", "\r\n"} {
				t.Run(fmt.Sprintf("Size%d/Comment%t/CRLF%t", size, prefix != "", newline == "\r\n"), func(t *testing.T) {
					checkLongIgnoreLines(t, size, prefix, newline)
				})
			}
		}
	}
}

func checkLongIgnoreLines(t *testing.T, size int, prefix, newline string) {
	t.Helper()
	long := strings.Repeat("a", size)
	data := []byte(prefix + long + newline + newline + "*.log" + newline + "[" + newline + "!keep.log")
	paths := parsePathList("app.log\nkeep.log\nmain.go\n")
	root := buildRepo(t, string(data), paths)
	want := gitCheckIgnore(t, root, paths)
	programmatic := gitignore.New("")
	programmatic.AddPatterns(data, "")
	file := gitignore.New("")
	source := filepath.Join(root, ".gitignore")
	file.AddFromFile(source, "")
	for _, m := range []*gitignore.Matcher{programmatic, file, gitignore.New(root)} {
		for _, p := range paths {
			if got := m.Match(p.query()); got != want[p.rel] {
				t.Errorf("Match(%q) = %v, Git = %v", p.rel, got, want[p.rel])
			}
		}
		if got := m.Match(long); got != (prefix == "") {
			t.Errorf("long pattern match = %v", got)
		}
		expectedSource := source
		if m == programmatic {
			expectedSource = ""
		}
		detail := m.MatchDetail("app.log")
		if detail.Line != 3 || detail.Source != expectedSource {
			t.Errorf("MatchDetail = %+v", detail)
		}
		errs := m.Errors()
		if len(errs) != 1 || errs[0].Line != 4 || errs[0].Source != expectedSource {
			t.Errorf("Errors = %v", errs)
		}
	}
}

func BenchmarkParseIgnoreLines(b *testing.B) {
	for _, count := range []int{10, 1000} {
		b.Run(fmt.Sprint(count), func(b *testing.B) {
			data := []byte(strings.Repeat("*.log\n# comment\n!keep.log\n", count))
			b.ReportAllocs()
			for b.Loop() {
				m := gitignore.New("")
				m.AddPatterns(data, "")
			}
		})
	}
}
