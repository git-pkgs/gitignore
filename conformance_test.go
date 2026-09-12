package gitignore_test

import (
	"bytes"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/git-pkgs/gitignore"
)

// TestConformance compares Match against git check-ignore for every case
// under testdata/conformance. Each case directory contains:
//
//	gitignore  pattern file
//	paths      one query path per line; a trailing slash marks a directory
//	skip       optional; when present, divergences are logged instead of failed
//
// The test builds a temporary git repository per case, materialises every
// path, asks git check-ignore for all of them in one call, and checks the
// library returns the same answer.
func TestConformance(t *testing.T) {
	requireGit(t)
	isolateGitEnv(t)

	cases, err := filepath.Glob("testdata/conformance/*")
	if err != nil {
		t.Fatal(err)
	}
	if len(cases) == 0 {
		t.Fatal("no conformance cases found under testdata/conformance")
	}

	for _, dir := range cases {
		info, err := os.Stat(dir)
		if err != nil || !info.IsDir() {
			continue
		}
		name := filepath.Base(dir)
		t.Run(name, func(t *testing.T) {
			runConformanceCase(t, dir)
		})
	}
}

func runConformanceCase(t *testing.T, dir string) {
	patterns, err := os.ReadFile(filepath.Join(dir, "gitignore"))
	if err != nil {
		t.Fatalf("read gitignore: %v", err)
	}
	rawPaths, err := os.ReadFile(filepath.Join(dir, "paths"))
	if err != nil {
		t.Fatalf("read paths: %v", err)
	}
	skipReason, _ := os.ReadFile(filepath.Join(dir, "skip"))

	paths := parsePathList(string(rawPaths))
	if len(paths) == 0 {
		t.Fatal("no paths to check")
	}

	root := buildRepo(t, string(patterns), paths)
	m := gitignore.New(root)

	want := gitCheckIgnore(t, root, paths)
	report := func(format string, args ...any) {
		if len(skipReason) > 0 {
			t.Logf("(known divergence) "+format, args...)
		} else {
			t.Errorf(format, args...)
		}
	}

	diverged := 0
	for _, p := range paths {
		got := m.Match(p.query())
		if got != want[p.rel] {
			diverged++
			report("path %q (isDir=%v): library=%v git=%v", p.rel, p.isDir, got, want[p.rel])
		}
	}
	if diverged == 0 && len(skipReason) > 0 {
		t.Errorf("case is marked skip (%s) but no longer diverges; remove the skip file",
			strings.TrimSpace(string(skipReason)))
	}
}

// TestConformanceHarnessSelfCheck verifies the batched gitCheckIgnore parse
// by re-asking git for each path individually with the simple exit-code form
// and comparing answers. It exercises every case under testdata/conformance.
// If this fails and TestConformance passes, the batched harness is wrong.
func TestConformanceHarnessSelfCheck(t *testing.T) {
	requireGit(t)
	isolateGitEnv(t)

	cases, _ := filepath.Glob("testdata/conformance/*")
	for _, dir := range cases {
		info, err := os.Stat(dir)
		if err != nil || !info.IsDir() {
			continue
		}
		t.Run(filepath.Base(dir), func(t *testing.T) {
			patterns, _ := os.ReadFile(filepath.Join(dir, "gitignore"))
			rawPaths, _ := os.ReadFile(filepath.Join(dir, "paths"))
			paths := parsePathList(string(rawPaths))
			root := buildRepo(t, string(patterns), paths)

			batched := gitCheckIgnore(t, root, paths)
			for _, p := range paths {
				cmd := exec.Command("git", "check-ignore", "-q", "--no-index", p.rel)
				cmd.Dir = root
				single := cmd.Run() == nil
				if single != batched[p.rel] {
					t.Errorf("path %q: batched=%v single-call=%v", p.rel, batched[p.rel], single)
				}
			}
		})
	}
}

