package gitignore_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/git-pkgs/gitignore"
)

func TestMatchPathDepth(t *testing.T) {
	for _, depth := range []int{1, 16, 17, 64} {
		t.Run(fmt.Sprint(depth), func(t *testing.T) {
			m := gitignore.New("")
			m.AddPatterns([]byte("*.log\n!keep.log\nbuild/\n"), "")
			for _, tc := range []struct {
				name string
				want bool
			}{
				{"app.log", true}, {"keep.log", false}, {"main.go", false},
				{"build/file.go", true}, {"build/", true},
			} {
				path := strings.Repeat("d/", depth-1) + tc.name
				if got := m.Match(path); got != tc.want {
					t.Errorf("Match(%q) = %v, want %v", path, got, tc.want)
				}
				if got := m.MatchPath(strings.TrimSuffix(path, "/"), strings.HasSuffix(path, "/")); got != tc.want {
					t.Errorf("MatchPath(%q) = %v, want %v", path, got, tc.want)
				}
				if got := m.MatchDetail(path).Ignored; got != tc.want {
					t.Errorf("MatchDetail(%q) = %v, want %v", path, got, tc.want)
				}
			}
		})
	}
}

func TestMatchConcurrent(t *testing.T) {
	m := gitignore.New("")
	m.AddPatterns([]byte("*.log\n!keep.log\nbuild/\n"), "")
	m.AddPatterns([]byte("!trace.log\n"), "src")
	for _, tc := range []struct {
		path string
		want bool
	}{
		{"src/app.log", true}, {"src/keep.log", false}, {"src/trace.log", false},
		{"docs/trace.log", true}, {"build/file.go", true},
		{strings.Repeat("d/", 64) + "app.log", true},
	} {
		t.Run(tc.path, func(t *testing.T) {
			t.Parallel()
			for range 100 {
				if m.Match(tc.path) != tc.want || m.MatchPath(tc.path, false) != tc.want || m.MatchDetail(tc.path).Ignored != tc.want {
					t.Fatalf("inconsistent match for %q, want %v", tc.path, tc.want)
				}
			}
		})
	}
}

func BenchmarkAddPatterns(b *testing.B) {
	for _, count := range []int{10, 100, 1000} {
		for _, scope := range []string{"", "src/pkg/internal/generated"} {
			b.Run(fmt.Sprintf("Rules%d/Scope%t", count, scope != ""), func(b *testing.B) {
				rules := []string{"*.log\n", "/cache/\n", "!keep.log\n"}
				var text strings.Builder
				for i := range count {
					text.WriteString(rules[i%len(rules)])
				}
				data := []byte(text.String())
				for b.Loop() {
					m := gitignore.New("")
					m.AddPatterns(data, scope)
				}
			})
		}
	}
}

func BenchmarkMatchDepth(b *testing.B) {
	for _, depth := range []int{1, 4, 16, 64} {
		for _, empty := range []bool{false, true} {
			b.Run(fmt.Sprintf("Depth%d/Empty%t", depth, empty), func(b *testing.B) {
				m := gitignore.New("")
				if !empty {
					m.AddPatterns([]byte(realisticPatterns()), "")
				}
				path := strings.Repeat("src/", depth-1) + "main.go"
				for b.Loop() {
					m.Match(path)
				}
			})
		}
	}
}

func BenchmarkMatchScopes(b *testing.B) {
	for _, count := range []int{10, 100, 1000} {
		b.Run(fmt.Sprintf("Siblings%d", count), func(b *testing.B) {
			m := gitignore.New("")
			for i := range count {
				m.AddPatterns([]byte("*.log\ncache/\n!keep.log\n"), fmt.Sprintf("pkg%d", i))
			}
			for b.Loop() {
				m.Match("pkg0/src/keep.log")
			}
		})
	}
}

func BenchmarkMatchPatternOrder(b *testing.B) {
	for _, count := range []int{10, 100, 1000} {
		for _, position := range []string{"First", "Last", "Miss"} {
			b.Run(fmt.Sprintf("Rules%d/%s", count, position), func(b *testing.B) {
				m := gitignore.New("")
				for i := range count {
					m.AddPatterns([]byte(fmt.Sprintf("pattern_%d_*.log", i)), "")
				}
				index := 0
				switch position {
				case "Last":
					index = count - 1
				case "Miss":
					index = count
				}
				path := fmt.Sprintf("src/pattern_%d_file.log", index)
				if got := m.Match(path); got != (position != "Miss") {
					b.Fatalf("unexpected result for %q: %v", path, got)
				}
				for b.Loop() {
					m.Match(path)
				}
			})
		}
	}
}

