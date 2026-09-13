package gitignore

import (
	"io"
	"os"
	"strconv"
)

// Option configures a Matcher at construction time.
type Option func(*Matcher)

// MaxIgnoreFileSize limits the bytes read from each ignore file. Nonpositive
// values are unlimited. AddPatterns is unaffected because its data is already
// in memory.
func MaxIgnoreFileSize(n int64) Option {
	return func(m *Matcher) { m.maxIgnoreFileSize = n }
}

// IgnoreFileSizeError reports an ignore file that exceeded its byte limit.
type IgnoreFileSizeError struct {
	Path  string
	Limit int64
}

func (e *IgnoreFileSizeError) Error() string {
	return e.Path + ": " + e.message()
}

func (e *IgnoreFileSizeError) message() string {
	return "ignore file exceeds size limit of " + strconv.FormatInt(e.Limit, 10) + " bytes"
}

func readIgnoreFile(path string, limit int64) ([]byte, error) {
	if limit <= 0 {
		return os.ReadFile(path)
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if info.Size() > limit {
		return nil, &IgnoreFileSizeError{Path: path, Limit: limit}
	}
	data, err := io.ReadAll(io.LimitReader(f, limit))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) < limit {
		return data, nil
	}
	// The file can grow after Stat. Probe without overflowing limit+1.
	if n, err := io.CopyN(io.Discard, f, 1); n != 0 {
		return nil, &IgnoreFileSizeError{Path: path, Limit: limit}
	} else if err != io.EOF {
		return nil, err
	}
	return data, nil
}
