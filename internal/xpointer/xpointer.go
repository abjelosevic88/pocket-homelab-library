// Package xpointer converts between KOReader's (CREngine's) XPointers, such
// as /body/DocFragment[12]/body/div/p[7]/text().123, and a position the web
// reader understands: the spine item and how far into its text the point is.
//
// The fraction counts the Unicode code points of the text nodes of the spine
// item's <body> in document order, with every run of whitespace counted as
// one space, before the point, divided by the total. The web reader computes
// it the same way.
//
// CREngine numbers spine items as DocFragment[spine+1] (every spine item
// since 2024, not only XHTML ones), writes element steps as name[k] where k
// counts same-named siblings (newer versions always, older ones only when
// there is more than one), and text()[k] over text siblings, with the offset
// counted in the text after whitespace collapsing. Pointers built here always
// carry the explicit index, which every version and CrossPoint accept.
package xpointer

import (
	"archive/zip"
	"encoding/xml"
	"errors"
	"fmt"
	"html"
	"io"
	"math"
	"os"
	"path"
	"strconv"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"
)

// ErrNotFound is returned when a pointer does not resolve in the book.
var ErrNotFound = errors.New("xpointer: not found")

// Resolve finds where an XPointer points in an EPUB: the spine index
// (0-based) and the fraction (0..1) of that item's text before it.
func Resolve(epubPath, xp string) (spine int, fraction float64, err error) {
	b, err := load(epubPath)
	if err != nil {
		return 0, 0, err
	}
	frag, steps, err := parse(xp)
	if err != nil {
		return 0, 0, err
	}
	spine = frag - 1
	if spine < 0 || spine >= len(b.items) {
		return 0, 0, ErrNotFound
	}
	it := b.items[spine]
	if it.total == 0 {
		return spine, 0, nil
	}
	return spine, clamp01(float64(it.resolve(steps)) / float64(it.total)), nil
}

// Build makes the XPointer of the point a fraction into a spine item's text.
func Build(epubPath string, spine int, fraction float64) (string, error) {
	b, err := load(epubPath)
	if err != nil {
		return "", err
	}
	if spine < 0 || spine >= len(b.items) {
		return "", ErrNotFound
	}
	base := fmt.Sprintf("/body/DocFragment[%d]/body", spine+1)
	it := b.items[spine]
	fraction = clamp01(fraction)
	if fraction <= 0 || it.total == 0 {
		return base, nil
	}
	target := int(math.Ceil(fraction * float64(it.total)))
	target = max(0, min(it.total-1, target))
	var last *textNode
	for i := range it.nodes {
		n := &it.nodes[i]
		if n.ws || n.textIdx == 0 {
			continue
		}
		last = n
		if target < n.start+n.length {
			return base + n.pointer(max(0, target-n.start)), nil
		}
	}
	if last != nil {
		return base + last.pointer(last.length), nil
	}
	return base, nil
}

// Overall returns the whole-book percentage of a spine position, weighting
// spine items by their text length (as KOReader roughly does).
func Overall(epubPath string, spine int, fraction float64) (float64, error) {
	b, err := load(epubPath)
	if err != nil {
		return 0, err
	}
	if spine < 0 || spine >= len(b.items) {
		return 0, ErrNotFound
	}
	if b.total == 0 {
		return clamp01((float64(spine) + clamp01(fraction)) / float64(len(b.items))), nil
	}
	before := 0
	for i := 0; i < spine; i++ {
		before += b.items[i].total
	}
	return clamp01((float64(before) + clamp01(fraction)*float64(b.items[spine].total)) / float64(b.total)), nil
}

// Locate is the reverse of Overall: the spine item and fraction at a
// whole-book percentage.
func Locate(epubPath string, percentage float64) (spine int, fraction float64, err error) {
	b, err := load(epubPath)
	if err != nil {
		return 0, 0, err
	}
	percentage = clamp01(percentage)
	if b.total == 0 {
		pos := percentage * float64(len(b.items))
		i := min(len(b.items)-1, int(pos))
		return i, clamp01(pos - float64(i)), nil
	}
	target := percentage * float64(b.total)
	acc := 0.0
	for i, it := range b.items {
		if it.total > 0 && target <= acc+float64(it.total) {
			return i, clamp01((target - acc) / float64(it.total)), nil
		}
		acc += float64(it.total)
	}
	return len(b.items) - 1, 1, nil
}