// TestConformanceFuzz generates random pattern sets and paths, then compares
// the library against git check-ignore. It is skipped under -short.
func TestConformanceFuzz(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping fuzz comparison in short mode")
	}
	requireGit(t)
	isolateGitEnv(t)

	const (
		maxPatterns   = 6
		pathsPerRound = 30
	)
	rounds := envInt("GITIGNORE_FUZZ_ROUNDS", 40)
	seed := int64(envInt("GITIGNORE_FUZZ_SEED", 1))
	rng := rand.New(rand.NewSource(seed))

	for r := 0; r < rounds; r++ {
		patterns := randomPatterns(rng, 1+rng.Intn(maxPatterns))
		paths := randomPaths(rng, pathsPerRound)

		root := buildRepo(t, patterns, paths)
		m := gitignore.New(root)
		want := gitCheckIgnore(t, root, paths)

		for _, p := range paths {
			got := m.Match(p.query())
			if got != want[p.rel] {
				t.Errorf("round %d\npatterns:\n%s\npath %q (isDir=%v): library=%v git=%v",
					r, indent(patterns), p.rel, p.isDir, got, want[p.rel])
			}
		}
		_ = os.RemoveAll(root)
	}
}

type conformancePath struct {
	rel   string
	isDir bool
}

func (p conformancePath) query() string {
	if p.isDir {
		return p.rel + "/"
	}
	return p.rel
}

func parsePathList(s string) []conformancePath {
	var out []conformancePath
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		isDir := strings.HasSuffix(line, "/")
		out = append(out, conformancePath{
			rel:   strings.TrimSuffix(line, "/"),
			isDir: isDir,
		})
	}
	return out
}

