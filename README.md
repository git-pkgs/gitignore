# gitignore

A Go library for matching paths against gitignore rules. Pattern matching uses a direct wildmatch implementation (two-pointer backtracking, same algorithm as git's `wildmatch.c`) rather than compiling patterns to regexes or delegating to `filepath.Match`

- Wildmatch engine modeled on git's own `wildmatch.c`, tested against git's wildmatch test suite
- Bracket expressions with ranges, negation, backslash escapes, and all 12 POSIX character classes (`[:alnum:]`, `[:alpha:]`, etc.)
- Proper `**` handling (zero or more directories, only when standalone between separators)
- `core.excludesfile` support with XDG fallback
- Automatic nested `.gitignore` discovery via `NewFromDirectory` and `Walk`
- Negation patterns with correct last-match-wins semantics
- Directory-only patterns (trailing `/`) with descendant matching
- Match provenance via `MatchDetail` (which pattern, file, and line number matched)
- Invalid pattern surfacing via `Errors()`
- Fast rejection for literal names and suffix patterns like `*.log`

```go
import "github.com/git-pkgs/gitignore"
```

## Loading patterns

`New` reads the user's global excludes file, `.git/info/exclude`, and the root `.gitignore`:

```go
m := gitignore.New("/path/to/repo")
m.Match("vendor/lib.go")  // true if matched
m.Match("vendor/")        // trailing slash tests as directory
```

For repos with nested `.gitignore` files, `NewFromDirectory` walks the tree and loads them all, scoped to their containing directory:

```go
m := gitignore.NewFromDirectory("/path/to/repo")
```

To create a matcher with only programmatic patterns (no filesystem loading), pass an empty root:

```go
m := gitignore.New("")
m.AddPatterns([]byte("*.tmp\n"), "")
```

You can also add patterns to any matcher manually:

```go
m.AddFromFile("/path/to/repo/src/.gitignore", "src")
m.AddPatterns([]byte("*.log\nbuild/\n"), "")
```

To limit the bytes read from each ignore file, pass `MaxIgnoreFileSize` to any constructor or walk function:

```go
m := gitignore.NewFromDirectory("/path/to/repo", gitignore.MaxIgnoreFileSize(1<<20))
```

The limit applies to global excludes, `.git/info/exclude`, and `.gitignore` files, including later `AddFromFile` calls. Oversized files are skipped entirely and recorded in `Errors()` with their source path and line zero. Nonpositive limits are unlimited. `AddPatterns` is unaffected, and the limit does not cap total memory across files.

## Matching

`Match` uses the trailing-slash convention to distinguish files from directories. If you already know whether the path is a directory, `MatchPath` avoids that:

```go
m.Match("vendor/")             // directory
m.MatchPath("vendor", true)    // same thing, no trailing slash needed
```

To find out which pattern matched (useful for debugging), use `MatchDetail`:

```go
r := m.MatchDetail("app.log")
if r.Matched {
    fmt.Printf("ignored by %s (line %d of %s)\n", r.Pattern, r.Line, r.Source)
}
```

## Walking a directory tree

`Walk` traverses the repo, loading `.gitignore` files as it descends and skipping ignored entries. It never descends into `.git` or ignored directories.

```go
gitignore.Walk("/path/to/repo", func(path string, d fs.DirEntry) error {
    fmt.Println(path)
    return nil
})
```

`WalkFrom` walks a subdirectory while still respecting root-level rules. It loads global excludes, `.git/info/exclude`, the root `.gitignore`, and every `.gitignore` between the root and the start directory before walking, so patterns scoped above the start still apply. Paths passed to `fn` are relative to the root.

```go
gitignore.WalkFrom("/path/to/repo", "src/pkg", func(path string, d fs.DirEntry) error {
    fmt.Println(path) // e.g. "src/pkg/lib.go"
    return nil
})
```

`Walk` and `WalkFrom` accept the same options as trailing arguments. With `MaxIgnoreFileSize` set they stop and return an `*IgnoreFileSizeError` when an oversized file is encountered. Its `Path` and `Limit` fields identify the file and configured byte limit; use `errors.As` to inspect it. Callbacks may already have run for earlier entries.

## Error handling

Invalid patterns (like unknown POSIX character classes) are silently skipped during matching. To inspect them:

```go
for _, err := range m.Errors() {
    fmt.Println(err) // includes source file, line number, and reason
}
```

## Thread safety

A Matcher is safe for concurrent `Match`/`MatchPath`/`MatchDetail` calls once construction is complete. Don't call `AddPatterns` or `AddFromFile` concurrently with matching.

## Match semantics

Paths should use forward slashes and be relative to the repository root. An ignored parent directory excludes all its descendants; otherwise, the last matching rule for the path wins.

## Benchmarks

Run benchmarks on an otherwise idle machine, using the same Go toolchain for both revisions. Record repeated samples and memory allocations:

```sh
go test -run '^$' -bench . -benchmem -count=10 -cpu=1
```

`BenchmarkCompile` and `BenchmarkWalkTree` include filesystem reads and a Git subprocess through `New`; `BenchmarkAddPatterns` measures parsing without filesystem access. Compare samples with `benchstat` and check the timing spread before quoting speed changes.

## License

MIT
