package conf

import (
	"bytes"
	"fmt"
)

// Render writes the file back to its byte form. The round-trip is
// byte-exact when the caller does NOT mutate Items.Text — the original
// "key value…" lines are kept as-is. Replacing a directive is the
// caller's job: the parser records the raw text per directive, and the
// renderer falls back to it whenever the directive is not annotated as
// replaced.
//
// A caller can replace a directive by zeroing its Text field and setting
// Values + Key explicitly; Render then writes "key value1 value2 …\n"
// using the recorded Indent. This is the mechanism the diff/apply paths
// use to substitute the operator's edits.
//
// Block items (ItemBlockOpen / ItemBlockClose) are emitted via their
// header's Text + a closing "}\n" drawn from the Close item — they are
// NEVER substituted automatically because a block's body must round-trip
// losslessly and the catalog never rewrites inner items.
func (f *File) Render() ([]byte, error) {
	var buf bytes.Buffer
	for i, it := range f.Items {
		switch it.Kind {
		case ItemComment:
			if _, err := buf.WriteString(it.Text); err != nil {
				return nil, fmt.Errorf("render comment line %d: %w", it.Line, err)
			}

		case ItemDirective:
			line, err := renderDirectiveLine(it)
			if err != nil {
				return nil, err
			}
			if _, err := buf.Write(line); err != nil {
				return nil, fmt.Errorf("render directive line %d: %w", it.Line, err)
			}

		case ItemBlockOpen:
			// Header line exactly as parsed.
			buf.WriteString(it.Text)

		case ItemBlockClose:
			// Close brace: indentation + "}" + "\n".
			if it.Text == "" {
				for i := 0; i < it.Indent; i++ {
					buf.WriteByte(' ')
				}
				buf.WriteByte('}')
				buf.WriteString(itemTerminator(it))
			} else {
				buf.WriteString(it.Text)
			}
		}
		// If another item follows a source line that had no terminator,
		// separate the lines so appended directives remain distinct. The
		// final source line still round-trips without a newline when it is
		// the last item.
		if i < len(f.Items)-1 && buf.Len() > 0 {
			written := buf.Bytes()
			last := written[len(written)-1]
			if last != '\n' && last != '\r' {
				terminator := it.Terminator
				if terminator == "" {
					terminator = "\n"
				}
				buf.WriteString(terminator)
			}
		}
	}
	return buf.Bytes(), nil
}

// renderDirectiveLine returns the bytes for one directive. When the
// parser-recorded Text is non-empty we use it verbatim (round-trip); when
// the caller cleared Text (because they mutated the directive) we
// rebuild the line from Key + Values.
func renderDirectiveLine(it Item) ([]byte, error) {
	if it.Text != "" {
		return []byte(it.Text), nil
	}
	if it.Key == "" {
		return nil, fmt.Errorf("render: directive with empty key at line %d", it.Line)
	}
	var buf bytes.Buffer
	for i := 0; i < it.Indent; i++ {
		buf.WriteByte(' ')
	}
	buf.WriteString(it.Key)
	for _, v := range it.Values {
		buf.WriteByte(' ')
		buf.WriteString(v)
	}
	buf.WriteString(itemTerminator(it))
	return buf.Bytes(), nil
}

func itemTerminator(it Item) string {
	if it.Terminator != "" {
		return it.Terminator
	}
	if it.Line < 0 {
		return "\n"
	}
	return ""
}
