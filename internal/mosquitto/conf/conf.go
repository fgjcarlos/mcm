// Package conf parses Mosquitto configuration files losslessly, preserving
// comments and unknown directives byte-for-byte. This is the foundation for
// the versioned configuration model described in issue #298.
//
// The parser deliberately tracks the exact textual span of every directive
// and comment so Render() reproduces the original bytes except where the
// caller has replaced specific directives. The validation layer piggybacks
// on the same AST to enforce the version-aware directive catalog.
package conf

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"strings"
)

// ErrUnknownDirective is wrapped in validation issues when a directive
// appears that the catalog does not recognise. Unknown directives are NOT
// parser errors — the importer must preserve them byte-for-byte so future
// Mosquitto versions can be supported without a schema bump.
var ErrUnknownDirective = errors.New("unknown directive")

// ItemKind discriminates between comments, directives, and the synthetic
// markers used to track the opening/closing brace of a block.
type ItemKind int

const (
	ItemComment ItemKind = iota
	ItemDirective
	ItemBlockOpen
	ItemBlockClose
)

// Item is one parsed node in the configuration file. For ItemDirective the
// Key is the directive name (lowercase, post-normalisation), Values are the
// remaining tokens (blank-trimmed, original case preserved per token so
// quoted strings survive).
//
// For ItemComment the Text field is the raw line including the leading "#"
// (and its trailing newline if present).
//
// For ItemBlockOpen / ItemBlockClose the Key is the directive that opened
// the block (e.g. "listener") and Indent is the indentation depth used to
// match the closing brace. Values is empty.
type Item struct {
	Kind   ItemKind
	Key    string
	Values []string
	Text   string
	Line   int // 1-indexed source line; -1 when synthesised
	Indent int // column at which the token begins (0 = no leading space)
}

// File is an ordered list of Items plus the source path. The order is the
// textual order in the original file — the importer preserves it through
// Render() so a round-trip is byte-exact.
type File struct {
	Items []Item
	Path  string
}

// ListenerBlock returns the (Key, Values, bodyItems) for each block that
// the catalog recognises as having a {…} body. Today only "listener" is
// block-shaped; future catalog entries can opt-in via the Block flag.
func (f *File) ListenerBlocks() []Block {
	var out []Block
	for i := range f.Items {
		it := &f.Items[i]
		if it.Kind != ItemBlockOpen {
			continue
		}
		end := matchingBlockClose(f.Items, i)
		if end < 0 {
			// Malformed file — validator surfaces it.
			continue
		}
		out = append(out, Block{Header: it, Body: f.Items[i+1 : end], Close: f.Items[end]})
	}
	return out
}

// Block is one listener-style block: the opening directive, the inner
// items, and the closing brace.
type Block struct {
	Header *Item
	Body   []Item
	Close  Item
}

