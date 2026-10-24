package formats

import (
	"encoding/xml"
	"io"
	"path"
	"regexp"
	"strconv"
	"strings"

	"golang.org/x/text/encoding/htmlindex"
)

// node is a generic XML element: OPF, FB2 and ComicInfo documents vary too
// much in namespaces and prefixes for fixed structs.
type node struct {
	Name     string // local name, lower case
	Attrs    map[string]string
	Text     string
	Children []*node
}

func (n *node) attr(names ...string) string {
	for _, k := range names {
		if v, ok := n.Attrs[k]; ok {
			return v
		}
	}
	return ""
}

// all returns the descendants with that local name, in document order.
func (n *node) all(name string) []*node {
	var out []*node
	var walk func(*node)
	walk = func(c *node) {
		for _, ch := range c.Children {
			if ch.Name == name {
				out = append(out, ch)
			}
			walk(ch)
		}
	}
	walk(n)
	return out
}

func (n *node) first(name string) *node {
	if l := n.all(name); len(l) > 0 {
		return l[0]
	}
	return nil
}

// text returns the element's text including that of its children.
func (n *node) text() string {
	if n == nil {
		return ""
	}
	var b strings.Builder
	var walk func(*node)
	walk = func(c *node) {
		b.WriteString(c.Text)
		for _, ch := range c.Children {
			walk(ch)
			b.WriteByte(' ')
		}
	}
	walk(n)
	return strings.TrimSpace(b.String())
}

func charsetReader(label string, r io.Reader) (io.Reader, error) {
	enc, err := htmlindex.Get(label)
	if err != nil {
		return r, nil
	}
	return enc.NewDecoder().Reader(r), nil
}

// parseXML reads a document into a node tree (attribute keys are local
// names, lower case; text keeps direct character data only).
func parseXML(r io.Reader) (*node, error) {
	dec := xml.NewDecoder(r)
	dec.Strict = false
	dec.Entity = xml.HTMLEntity
	dec.CharsetReader = charsetReader
	root := &node{Name: "#root", Attrs: map[string]string{}}
	stack := []*node{root}
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			if len(root.Children) > 0 {
				break // keep what was read from a broken document
			}
			return nil, err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			n := &node{Name: strings.ToLower(t.Name.Local), Attrs: map[string]string{}}
			for _, a := range t.Attr {
				k := strings.ToLower(a.Name.Local)
				if _, ok := n.Attrs[k]; !ok {
					n.Attrs[k] = a.Value
				}
			}
			parent := stack[len(stack)-1]
			parent.Children = append(parent.Children, n)
			stack = append(stack, n)
		case xml.EndElement:
			if len(stack) > 1 {
				stack = stack[:len(stack)-1]
			}
		case xml.CharData:
			stack[len(stack)-1].Text += string(t)
		}
	}
	return root, nil
}

// opfInfo is a parsed package document plus what EPUB reading needs.
type opfInfo struct {
	meta       *Meta
	manifest   map[string]opfItem // id → item
	spine      []string           // hrefs, relative to the OPF
	coverHref  string             // the cover image
	coverPage  string             // an XHTML page holding the cover
	firstImage string
}

type opfItem struct {
	href, mediaType, properties string
}

// ReadOPF reads an OPF document (an EPUB's package file or a Calibre sidecar).
func ReadOPF(r io.Reader) (*Meta, error) {
	info, err := parseOPF(r)
	if err != nil {
		return nil, err
	}
	info.meta.tidy()
	return info.meta, nil
}

var (
	asinRE    = regexp.MustCompile(`^(B0[0-9A-Z]{8}|[0-9]{9}[0-9X])$`)
	urnScheme = regexp.MustCompile(`^(?i)(urn:)?([a-z0-9_-]+):(.+)$`)
)

