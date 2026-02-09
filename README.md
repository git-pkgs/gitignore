# gitignore

A Go library for matching paths against gitignore rules. Handles the full gitignore spec including negation patterns, `**` globs, bracket expressions with POSIX character classes, directory-only patterns, and scoped patterns from nested `.gitignore` files.

```go
import "github.com/git-pkgs/gitignore"

// Load patterns from .git/info/exclude and root .gitignore
m := gitignore.New("/path/to/repo")

// Add patterns from nested .gitignore files
m.AddFromFile("/path/to/repo/src/.gitignore", "src")

// Or add patterns directly
m.AddPatterns([]byte("*.log\nbuild/\n"), "")

// Check if a path is ignored (use trailing slash for directories)
m.Match("vendor/lib.go")  // true if matched
m.Match("vendor/")        // tests as directory
```

Paths passed to `Match` should use forward slashes and be relative to the repository root. Directory paths need a trailing slash so that directory-only patterns (written with a trailing `/` in `.gitignore`) work correctly.

Uses last-match-wins semantics, same as git.