func BenchmarkMatchShapes(b *testing.B) {
	for _, tc := range []struct{ name, pattern, path string }{
		{"Literal", "target", "src/target"},
		{"Suffix", "*.log", "src/application.log"},
		{"Prefix", "generated*", "src/generated_file.go"},
		{"Stars", "a*b*c", "src/aaaaabbbbbc"},
		{"Brackets", "[a-z][a-z][0-9].log", "src/ab5.log"},
		{"POSIX", "[[:alpha:]][[:digit:]].log", "src/a5.log"},
		{"DoubleStars", "**/a/**/b/**/target", "x/a/y/a/z/b/q/target"},
		{"NearMiss", "a*a*a*a*b", "src/aaaaaaaaaaaaaaaaaaaaac"},
	} {
		b.Run(tc.name, func(b *testing.B) {
			m := gitignore.New("")
			m.AddPatterns([]byte(tc.pattern), "")
			if got := m.Match(tc.path); got != (tc.name != "NearMiss") {
				b.Fatalf("unexpected result for %q: %v", tc.path, got)
			}
			for b.Loop() {
				m.Match(tc.path)
			}
		})
	}
}

func BenchmarkMatchMixedParallel(b *testing.B) {
	m := gitignore.New("")
	m.AddPatterns([]byte(realisticPatterns()), "")
	paths := []string{"src/main.go", "vendor/pkg/file.go", "app.log", "important.log", "src/a/b/c/file.go"}
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			m.Match(paths[i])
			i = (i + 1) % len(paths)
		}
	})
}

func BenchmarkWalkTree(b *testing.B) {
	for _, tc := range []struct {
		name    string
		width   int
		depth   int
		ignored bool
	}{
		{"Wide", 100, 1, false},
		{"Deep", 1, 32, false},
		{"Pruned", 100, 1, true},
	} {
		b.Run(tc.name, func(b *testing.B) {
			b.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
			root := benchTree(b, tc.width, tc.depth, tc.ignored)
			for b.Loop() {
				if err := gitignore.Walk(root, func(_ string, _ os.DirEntry) error { return nil }); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func benchTree(b *testing.B, width, depth int, ignored bool) string {
	b.Helper()
	root := b.TempDir()
	for i := range width {
		dir := filepath.Join(root, fmt.Sprintf("pkg%d", i))
		for range depth {
			dir = filepath.Join(dir, "src")
			if err := os.MkdirAll(dir, 0o755); err != nil {
				b.Fatal(err)
			}
			for name, data := range map[string]string{
				".gitignore": "*.log\ncache/\n!keep.log\n",
				"main.go":    "", "debug.log": "", "keep.log": "",
			} {
				if err := os.WriteFile(filepath.Join(dir, name), []byte(data), 0o644); err != nil {
					b.Fatal(err)
				}
			}
		}
	}
	if ignored {
		if err := os.WriteFile(filepath.Join(root, ".gitignore"), []byte("pkg*\n!pkg0\n"), 0o644); err != nil {
			b.Fatal(err)
		}
	}
	return root
}

func TestMatchInterleavedScopes(t *testing.T) {
	m := gitignore.New("")
	for _, step := range []struct {
		pattern, scope string
		ignored        bool
	}{
		{"*.log", "", true},
		{"!keep.log", "src", false},
		{"keep.log", "other", false},
		{"src/keep.log", "", true},
		{"!keep.log", "src", false},
		{"[broken", "src", false},
	} {
		m.AddPatterns([]byte(step.pattern), step.scope)
		if got := m.Match("src/keep.log"); got != step.ignored {
			t.Errorf("after %q in %q: ignored = %v, want %v", step.pattern, step.scope, got, step.ignored)
		}
		if got := m.MatchDetail("src/keep.log").Ignored; got != step.ignored {
			t.Errorf("after %q in %q: detail ignored = %v, want %v", step.pattern, step.scope, got, step.ignored)
		}
	}
}