// buildRepo creates a temporary git repository containing the given
// .gitignore and materialises each path as a file or directory so that
// git check-ignore has real filesystem entries to consult.
func buildRepo(t *testing.T, patterns string, paths []conformancePath) string {
	t.Helper()
	root := t.TempDir()

	cmd := exec.Command("git", "-c", "init.defaultBranch=main", "init", "-q")
	cmd.Dir = root
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	if err := os.WriteFile(filepath.Join(root, ".gitignore"), []byte(patterns), 0o644); err != nil {
		t.Fatal(err)
	}

	// Create directories first, deepest last is fine since MkdirAll handles
	// intermediates. Create files afterwards so a file path does not get
	// turned into a directory by a later entry that lives under it. Sort
	// files shortest-first for the same reason in the other direction.
	var files, dirs []conformancePath
	for _, p := range paths {
		if p.isDir {
			dirs = append(dirs, p)
		} else {
			files = append(files, p)
		}
	}
	sort.Slice(files, func(i, j int) bool { return len(files[i].rel) < len(files[j].rel) })

	for _, p := range dirs {
		if err := os.MkdirAll(filepath.Join(root, filepath.FromSlash(p.rel)), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, p := range files {
		full := filepath.Join(root, filepath.FromSlash(p.rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if info, err := os.Stat(full); err == nil && info.IsDir() {
			continue
		}
		if err := os.WriteFile(full, nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// Verify what was materialised matches what the caller declared, so
	// git and the library see the same isDir for every path. A mismatch
	// means the paths file lists the same name as both a file and (part
	// of) a directory, which cannot be represented on disk.
	for _, p := range paths {
		info, err := os.Stat(filepath.Join(root, filepath.FromSlash(p.rel)))
		if err != nil {
			t.Fatalf("materialise %q: %v", p.rel, err)
		}
		if info.IsDir() != p.isDir {
			t.Fatalf("path %q declared isDir=%v but is a %v on disk; fix the paths file",
				p.rel, p.isDir, kind(info.IsDir()))
		}
	}
	return root
}

func kind(isDir bool) string {
	if isDir {
		return "directory"
	}
	return "file"
}

// gitCheckIgnore asks git which of the given paths are ignored, in a single
// batched call. It returns a map from relative path to the ignore result.
// --no-index avoids the tracked-file exemption; -n prints unmatched paths
// too; -z uses NUL separators so patterns containing colons or tabs do not
// break parsing.
func gitCheckIgnore(t *testing.T, root string, paths []conformancePath) map[string]bool {
	t.Helper()

	var stdin bytes.Buffer
	for _, p := range paths {
		stdin.WriteString(p.rel)
		stdin.WriteByte(0)
	}

	cmd := exec.Command("git", "-c", "core.excludesFile=", "check-ignore",
		"--no-index", "-z", "-v", "-n", "--stdin")
	cmd.Dir = root
	cmd.Stdin = &stdin
	out, err := cmd.Output()
	if err != nil {
		// check-ignore exits 1 when no path is ignored; that is not an error
		// for our purposes. Any other failure is.
		if ee, ok := err.(*exec.ExitError); !ok || ee.ExitCode() != 1 {
			t.Fatalf("git check-ignore: %v\n%s", err, ee.Stderr)
		}
	}

	result := make(map[string]bool, len(paths))
	fields := strings.Split(strings.TrimSuffix(string(out), "\x00"), "\x00")
	if len(fields) != 4*len(paths) {
		t.Fatalf("git check-ignore returned %d fields for %d paths (want %d)",
			len(fields), len(paths), 4*len(paths))
	}
	// Output is groups of four fields: source, linenum, pattern, path.
	for i := 0; i+3 < len(fields); i += 4 {
		pattern := fields[i+2]
		path := fields[i+3]
		ignored := pattern != "" && !strings.HasPrefix(pattern, "!")
		result[path] = ignored
	}
	if len(result) != len(paths) {
		t.Fatalf("git check-ignore returned %d distinct paths for %d inputs", len(result), len(paths))
	}
	return result
}

func requireGit(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
}

// isolateGitEnv points HOME, XDG_CONFIG_HOME and the git global/system config
// locations at an empty temporary directory so neither the git subprocess nor
// gitignore.New picks up the user's global excludes.
func isolateGitEnv(t *testing.T) {
	t.Helper()
	empty := t.TempDir()
	t.Setenv("HOME", empty)
	t.Setenv("USERPROFILE", empty)
	t.Setenv("XDG_CONFIG_HOME", empty)
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(empty, "gitconfig"))
	t.Setenv("GIT_CONFIG_SYSTEM", filepath.Join(empty, "gitconfig"))
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
}

func envInt(name string, def int) int {
	if v, err := strconv.Atoi(os.Getenv(name)); err == nil && v > 0 {
		return v
	}
	return def
}

func indent(s string) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	for i, l := range lines {
		lines[i] = "  " + l
	}
	return strings.Join(lines, "\n")
}

// randomPatterns builds a small .gitignore from a fixed vocabulary of
// segments and pattern shapes. It is deterministic for a given rng state.
func randomPatterns(rng *rand.Rand, n int) string {
	segs := []string{
		"a", "b", "c", "*", "**", "*.log", "x*", "*x",
		"?", "??", "a?", "?b",
		"[ab]", "[!ab]", "[a-c]", "[[:lower:]]", "[a*]b", "[a?]",
		"a*b", "\\*", "\\!a", "***", "*a*",
	}
	var b strings.Builder
	for i := 0; i < n; i++ {
		depth := 1 + rng.Intn(4)
		parts := make([]string, depth)
		for j := range parts {
			parts[j] = segs[rng.Intn(len(segs))]
		}
		p := strings.Join(parts, "/")
		// Reject patterns git refuses or that have no useful effect.
		if p == "" || p == "**" || p == "/" || strings.Contains(p, "**/**") {
			i--
			continue
		}
		if rng.Intn(4) == 0 {
			p = "/" + p
		}
		if rng.Intn(4) == 0 {
			p += "/"
		}
		if i > 0 && rng.Intn(3) == 0 {
			p = "!" + p
		}
		b.WriteString(p)
		b.WriteByte('\n')
	}
	return b.String()
}

// randomPaths builds a set of query paths from the same segment vocabulary
// as randomPatterns so they have a reasonable chance of matching. It avoids
// producing a file path that is also a parent of another path, so that
// everything can be materialised on disk consistently.
func randomPaths(rng *rand.Rand, n int) []conformancePath {
	segs := []string{"a", "b", "c", "d", "ab", "ax", "xb", "x1", "app.log", "keep", "!a"}

	asDir := make(map[string]bool)
	asFile := make(map[string]bool)
	seen := make(map[string]bool)
	var out []conformancePath
tries:
	for len(out) < n {
		depth := 1 + rng.Intn(4)
		parts := make([]string, depth)
		for j := range parts {
			parts[j] = segs[rng.Intn(len(segs))]
		}
		rel := strings.Join(parts, "/")
		if seen[rel] {
			continue
		}
		isDir := rng.Intn(3) == 0
		// Every proper prefix must be a directory; reject if any is
		// already a file. The full path must not already be a directory
		// if we picked file, or a file if we picked directory.
		for i := 1; i < depth; i++ {
			if asFile[strings.Join(parts[:i], "/")] {
				continue tries
			}
		}
		if isDir && asFile[rel] || !isDir && asDir[rel] {
			continue
		}
		for i := 1; i < depth; i++ {
			asDir[strings.Join(parts[:i], "/")] = true
		}
		if isDir {
			asDir[rel] = true
		} else {
			asFile[rel] = true
		}
		seen[rel] = true
		out = append(out, conformancePath{rel: rel, isDir: isDir})
	}
	return out
}