func clamp01(f float64) float64 {
	if math.IsNaN(f) || f < 0 {
		return 0
	}
	if f > 1 {
		return 1
	}
	return f
}

// ---- pointer strings ----

type step struct {
	name  string // element name or "text()"
	index int    // 1-based; 0 = not given (the first)
	point int    // the .N after the last step, -1 = none
}

// parse splits "/body/DocFragment[N]/body/…/text()[k].off" into the fragment
// number and the steps below the fragment's body.
func parse(xp string) (int, []step, error) {
	xp = strings.TrimSpace(xp)
	if !strings.HasPrefix(xp, "/") {
		return 0, nil, ErrNotFound
	}
	point := -1
	if i := strings.LastIndexByte(xp, '.'); i > strings.LastIndexByte(xp, '/') {
		if n, err := strconv.Atoi(xp[i+1:]); err == nil && n >= 0 {
			point = n
			xp = xp[:i]
		}
	}
	var steps []step
	for _, part := range strings.Split(strings.Trim(xp, "/"), "/") {
		if part == "" {
			continue
		}
		name, idx := part, 0
		if i := strings.IndexByte(part, '['); i > 0 && strings.HasSuffix(part, "]") {
			name = part[:i]
			n, err := strconv.Atoi(part[i+1 : len(part)-1])
			if err != nil || n < 1 {
				return 0, nil, ErrNotFound
			}
			idx = n
		}
		steps = append(steps, step{name: strings.ToLower(name), index: idx, point: -1})
	}
	if len(steps) < 2 || steps[0].name != "body" || steps[1].name != "docfragment" {
		return 0, nil, ErrNotFound
	}
	frag := max(steps[1].index, 1)
	rest := steps[2:]
	if len(rest) > 0 && rest[0].name == "body" {
		rest = rest[1:]
	}
	if len(rest) > 0 {
		rest[len(rest)-1].point = point
	}
	return frag, rest, nil
}

// ---- the book's text ----

type seg struct {
	name  string
	index int
}

// textNode is one text node of a spine item's body as CREngine sees it.
type textNode struct {
	path    []seg // elements from below <body> down to the node's parent
	key     string
	textIdx int  // text()[k] among the parent's text children, 0 = dropped by CREngine
	start   int  // collapsed characters before it
	length  int  // its collapsed characters
	ws      bool // whitespace only
}

func (n *textNode) pointer(offset int) string {
	var sb strings.Builder
	for _, s := range n.path {
		fmt.Fprintf(&sb, "/%s[%d]", s.name, s.index)
	}
	fmt.Fprintf(&sb, "/text()[%d].%d", n.textIdx, offset)
	return sb.String()
}

type item struct {
	nodes []textNode
	elems map[string]int // path key → collapsed characters before the element
	total int
}

type book struct {
	items []item
	total int
}

func pathKey(p []seg) string {
	var sb strings.Builder
	for _, s := range p {
		sb.WriteString(s.name)
		sb.WriteByte('[')
		sb.WriteString(strconv.Itoa(s.index))
		sb.WriteString("]/")
	}
	return sb.String()
}

// resolve turns the steps below <body> into collapsed characters before the point.
func (it *item) resolve(steps []step) int {
	var p []seg
	textIdx, point := 0, -1
	for _, s := range steps {
		if s.name == "text()" {
			textIdx, point = max(s.index, 1), s.point
			break
		}
		p = append(p, seg{name: s.name, index: max(s.index, 1)})
		point = s.point
	}
	// the longest prefix that exists: a pointer into a slightly different
	// rendition of the book still lands close
	for len(p) > 0 {
		if _, ok := it.elems[pathKey(p)]; ok {
			break
		}
		p, textIdx, point = p[:len(p)-1], 0, -1
	}
	key := pathKey(p)
	if textIdx > 0 {
		var before *textNode
		for i := range it.nodes {
			n := &it.nodes[i]
			if n.textIdx == 0 || n.key != key {
				continue
			}
			if n.textIdx == textIdx {
				return n.start + min(max(point, 0), n.length)
			}
			if n.textIdx < textIdx {
				before = n
			}
		}
		if before != nil {
			return before.start + before.length
		}
	}
	return it.elems[key]
}

