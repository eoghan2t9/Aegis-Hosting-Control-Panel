package svc

import (
	"os"
	"strings"
)

// MaxLogTailBytes bounds how much of a log file one tail request may read.
// Access logs rotate, so the current file is bounded in practice; the cap
// keeps a runaway log from ballooning an API response.
const MaxLogTailBytes = 512 * 1024

// TailLines returns the last n lines of file. The read is capped at
// MaxLogTailBytes from the end of the file; when the cap truncates mid-line
// the first returned line is the remainder of that (partial) line.
func TailLines(file string, n int) ([]string, error) {
	if n <= 0 {
		n = 200
	}
	if n > 5000 {
		n = 5000
	}
	f, err := os.Open(file)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	st, err := f.Stat()
	if err != nil {
		return nil, err
	}
	size := st.Size()
	if size == 0 {
		return []string{}, nil
	}
	read := int64(MaxLogTailBytes)
	if size < read {
		read = size
	}
	buf := make([]byte, read)
	if _, err := f.ReadAt(buf, size-read); err != nil {
		return nil, err
	}
	// Drop a partial first line when we didn't read from the start.
	text := string(buf)
	if read < size {
		if i := strings.IndexByte(text, '\n'); i >= 0 {
			text = text[i+1:]
		}
	}
	lines := strings.Split(strings.TrimSuffix(text, "\n"), "\n")
	// Reverse-filter empties, keep the last n.
	out := make([]string, 0, n)
	for _, l := range lines {
		if l != "" {
			out = append(out, l)
		}
	}
	if len(out) > n {
		out = out[len(out)-n:]
	}
	if out == nil {
		out = []string{}
	}
	return out, nil
}
