package lsp

import (
	"fmt"
	"net/url"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/TeddyGavi/bem-ls/internal/core"
	"github.com/TeddyGavi/bem-ls/internal/lsp/protocol"
)

// uriToPath converts a file:// URI to a local path.
func uriToPath(uri protocol.DocumentURI) (string, error) {
	u, err := url.Parse(string(uri))
	if err != nil {
		return "", err
	}
	if u.Scheme != "file" {
		return "", fmt.Errorf("unsupported URI scheme %q", u.Scheme)
	}
	p := u.Path
	if runtime.GOOS == "windows" {
		// file:///C:/x has Path "/C:/x".
		if len(p) >= 3 && p[0] == '/' && p[2] == ':' {
			p = p[1:]
		}
		if u.Host != "" {
			p = `\\` + u.Host + p // UNC path
		}
	}
	return filepath.Clean(filepath.FromSlash(p)), nil
}

// pathToURI converts a local path to a file:// URI.
func pathToURI(path string) protocol.DocumentURI {
	p := filepath.ToSlash(path)
	if !strings.HasPrefix(p, "/") {
		p = "/" + p // C:/x
	}
	return protocol.DocumentURI((&url.URL{Scheme: "file", Path: p}).String())
}

// mapper converts between byte offsets and LSP positions for one text.
type mapper struct {
	text  string
	lines []int // byte offset of each line start
	utf8  bool  // Character counts bytes rather than UTF-16 units
}

func newMapper(text, encoding string) *mapper {
	m := &mapper{text: text, lines: []int{0}, utf8: encoding == protocol.EncodingUTF8}
	for i := 0; i < len(text); i++ {
		if text[i] == '\n' {
			m.lines = append(m.lines, i+1)
		}
	}
	return m
}

// offset converts a position to a byte offset, clamping to the text.
func (m *mapper) offset(p protocol.Position) int {
	if int(p.Line) >= len(m.lines) {
		return len(m.text)
	}
	start := m.lines[p.Line]
	end := len(m.text)
	if int(p.Line)+1 < len(m.lines) {
		end = m.lines[p.Line+1] - 1 // before '\n'
	}
	if m.utf8 {
		off := start + int(p.Character)
		if off > end {
			off = end
		}
		return off
	}
	units := 0
	for i := start; i < end; {
		if units >= int(p.Character) {
			return i
		}
		r, w := utf8.DecodeRuneInString(m.text[i:])
		if r >= 0x10000 {
			units += 2
		} else {
			units++
		}
		i += w
	}
	return end
}

// position converts a byte offset to a position.
func (m *mapper) position(off int) protocol.Position {
	if off < 0 {
		off = 0
	}
	if off > len(m.text) {
		off = len(m.text)
	}
	line := sort.Search(len(m.lines), func(i int) bool { return m.lines[i] > off }) - 1
	start := m.lines[line]
	if m.utf8 {
		return protocol.Position{Line: uint32(line), Character: uint32(off - start)}
	}
	units := 0
	for _, r := range m.text[start:off] {
		if r >= 0x10000 {
			units += 2
		} else {
			units++
		}
	}
	return protocol.Position{Line: uint32(line), Character: uint32(units)}
}

func (m *mapper) rangeOf(r core.Range) protocol.Range {
	return protocol.Range{Start: m.position(r.Start.Offset), End: m.position(r.End.Offset)}
}

func (m *mapper) rangeOfOffsets(start, end int) protocol.Range {
	return protocol.Range{Start: m.position(start), End: m.position(end)}
}

// applyChanges applies LSP content changes to text.
func applyChanges(text string, changes []protocol.TextDocumentContentChangeEvent, encoding string) string {
	for _, c := range changes {
		if c.Range == nil {
			text = c.Text
			continue
		}
		m := newMapper(text, encoding)
		s, e := m.offset(c.Range.Start), m.offset(c.Range.End)
		if e < s {
			s, e = e, s
		}
		text = text[:s] + c.Text + text[e:]
	}
	return text
}

// lineIndent returns the leading whitespace of the line containing off.
func lineIndent(text string, off int) string {
	start := strings.LastIndexByte(text[:off], '\n') + 1
	end := start
	for end < len(text) && (text[end] == ' ' || text[end] == '\t') {
		end++
	}
	return text[start:end]
}

// indentUnit guesses one level of indentation from the text: the indent of
// the first indented line, or two spaces.
func indentUnit(text string) string {
	for _, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimLeft(line, " \t")
		if trimmed != "" && len(trimmed) < len(line) {
			ind := line[:len(line)-len(trimmed)]
			if strings.HasPrefix(ind, "\t") {
				return "\t"
			}
			if len(ind) >= 2 && len(ind) <= 8 {
				return ind
			}
		}
	}
	return "  "
}
