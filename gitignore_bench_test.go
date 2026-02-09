package gitignore_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/git-pkgs/gitignore"
)

func benchMatcher(b *testing.B, patterns string) *gitignore.Matcher {
	b.Helper()
	root := b.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".git", "info"), 0755); err != nil {
		b.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".gitignore"), []byte(patterns), 0644); err != nil {
		b.Fatal(err)
	}
	return gitignore.New(root)
}

func realisticPatterns() string {
	var b strings.Builder
	// Extensions
	exts := []string{"log", "tmp", "bak", "swp", "swo", "o", "a", "so", "dylib",
		"pyc", "pyo", "class", "jar", "war", "ear", "dll", "exe", "obj", "lib",
		"out", "app", "DS_Store", "thumbs.db", "desktop.ini", "iml", "ipr", "iws"}
	for _, ext := range exts {
		fmt.Fprintf(&b, "*.%s\n", ext)
	}
	// Directories
	dirs := []string{"node_modules/", "vendor/", "build/", "dist/", "target/",
		".cache/", ".tmp/", "__pycache__/", ".pytest_cache/", "coverage/",
		".nyc_output/", ".next/", ".nuxt/", ".output/", ".vscode/", ".idea/",
		".gradle/", ".mvn/", "bin/", "obj/"}
	for _, d := range dirs {
		b.WriteString(d)
		b.WriteByte('\n')
	}
	// Doublestar patterns
	dsPats := []string{"**/logs/**", "**/.env", "**/.env.*", "**/secret*",
		"**/credentials.*", "**/*.min.js", "**/*.min.css", "**/*.map"}
	for _, p := range dsPats {
		b.WriteString(p)
		b.WriteByte('\n')
	}
	// Negation
	b.WriteString("!.env.example\n")
	b.WriteString("!important.log\n")
	// Anchored
	b.WriteString("/Makefile.local\n")
	b.WriteString("/config/local.yml\n")
	// Bracket
	b.WriteString("*.[oa]\n")
	b.WriteString("*~\n")
	b.WriteString(".*.sw[a-p]\n")
	return b.String()
}

func BenchmarkCompile(b *testing.B) {
	patterns := realisticPatterns()
	root := b.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".git", "info"), 0755); err != nil {
		b.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".gitignore"), []byte(patterns), 0644); err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	for b.Loop() {
		gitignore.New(root)
	}
}

func BenchmarkMatchHit(b *testing.B) {
	m := benchMatcher(b, realisticPatterns())
	b.ResetTimer()
	for b.Loop() {
		m.Match("src/app.log")
	}
}

func BenchmarkMatchMiss(b *testing.B) {
	m := benchMatcher(b, realisticPatterns())
	b.ResetTimer()
	for b.Loop() {
		m.Match("src/main.go")
	}
}

func BenchmarkMatchLargePatternSet(b *testing.B) {
	var sb strings.Builder
	sb.WriteString(realisticPatterns())
	for i := range 200 {
		fmt.Fprintf(&sb, "pattern_%d_*.txt\n", i)
	}
	m := benchMatcher(b, sb.String())
	b.ResetTimer()
	for b.Loop() {
		m.Match("src/components/Button.tsx")
	}
}

func BenchmarkMatchDeepPath(b *testing.B) {
	m := benchMatcher(b, realisticPatterns())
	b.ResetTimer()
	for b.Loop() {
		m.Match("a/b/c/d/e/f/g/file.txt")
	}
}