func parseOPF(r io.Reader) (*opfInfo, error) {
	root, err := parseXML(r)
	if err != nil {
		return nil, err
	}
	pkg := root.first("package")
	if pkg == nil {
		pkg = root
	}
	md := pkg.first("metadata")
	if md == nil {
		md = pkg
	}
	m := &Meta{Identifiers: map[string]string{}}
	info := &opfInfo{meta: m, manifest: map[string]opfItem{}}

	// EPUB 3 refinements: <meta refines="#id" property="…">value</meta>
	refines := map[string]map[string]string{}
	var collections []*node
	for _, mt := range md.all("meta") {
		if ref := strings.TrimPrefix(mt.attr("refines"), "#"); ref != "" {
			if refines[ref] == nil {
				refines[ref] = map[string]string{}
			}
			refines[ref][strings.ToLower(mt.attr("property"))] = strings.TrimSpace(mt.Text)
			continue
		}
		name, content := strings.ToLower(mt.attr("name")), mt.attr("content")
		switch name {
		case "calibre:series":
			m.Series = content
		case "calibre:series_index":
			m.SeriesIndex, _ = strconv.ParseFloat(strings.TrimSpace(content), 64)
		case "calibre:title_sort":
			m.SortTitle = content
		case "cover":
			info.coverHref = "#" + content // resolved after the manifest
		}
		switch strings.ToLower(mt.attr("property")) {
		case "belongs-to-collection":
			collections = append(collections, mt)
		}
	}
	// titles
	for _, t := range md.all("title") {
		v := strings.TrimSpace(t.text())
		if v == "" {
			continue
		}
		typ := refines[t.attr("id")]["title-type"]
		switch {
		case typ == "subtitle":
			if m.Subtitle == "" {
				m.Subtitle = v
			}
		case m.Title == "" && (typ == "" || typ == "main"):
			m.Title = v
		}
	}
	// creators
	for _, c := range md.all("creator") {
		role := strings.ToLower(c.attr("role"))
		if role == "" {
			role = strings.ToLower(refines[c.attr("id")]["role"])
		}
		if role != "" && role != "aut" {
			continue
		}
		m.Authors = append(m.Authors, splitAuthors(c.text())...)
	}
	// series from EPUB 3 collections (when Calibre's meta is absent)
	if m.Series == "" {
		for _, c := range collections {
			ref := refines[c.attr("id")]
			if t := ref["collection-type"]; t != "" && t != "series" {
				continue
			}
			m.Series = strings.TrimSpace(c.Text)
			m.SeriesIndex, _ = strconv.ParseFloat(ref["group-position"], 64)
			break
		}
	}
	// identifiers
	for _, id := range md.all("identifier") {
		v := strings.TrimSpace(id.text())
		if v == "" {
			continue
		}
		scheme := strings.ToLower(id.attr("scheme"))
		if scheme == "" {
			if mm := urnScheme.FindStringSubmatch(v); mm != nil && !strings.HasPrefix(strings.ToLower(v), "http") {
				scheme, v = strings.ToLower(mm[2]), mm[3]
			}
		}
		if scheme == "" && strings.Contains(strings.ToLower(id.attr("id")), "isbn") {
			scheme = "isbn"
		}
		switch scheme {
		case "isbn":
			if isbn := NormalizeISBN(v); isbn != "" && m.ISBN == "" {
				m.ISBN = isbn
			}
		case "uuid", "calibre", "google", "goodreads", "amazon", "asin", "mobi-asin", "openlibrary", "hardcover", "isfdb", "lccn", "oclc":
			key := scheme
			if key == "mobi-asin" || key == "amazon" {
				if !asinRE.MatchString(strings.ToUpper(v)) {
					continue // Calibre stores a UUID there when there is no real ASIN
				}
				key = "asin"
			}
			if _, ok := m.Identifiers[key]; !ok {
				m.Identifiers[key] = v
			}
		default:
			if isbn := NormalizeISBN(v); isbn != "" && m.ISBN == "" && len(nonISBN.ReplaceAllString(v, "")) >= 10 && !strings.Contains(v, "-0000-") {
				m.ISBN = isbn
			}
		}
	}
	if l := md.first("language"); l != nil {
		m.Language = l.text()
	}
	if p := md.first("publisher"); p != nil {
		m.Publisher = p.text()
	}
	for _, d := range md.all("date") {
		ev := strings.ToLower(d.attr("event"))
		if ev == "" || ev == "publication" || ev == "original-publication" {
			m.Published = d.text()
			break
		}
	}
	if d := md.first("description"); d != nil {
		m.Description = strings.TrimSpace(d.Text)
		if m.Description == "" {
			m.Description = d.text()
		}
	}
	for _, s := range md.all("subject") {
		m.Tags = append(m.Tags, s.text())
	}

	// manifest, spine, guide (EPUB only; a sidecar has none)
	for _, it := range pkg.all("item") {
		info.manifest[it.attr("id")] = opfItem{href: it.attr("href"), mediaType: strings.ToLower(it.attr("media-type")), properties: it.attr("properties")}
		if strings.Contains(" "+it.attr("properties")+" ", " cover-image ") {
			info.coverHref = it.attr("href")
		}
		if info.firstImage == "" && strings.HasPrefix(strings.ToLower(it.attr("media-type")), "image/") {
			info.firstImage = it.attr("href")
		}
	}
	if strings.HasPrefix(info.coverHref, "#") {
		id := info.coverHref[1:]
		info.coverHref = ""
		if it, ok := info.manifest[id]; ok && strings.HasPrefix(it.mediaType, "image/") {
			info.coverHref = it.href
		} else {
			// some books name the cover image's file instead of its id
			for _, it := range info.manifest {
				if strings.HasPrefix(it.mediaType, "image/") && (path.Base(it.href) == id || strings.TrimSuffix(path.Base(it.href), path.Ext(it.href)) == id) {
					info.coverHref = it.href
					break
				}
			}
		}
	}
	for _, ref := range pkg.all("itemref") {
		if it, ok := info.manifest[ref.attr("idref")]; ok {
			info.spine = append(info.spine, it.href)
		}
	}
	for _, g := range pkg.all("reference") {
		if strings.ToLower(g.attr("type")) == "cover" {
			info.coverPage = g.attr("href")
		}
	}
	return info, nil
}

// splitAuthors separates "A & B" / "A and B" lists Calibre and others write into one creator.
func splitAuthors(s string) []string {
	s = cleanSpace(s)
	if s == "" {
		return nil
	}
	var out []string
	for _, p := range strings.Split(s, " & ") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