// invisible elements' text is not counted (CREngine does not render it).
var invisible = map[string]bool{"head": true, "script": true, "style": true, "title": true, "noscript": true, "template": true}

// block containers: CREngine drops whitespace-only text directly inside them.
var blockContainer = map[string]bool{
	"body": true, "div": true, "section": true, "article": true, "aside": true, "nav": true, "header": true, "footer": true, "main": true,
	"blockquote": true, "ul": true, "ol": true, "dl": true, "table": true, "thead": true, "tbody": true, "tfoot": true, "tr": true,
	"figure": true, "html": true, "hgroup": true, "details": true, "fieldset": true, "form": true, "map": true, "svg": true,
}

type frame struct {
	name    string
	counts  map[string]int
	textN   int  // text children counted
	pending bool // the next character data starts a new text node
}

// parseItem reads one XHTML document and lists its body's text nodes.
func parseItem(r io.Reader) item {
	it := item{elems: map[string]int{"": 0}}
	dec := xml.NewDecoder(r)
	dec.Strict = false
	dec.AutoClose = xml.HTMLAutoClose
	dec.Entity = xml.HTMLEntity
	dec.CharsetReader = func(label string, in io.Reader) (io.Reader, error) { return in, nil }
	var stack []*frame // <body> and below
	invisibleDepth := 0
	var path []seg
	pos := 0
	lastSpace := true // a leading run counts as nothing
	cur := -1         // index of the open text node in it.nodes
	for {
		tok, err := dec.Token()
		if err != nil {
			break
		}
		switch t := tok.(type) {
		case xml.StartElement:
			name := strings.ToLower(t.Name.Local)
			cur = -1
			if len(stack) == 0 {
				if name == "body" {
					stack = []*frame{{name: "body", counts: map[string]int{}, pending: true}}
				}
				continue
			}
			parent := stack[len(stack)-1]
			parent.counts[name]++
			parent.pending = true
			path = append(path, seg{name: name, index: parent.counts[name]})
			stack = append(stack, &frame{name: name, counts: map[string]int{}, pending: true})
			if invisibleDepth > 0 || invisible[name] {
				invisibleDepth++
			}
			if _, ok := it.elems[pathKey(path)]; !ok {
				it.elems[pathKey(path)] = pos
			}
		case xml.EndElement:
			cur = -1
			if len(stack) == 0 {
				continue
			}
			if len(stack) == 1 {
				if strings.EqualFold(t.Name.Local, "body") {
					it.total = pos
					return it
				}
				continue
			}
			stack = stack[:len(stack)-1]
			path = path[:len(path)-1]
			stack[len(stack)-1].pending = true
			if invisibleDepth > 0 {
				invisibleDepth--
			}
		case xml.CharData:
			if len(stack) == 0 || invisibleDepth > 0 || len(t) == 0 {
				continue
			}
			parent := stack[len(stack)-1]
			s := string(t)
			ws := strings.TrimFunc(s, unicode.IsSpace) == ""
			if cur < 0 || parent.pending {
				parent.pending = false
				n := textNode{path: append([]seg(nil), path...), key: pathKey(path), start: pos, ws: ws}
				if !ws || !blockContainer[parent.name] {
					parent.textN++
					n.textIdx = parent.textN
				}
				it.nodes = append(it.nodes, n)
				cur = len(it.nodes) - 1
			} else if !ws {
				n := &it.nodes[cur]
				if n.textIdx == 0 {
					parent.textN++
					n.textIdx = parent.textN
				}
				n.ws = false
			}
			n := &it.nodes[cur]
			for _, r := range s {
				if unicode.IsSpace(r) {
					if lastSpace {
						continue
					}
					lastSpace = true
				} else {
					lastSpace = false
				}
				pos++
				n.length++
			}
		}
		// comments and processing instructions: CREngine drops them, the
		// text on both sides stays one node
	}
	it.total = pos
	return it
}

// ---- reading EPUBs (cached) ----

type cacheKey struct {
	path  string
	size  int64
	mtime int64
}

var (
	cacheMu sync.Mutex
	cache   = map[cacheKey]*book{}
	order   []cacheKey
)

const cacheSize = 8

