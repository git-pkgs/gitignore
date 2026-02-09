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
- Literal suffix fast-reject for common patterns like `*.log`

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

You can also add patterns manually:

```go
m.AddFromFile("/path/to/repo/src/.gitignore", "src")
m.AddPatterns([]byte("*.log\nbuild/\n"), "")
```

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

Paths should use forward slashes and be relative to the repository root. Last-match-wins, same as git.

## License

MIT
