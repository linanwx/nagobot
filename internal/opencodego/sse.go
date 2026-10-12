package opencodego

import (
	"bufio"
	"fmt"
	"io"
	"strings"
)

// readSSE ignores heartbeat comments and empty events. Payloads are joined
// according to SSE's multiline data rule; JSON validity is the protocol's job.
func readSSE(r io.Reader, event func(string, []byte) (bool, error)) error {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 4096), 16<<20)
	var kind string
	var data []string
	dispatch := func() (bool, error) {
		payload := strings.Join(data, "\n")
		name := kind
		kind, data = "", nil
		if strings.TrimSpace(payload) == "" {
			return false, nil
		}
		return event(name, []byte(payload))
	}
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			done, err := dispatch()
			if err != nil {
				return err
			}
			if done {
				return nil
			}
			continue
		}
		if strings.HasPrefix(line, ":") {
			continue
		}
		field, value, _ := strings.Cut(line, ":")
		value = strings.TrimPrefix(value, " ")
		switch field {
		case "event":
			kind = value
		case "data":
			data = append(data, value)
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("read OpenCode Go stream: %w", err)
	}
	if len(data) > 0 {
		done, err := dispatch()
		if err != nil {
			return err
		}
		if done {
			return nil
		}
	}
	return fmt.Errorf("OpenCode Go stream truncated before terminal event: %w", io.ErrUnexpectedEOF)
}
