package protocol

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"unicode/utf8"

	"github.com/linguo2625469/workbuddy2api-panel/internal/jsondoc"
)

// objectPrefix accepts complete objects or syntactically valid unfinished object
// prefixes. It never repairs JSON. A cutoff is not permission to accept duplicate
// keys, a scalar/array root, excessive nesting, or already-invalid syntax.
func objectPrefix(raw string) error {
	if !utf8.ValidString(raw) {
		return fmt.Errorf("invalid UTF-8 tool arguments")
	}
	trimmed := strings.TrimLeft(raw, " \t\r\n")
	if trimmed == "" {
		return nil // observed empty argument string, not invented missing arguments
	}
	if trimmed[0] != '{' {
		return fmt.Errorf("upstream tool input must be a JSON object prefix")
	}
	type frame struct {
		object bool
		key    bool
		seen   map[string]bool
	}
	d := json.NewDecoder(strings.NewReader(raw))
	d.UseNumber()
	var stack []frame
	complete := false
	for {
		tok, err := d.Token()
		if err != nil {
			if err == io.EOF || !complete && errors.Is(err, io.ErrUnexpectedEOF) {
				return nil
			}
			return fmt.Errorf("invalid upstream tool argument prefix: %w", err)
		}
		if complete {
			return fmt.Errorf("multiple upstream tool argument values")
		}
		if delim, ok := tok.(json.Delim); ok && (delim == '}' || delim == ']') {
			stack = stack[:len(stack)-1] // Decoder.Token already checked matching delimiters.
			complete = len(stack) == 0
			continue
		}
		if len(stack) > 0 {
			f := &stack[len(stack)-1]
			if f.object && f.key {
				key, ok := tok.(string)
				if !ok || f.seen[key] {
					return fmt.Errorf("invalid or duplicate upstream tool argument key")
				}
				f.seen[key], f.key = true, false
				continue
			}
			f.key = f.object
		}
		if len(stack) > jsondoc.MaxDepth {
			return fmt.Errorf("upstream tool argument nesting exceeds %d", jsondoc.MaxDepth)
		}
		if delim, ok := tok.(json.Delim); ok {
			f := frame{object: delim == '{', key: delim == '{'}
			if f.object {
				f.seen = map[string]bool{}
			}
			stack = append(stack, f)
		}
	}
}
