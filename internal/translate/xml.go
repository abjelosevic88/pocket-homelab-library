package translate

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
	"unicode"
)

// node is an element of a document as byte ranges into its source, so a
// translation replaces exactly the bytes of one element and leaves the rest
// of the file as it was.
type node struct {
	name       string // local name
	attrs      map[string]string
	start, end int64 // the whole element
	innerStart int64 // after the start tag
	innerEnd   int64 // before the end tag
	children   []*node
	text       int // non-space characters in all of its text
	own        int // non-space characters directly inside it
}

func (n *node) size() int64 { return n.end - n.start }

func nonSpace(s []byte) int {
	n := 0
	for _, r := range string(s) {
		if !unicode.IsSpace(r) {
			n++
		}
	}
	return n
}

func newDecoder(data []byte, strict, html bool) *xml.Decoder {
	d := xml.NewDecoder(bytes.NewReader(data))
	d.Strict = strict
	d.Entity = xml.HTMLEntity
	if html {
		d.AutoClose = xml.HTMLAutoClose
	}
	// EPUB text is UTF-8 in practice; a different declaration is read as is.
	d.CharsetReader = func(_ string, r io.Reader) (io.Reader, error) { return r, nil }
	return d
}

// parse reads a document into a tree of element spans. Book files are not
// always well-formed: an unknown entity is fine, and a document that does
// not parse as XML is read again with HTML's empty elements (<br>, <img>).
func parse(data []byte) (*node, error) {
	root, err := parseWith(data, false)
	if err != nil {
		if r2, err2 := parseWith(data, true); err2 == nil {
			return r2, nil
		}
	}
	return root, err
}

func parseWith(data []byte, html bool) (*node, error) {
	d := newDecoder(data, false, html)
	root := &node{name: "#document", end: int64(len(data))}
	stack := []*node{root}
	for {
		at := d.InputOffset()
		tok, err := d.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return root, err
		}
		top := stack[len(stack)-1]
		switch t := tok.(type) {
		case xml.StartElement:
			n := &node{name: t.Name.Local, start: at, innerStart: d.InputOffset(), attrs: map[string]string{}}
			for _, a := range t.Attr {
				n.attrs[a.Name.Local] = a.Value
			}
			top.children = append(top.children, n)
			stack = append(stack, n)
		case xml.EndElement:
			if len(stack) == 1 {
				continue
			}
			top.innerEnd, top.end = at, d.InputOffset()
			if top.innerEnd < top.innerStart { // <br/>: the end comes with the start
				top.innerEnd = top.innerStart
			}
			stack = stack[:len(stack)-1]
			stack[len(stack)-1].text += top.text
		case xml.CharData:
			c := nonSpace(t)
			top.text += c
			top.own += c
		}
	}
	return root, nil
}

// find returns every element called name, in document order.
func (n *node) find(name string) []*node {
	var out []*node
	var walk func(*node)
	walk = func(x *node) {
		for _, c := range x.children {
			if c.name == name {
				out = append(out, c)
			}
			walk(c)
		}
	}
	walk(n)
	return out
}

var (
	skipTags      = map[string]bool{"script": true, "style": true, "svg": true, "math": true, "img": true, "video": true, "audio": true}
	containerTags = map[string]bool{"div": true, "section": true, "article": true, "blockquote": true, "aside": true, "header": true, "footer": true,
		"main": true, "nav": true, "ol": true, "ul": true, "table": true, "tbody": true, "thead": true, "figure": true, "dl": true, "body": true}
)

// blocks collects the block-level elements with text under parent; a
// container that is too big for one request is split into its children.
func blocks(parent *node, maxChunk int64, out []*node) []*node {
	for _, c := range parent.children {
		if skipTags[c.name] || c.text == 0 {
			continue
		}
		if c.size() > maxChunk && containerTags[c.name] && len(c.children) > 0 && c.own == 0 {
			out = blocks(c, maxChunk, out)
			continue
		}
		out = append(out, c)
	}
	return out
}

// splice replaces byte ranges of data; edits must not overlap.
type edit struct {
	start, end int64
	with       []byte
}

func splice(data []byte, edits []edit) []byte {
	sortEdits(edits)
	var b bytes.Buffer
	b.Grow(len(data) + len(data)/4)
	at := int64(0)
	for _, e := range edits {
		if e.start < at {
			continue // overlapping: keep the first
		}
		b.Write(data[at:e.start])
		b.Write(e.with)
		at = e.end
	}
	b.Write(data[at:])
	return b.Bytes()
}

func sortEdits(e []edit) {
	for i := 1; i < len(e); i++ {
		for j := i; j > 0 && e[j].start < e[j-1].start; j-- {
			e[j], e[j-1] = e[j-1], e[j]
		}
	}
}

var (
	fenceRe   = regexp.MustCompile("(?s)^```[A-Za-z0-9_-]*[ \t]*\r?\n(.*?)\r?\n```\\s*$")
	xmlDeclRe = regexp.MustCompile(`^<\?xml[^>]*\?>\s*`)
	doctypeRe = regexp.MustCompile(`(?i)^<!DOCTYPE[^>]*>\s*`)
)

// cleanReply strips what a model wraps around markup: code fences, an XML
// declaration, a DOCTYPE.
func cleanReply(s string) string {
	s = strings.TrimSpace(s)
	if m := fenceRe.FindStringSubmatch(s); m != nil {
		s = m[1]
	}
	s = xmlDeclRe.ReplaceAllString(s, "")
	s = doctypeRe.ReplaceAllString(s, "")
	return strings.TrimSpace(s)
}

// fragments splits a reply into its top-level elements as raw markup. The
// reply must be well-formed, have exactly the expected number of elements
// and keep their names in order; text between them (a model's chatter) is
// dropped.
func fragments(reply string, want []string) ([][]byte, error) {
	const open, close = "<pocket-root>", "</pocket-root>"
	body := cleanReply(reply)
	wrapped := []byte(open + body + close)
	d := newDecoder(wrapped, true, false)
	var out [][]byte
	depth := 0
	var start int64
	var name string
	for {
		at := d.InputOffset()
		tok, err := d.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("not well-formed: %w", err)
		}
		switch t := tok.(type) {
		case xml.StartElement:
			depth++
			if depth == 2 {
				start, name = at, t.Name.Local
			}
		case xml.EndElement:
			if depth == 2 {
				i := len(out)
				if i < len(want) && want[i] != name {
					return nil, fmt.Errorf("element %d is <%s>, expected <%s>", i+1, name, want[i])
				}
				out = append(out, append([]byte(nil), wrapped[start:d.InputOffset()]...))
			}
			depth--
		}
	}
	if len(out) != len(want) {
		return nil, fmt.Errorf("expected %d top-level elements, got %d", len(want), len(out))
	}
	return out, nil
}

// escapeText escapes s for XML character data.
func escapeText(s string) string {
	var b strings.Builder
	_ = xml.EscapeText(&b, []byte(s))
	return b.String()
}

// innerText is an element's text content with markup removed and entities read.
func innerText(data []byte, n *node) string {
	d := newDecoder(data[n.innerStart:n.innerEnd], false, false)
	var b strings.Builder
	for {
		tok, err := d.Token()
		if err != nil {
			break
		}
		if c, ok := tok.(xml.CharData); ok {
			b.Write(c)
		}
	}
	return strings.TrimSpace(b.String())
}

var errNoText = errors.New("no translatable text found in the book")
