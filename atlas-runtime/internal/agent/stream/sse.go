package stream

import (
	"bufio"
	"io"
	"strings"
)

// Scanner wraps a bufio.Scanner to iterate over SSE data lines from an
// io.Reader. Lines that do not start with "data: " are skipped automatically.
// Call Next() until it returns false, then check Err().
type Scanner struct {
	sc   *bufio.Scanner
	line string
	err  error
}

// NewScanner returns a Scanner reading from r with a 1 MB line buffer.
func NewScanner(r io.Reader) *Scanner {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	return &Scanner{sc: sc}
}

// Next advances to the next SSE data line. Returns false when the stream ends
// or the sentinel "[DONE]" is reached.
func (s *Scanner) Next() bool {
	for s.sc.Scan() {
		line := s.sc.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := strings.TrimPrefix(line, "data: ")
		if data == "[DONE]" {
			return false
		}
		s.line = data
		return true
	}
	s.err = s.sc.Err()
	return false
}

// Line returns the current data payload (the part after "data: ").
func (s *Scanner) Line() string { return s.line }

// Err returns any scanner error encountered after Next returns false.
func (s *Scanner) Err() error { return s.err }
