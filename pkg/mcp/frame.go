package mcp

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// MaxLineBytes bounds a single newline-delimited JSON-RPC message. MCP stdio is
// line-framed and tool results can be multi-MB, so framing must not use the
// default bufio.Scanner 64KB cap; this is the hard ceiling instead.
const MaxLineBytes = 16 * 1024 * 1024

// ReadMessage reads one newline-terminated JSON-RPC message from r and returns
// it without the trailing newline. It grows past bufio's internal buffer for
// long lines (up to MaxLineBytes) so large payloads are not truncated. The
// returned bytes and err follow io.Reader conventions: a non-empty line may be
// returned together with io.EOF on the final unterminated message.
func ReadMessage(r *bufio.Reader) ([]byte, error) {
	var buf []byte
	for {
		chunk, err := r.ReadSlice('\n')
		if len(buf)+len(chunk) > MaxLineBytes {
			return nil, fmt.Errorf("message exceeds %d bytes", MaxLineBytes)
		}
		buf = append(buf, chunk...)
		if errors.Is(err, bufio.ErrBufferFull) {
			continue
		}
		if err != nil {
			return trimNewline(buf), err
		}
		return trimNewline(buf), nil
	}
}

// WriteMessage marshals v and writes it as one framed line (payload + '\n') to
// w. Callers that share w across goroutines must serialize WriteMessage
// themselves.
func WriteMessage(w io.Writer, v any) error {
	payload, err := json.Marshal(v)
	if err != nil {
		return err
	}
	if _, err := w.Write(payload); err != nil {
		return err
	}
	_, err = w.Write([]byte{'\n'})
	return err
}

// trimNewline drops a single trailing "\n" or "\r\n" from a line.
func trimNewline(b []byte) []byte {
	if n := len(b); n > 0 && b[n-1] == '\n' {
		b = b[:n-1]
		if n = len(b); n > 0 && b[n-1] == '\r' {
			b = b[:n-1]
		}
	}
	return b
}