func load(epubPath string) (*book, error) {
	fi, err := os.Stat(epubPath)
	if err != nil {
		return nil, err
	}
	k := cacheKey{epubPath, fi.Size(), fi.ModTime().UnixNano()}
	cacheMu.Lock()
	b, ok := cache[k]
	cacheMu.Unlock()
	if ok {
		return b, nil
	}
	b, err = read(epubPath)
	if err != nil {
		return nil, err
	}
	cacheMu.Lock()
	defer cacheMu.Unlock()
	if _, ok := cache[k]; !ok {
		cache[k] = b
		order = append(order, k)
		if len(order) > cacheSize {
			delete(cache, order[0])
			order = order[1:]
		}
	}
	return b, nil
}

func read(epubPath string) (*book, error) {
	zr, err := zip.OpenReader(epubPath)
	if err != nil {
		return nil, err
	}
	defer zr.Close()
	files := map[string]*zip.File{}
	for _, f := range zr.File {
		files[f.Name] = f
	}
	open := func(name string) (io.ReadCloser, error) {
		f, ok := files[name]
		if !ok {
			// some books differ in case
			for n, ff := range files {
				if strings.EqualFold(n, name) {
					f, ok = ff, true
					break
				}
			}
		}
		if !ok {
			return nil, ErrNotFound
		}
		return f.Open()
	}
	rc, err := open("META-INF/container.xml")
	if err != nil {
		return nil, fmt.Errorf("xpointer: no container.xml: %w", err)
	}
	var container struct {
		Rootfiles []struct {
			FullPath string `xml:"full-path,attr"`
		} `xml:"rootfiles>rootfile"`
	}
	err = xml.NewDecoder(rc).Decode(&container)
	rc.Close()
	if err != nil || len(container.Rootfiles) == 0 {
		return nil, errors.New("xpointer: bad container.xml")
	}
	opfPath := container.Rootfiles[0].FullPath
	rc, err = open(opfPath)
	if err != nil {
		return nil, fmt.Errorf("xpointer: no package document: %w", err)
	}
	var pkg struct {
		Items []struct {
			ID        string `xml:"id,attr"`
			Href      string `xml:"href,attr"`
			MediaType string `xml:"media-type,attr"`
		} `xml:"manifest>item"`
		Refs []struct {
			IDRef string `xml:"idref,attr"`
		} `xml:"spine>itemref"`
	}
	dec := xml.NewDecoder(rc)
	dec.Strict = false
	dec.CharsetReader = func(label string, in io.Reader) (io.Reader, error) { return in, nil }
	err = dec.Decode(&pkg)
	rc.Close()
	if err != nil {
		return nil, fmt.Errorf("xpointer: bad package document: %w", err)
	}
	type entry struct{ href, mediaType string }
	manifest := map[string]entry{}
	for _, m := range pkg.Items {
		manifest[m.ID] = entry{m.Href, m.MediaType}
	}
	dir := path.Dir(opfPath)
	b := &book{}
	for _, ref := range pkg.Refs {
		it := item{elems: map[string]int{"": 0}}
		if m, ok := manifest[ref.IDRef]; ok {
			low := strings.ToLower(m.href)
			if strings.Contains(m.mediaType, "html") || strings.HasSuffix(low, ".htm") || strings.HasSuffix(low, ".html") || strings.HasSuffix(low, ".xhtml") {
				name := unescape(m.href)
				if dir != "." && dir != "" {
					name = path.Join(dir, name)
				}
				if rc, err := open(name); err == nil {
					it = parseItem(rc)
					rc.Close()
				}
			}
		}
		b.items = append(b.items, it)
		b.total += it.total
	}
	if len(b.items) == 0 {
		return nil, errors.New("xpointer: empty spine")
	}
	return b, nil
}

// unescape turns a manifest href into a zip entry name.
func unescape(s string) string {
	if i := strings.IndexByte(s, '#'); i >= 0 {
		s = s[:i]
	}
	s = html.UnescapeString(s)
	if !strings.Contains(s, "%") {
		return s
	}
	var out []byte
	for i := 0; i < len(s); i++ {
		if s[i] == '%' && i+2 < len(s) {
			if v, err := strconv.ParseUint(s[i+1:i+3], 16, 8); err == nil {
				out = append(out, byte(v))
				i += 2
				continue
			}
		}
		out = append(out, s[i])
	}
	if !utf8.Valid(out) {
		return s
	}
	return string(out)
}