// Parse reads r and produces a *File. The parser is lenient about
// blank lines and trailing whitespace. It DOES NOT perform any catalog
// validation — callers that want validation should run Validate(file, cat)
// explicitly. The path argument is informational (stored on File.Path).
func Parse(r io.Reader, path string) (*File, error) {
	br := bufio.NewReader(r)
	scanner := bufio.NewScanner(br)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	f := &File{Path: path}
	var lineNo int
	var blockDepth int
	var inBlock bool
	for scanner.Scan() {
		lineNo++
		raw := scanner.Text()
		// Drop the trailing \r that some Windows-broken-but-not-Windows
		// mount layers add — the parser does not care about line
		// endings, only about content.
		raw = strings.TrimRight(raw, "\r")

		trimmed := strings.TrimSpace(raw)
		indent := len(raw) - len(strings.TrimLeft(raw, " \t"))

		switch {
		case trimmed == "":
			// Preserve blank lines as comments so the round-trip is
			// exact. They never carry semantic meaning.
			f.Items = append(f.Items, Item{Kind: ItemComment, Text: raw + "\n", Line: lineNo, Indent: indent})

		case strings.HasPrefix(trimmed, "#"):
			// Comment line, preserved verbatim.
			f.Items = append(f.Items, Item{Kind: ItemComment, Text: raw + "\n", Line: lineNo, Indent: indent})

		case inBlock:
			if isCloseBrace(trimmed) {
				f.Items = append(f.Items, Item{Kind: ItemBlockClose, Text: raw + "\n", Line: lineNo, Indent: indent})
				inBlock = false
				blockDepth--
				continue
			}
			if it, ok := parseDirectiveLine(raw, lineNo, indent, true); ok {
				f.Items = append(f.Items, it)
			} else {
				// Unknown / unparseable inside block — treat as comment so we don't drop data.
				f.Items = append(f.Items, Item{Kind: ItemComment, Text: raw + "\n", Line: lineNo, Indent: indent})
			}

		default:
			fields := strings.Fields(raw)
			if len(fields) == 0 {
				continue
			}
			dir := strings.ToLower(fields[0])
			if isBlockDirective(dir) && isBlockStart(raw, fields) {
				// Block header — record as ItemBlockOpen + an empty
				// directive-style item holding the header args. The
				// body items follow.
				it, _ := parseDirectiveLine(raw, lineNo, indent, false)
				it.Kind = ItemBlockOpen
				f.Items = append(f.Items, it)
				inBlock = true
				blockDepth++
				continue
			}
			if it, ok := parseDirectiveLine(raw, lineNo, indent, false); ok {
				f.Items = append(f.Items, it)
			} else {
				f.Items = append(f.Items, Item{Kind: ItemComment, Text: raw + "\n", Line: lineNo, Indent: indent})
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read conf %q: %w", path, err)
	}
	if blockDepth != 0 {
		return nil, fmt.Errorf("conf %q: unclosed block at end of file (depth=%d)", path, blockDepth)
	}
	return f, nil
}

// ParseString is a convenience wrapper around Parse.
func ParseString(s, path string) (*File, error) {
	return Parse(strings.NewReader(s), path)
}

// blockDirectives is the set of top-level directives that introduce a
// brace-delimited body. Today Mosquitto only has "listener" and "bridge".
// Bridge blocks are rare in container deployments; we add the entry so
// unknown body shapes round-trip instead of corrupting the file.
var blockDirectives = map[string]struct{}{
	"listener": {},
	"bridge":   {},
}

func isBlockDirective(name string) bool {
	_, ok := blockDirectives[name]
	return ok
}

// isBlockStart reports whether a top-level directive line opens a
// braced block. Mosquitto accepts both the inline form
// (`listener 1883 {`) and the newline form (`listener 1883\n{`). We
// detect both.
func isBlockStart(raw string, fields []string) bool {
	if strings.HasSuffix(strings.TrimSpace(raw), "{") {
		return true
	}
	if len(fields) > 0 && fields[len(fields)-1] == "{" {
		return true
	}
	return false
}

// isCloseBrace reports whether the trimmed line is a `}` possibly
// followed by inline comment text. Mosquitto's grammar permits trailing
// comments on the closing brace line.
func isCloseBrace(trimmed string) bool {
	if trimmed == "}" {
		return true
	}
	if strings.HasPrefix(trimmed, "}") && (trimmed[1] == ' ' || trimmed[1] == '	' || trimmed[1] == '#') {
		return true
	}
	return false
}

// parseDirectiveLine splits a directive line into tokens and decides whether
// the line is a directive at all (returns ok=false for things like stray
// continuation lines that the parser cannot classify).
func parseDirectiveLine(raw string, lineNo, indent int, allowEmpty bool) (Item, bool) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return Item{}, allowEmpty
	}
	if strings.HasPrefix(trimmed, "#") {
		return Item{}, false
	}
	// Tokenise with quote awareness so `set_tls_version "tlsv1.2"` keeps
	// the quoted arg as one value.
	values := tokenise(trimmed)
	if len(values) == 0 {
		return Item{}, false
	}
	key := strings.ToLower(values[0])
	rest := make([]string, len(values)-1)
	copy(rest, values[1:])
	return Item{
		Kind:   ItemDirective,
		Key:    key,
		Values: rest,
		Text:   raw + "\n",
		Line:   lineNo,
		Indent: indent,
	}, true
}

// tokenise splits a directive line into whitespace-separated tokens,
// keeping balanced double-quoted strings as one token. The Mosquitto
// configuration syntax supports double quotes; we do not handle escapes
// because Mosquitto itself does not (a " inside a quoted token is the
// closing quote, per mosquitto_conf.c).
func tokenise(s string) []string {
	var out []string
	var cur strings.Builder
	inQuote := false
	flush := func() {
		if cur.Len() > 0 {
			out = append(out, cur.String())
			cur.Reset()
		}
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '"':
			inQuote = !inQuote
			cur.WriteByte(c)
		case (c == ' ' || c == '\t') && !inQuote:
			flush()
		default:
			cur.WriteByte(c)
		}
	}
	flush()
	return out
}

// matchingBlockClose returns the index of the ItemBlockClose that pairs
// with the ItemBlockOpen at index openIdx, or -1 if the file is malformed.
func matchingBlockClose(items []Item, openIdx int) int {
	depth := 0
	for i := openIdx; i < len(items); i++ {
		switch items[i].Kind {
		case ItemBlockOpen:
			depth++
		case ItemBlockClose:
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return -1
}
