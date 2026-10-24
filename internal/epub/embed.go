package epub

import (
	"fmt"
	"path"
	"regexp"
	"strconv"
	"strings"
)

// Meta is what the catalogue says about a book, written into the package
// document by Embed. Empty fields leave what the file has.
type Meta struct {
	Title       string
	Authors     []string
	AuthorSorts []string // "Last, First" per author ('' = none)
	Series      string
	SeriesIndex float64
	Description string
	Language    string
	Publisher   string
	Published   string
	ISBN        string
	Tags        []string
	Cover       []byte // JPEG; nil = keep the file's cover
}

const dcNS = "http://purl.org/dc/elements/1.1/"
const opfNS = "http://www.idpf.org/2007/opf"

// Embed writes the catalogue's metadata into the book's package document and
// replaces its cover. It rewrites only the elements it owns and keeps the
// rest of the document (and its formatting) as it was.
func (b *Book) Embed(m Meta) error {
	raw, err := b.Read(b.OPFPath)
	if err != nil {
		return err
	}
	opf := string(raw)
	mm := metadataRE.FindStringSubmatchIndex(opf)
	if mm == nil {
		return fmt.Errorf("%s has no <metadata>", b.OPFPath)
	}
	v3 := strings.HasPrefix(epubVersion(opf), "3")
	metaTag, metaAttrs, body := opf[mm[2]:mm[3]], opf[mm[4]:mm[5]], opf[mm[6]:mm[7]]

	dc, ok := nsPrefix(opf, dcNS)
	if !ok {
		dc = "dc"
		metaAttrs += ` xmlns:dc="` + dcNS + `"`
	}
	op, ok := nsPrefix(opf, opfNS)
	if !ok && !v3 {
		op = "opf"
		metaAttrs += ` xmlns:opf="` + opfNS + `"`
	}
	uniqueID := ""
	if t := packageRE.FindString(opf); t != "" {
		uniqueID = attrs(t)["unique-identifier"]
	}

	removedIDs := map[string]bool{}
	drop := func(local string, keep func(tag string, a map[string]string) bool) {
		re := regexp.MustCompile(`(?s)<` + regexp.QuoteMeta(dc) + `:` + local + `\b([^>]*?)(?:/>|>(.*?)</` + regexp.QuoteMeta(dc) + `:` + local + `\s*>)\s*`)
		body = re.ReplaceAllStringFunc(body, func(el string) string {
			open := el
			if i := strings.Index(el, ">"); i >= 0 {
				open = el[:i+1]
			}
			a := attrs(open)
			if keep != nil && keep(el, a) {
				return el
			}
			if id := a["id"]; id != "" {
				removedIDs[id] = true
			}
			return ""
		})
	}
	if m.Title != "" {
		drop("title", nil)
	}
	if len(m.Authors) > 0 {
		// EPUB 3 gives roles in refinements: <meta refines="#c2" property="role">trl</meta>
		refinedRole := map[string]string{}
		for _, r := range regexp.MustCompile(`(?s)<(?:[A-Za-z0-9_-]+:)?meta\b([^>]*property\s*=\s*["']role["'][^>]*)>(.*?)</`).FindAllStringSubmatch(body, -1) {
			if id := strings.TrimPrefix(attrs(r[1])["refines"], "#"); id != "" {
				refinedRole[id] = strings.TrimSpace(r[2])
			}
		}
		drop("creator", func(_ string, a map[string]string) bool {
			role := a[op+":role"]
			if role == "" {
				role = a["role"]
			}
			if role == "" {
				role = refinedRole[a["id"]]
			}
			return role != "" && role != "aut"
		})
	}
	if m.Description != "" {
		drop("description", nil)
	}
	if m.Language != "" {
		drop("language", nil)
	}
	if m.Publisher != "" {
		drop("publisher", nil)
	}
	if m.Published != "" {
		drop("date", func(_ string, a map[string]string) bool {
			ev := a[op+":event"]
			return ev != "" && ev != "publication" && ev != "original-publication"
		})
	}
	if len(m.Tags) > 0 {
		drop("subject", nil)
	}
	isbn := strings.ReplaceAll(strings.ReplaceAll(m.ISBN, "-", ""), " ", "")
	if isbn != "" {
		drop("identifier", func(el string, a map[string]string) bool {
			if a["id"] != "" && a["id"] == uniqueID {
				return true
			}
			low := strings.ToLower(el)
			return !strings.Contains(low, "isbn")
		})
	}
	// the meta elements this owns: calibre series, EPUB3 collections, refinements of removed elements
	metaRE := regexp.MustCompile(`(?s)<(?:[A-Za-z0-9_-]+:)?meta\b([^>]*?)(?:/>|>(.*?)</(?:[A-Za-z0-9_-]+:)?meta\s*>)\s*`)
	if m.Series != "" {
		body = metaRE.ReplaceAllStringFunc(body, func(el string) string {
			a := attrs(el[:strings.Index(el, ">")+1])
			if n := a["name"]; n == "calibre:series" || n == "calibre:series_index" {
				return ""
			}
			if a["property"] == "belongs-to-collection" {
				if id := a["id"]; id != "" {
					removedIDs[id] = true
				}
				return ""
			}
			return el
		})
	}
	body = metaRE.ReplaceAllStringFunc(body, func(el string) string {
		a := attrs(el[:strings.Index(el, ">")+1])
		if r := strings.TrimPrefix(a["refines"], "#"); r != "" && removedIDs[r] {
			return ""
		}
		return el
	})

	// the new elements, before </metadata>
	var add []string
	el := func(s string) { add = append(add, s) }
	if m.Title != "" {
		el(fmt.Sprintf(`<%s:title id="pl-title">%s</%s:title>`, dc, esc(m.Title), dc))
	}
	for i, a := range m.Authors {
		sortName := ""
		if i < len(m.AuthorSorts) {
			sortName = m.AuthorSorts[i]
		}
		id := fmt.Sprintf("pl-creator%d", i+1)
		if v3 {
			el(fmt.Sprintf(`<%s:creator id="%s">%s</%s:creator>`, dc, id, esc(a), dc))
			el(fmt.Sprintf(`<meta refines="#%s" property="role" scheme="marc:relators">aut</meta>`, id))
			if sortName != "" {
				el(fmt.Sprintf(`<meta refines="#%s" property="file-as">%s</meta>`, id, esc(sortName)))
			}
		} else {
			fileAs := ""
			if sortName != "" {
				fileAs = fmt.Sprintf(` %s:file-as="%s"`, op, esc(sortName))
			}
			el(fmt.Sprintf(`<%s:creator id="%s" %s:role="aut"%s>%s</%s:creator>`, dc, id, op, fileAs, esc(a), dc))
		}
	}
	if m.Description != "" {
		el(fmt.Sprintf(`<%s:description>%s</%s:description>`, dc, esc(m.Description), dc))
	}
	if m.Language != "" {
		el(fmt.Sprintf(`<%s:language>%s</%s:language>`, dc, esc(m.Language), dc))
	}
	if m.Publisher != "" {
		el(fmt.Sprintf(`<%s:publisher>%s</%s:publisher>`, dc, esc(m.Publisher), dc))
	}
	if m.Published != "" {
		el(fmt.Sprintf(`<%s:date>%s</%s:date>`, dc, esc(m.Published), dc))
	}
	for _, t := range m.Tags {
		el(fmt.Sprintf(`<%s:subject>%s</%s:subject>`, dc, esc(t), dc))
	}
	if isbn != "" {
		if v3 {
			el(fmt.Sprintf(`<%s:identifier id="pl-isbn">urn:isbn:%s</%s:identifier>`, dc, esc(isbn), dc))
		} else {
			el(fmt.Sprintf(`<%s:identifier %s:scheme="ISBN">%s</%s:identifier>`, dc, op, esc(isbn), dc))
		}
	}
	if m.Series != "" {
		idx := strconv.FormatFloat(m.SeriesIndex, 'f', -1, 64)
		el(fmt.Sprintf(`<meta name="calibre:series" content="%s"/>`, esc(m.Series)))
		if m.SeriesIndex > 0 {
			el(fmt.Sprintf(`<meta name="calibre:series_index" content="%s"/>`, idx))
		}
		if v3 {
			el(fmt.Sprintf(`<meta property="belongs-to-collection" id="pl-series">%s</meta>`, esc(m.Series)))
			el(`<meta refines="#pl-series" property="collection-type">series</meta>`)
			if m.SeriesIndex > 0 {
				el(fmt.Sprintf(`<meta refines="#pl-series" property="group-position">%s</meta>`, idx))
			}
		}
	}
	if len(add) > 0 {
		body = strings.TrimRight(body, " \t\r\n") + "\n    " + strings.Join(add, "\n    ") + "\n  "
	}
	closeTag := "</" + metaTag + ">"
	opf = opf[:mm[0]] + "<" + metaTag + metaAttrs + ">" + body + closeTag + opf[mm[1]:]

	if len(m.Cover) > 0 {
		opf = b.replaceCover(opf, m.Cover, v3)
	}
	b.Put(b.OPFPath, []byte(opf))
	return nil
}

// replaceCover puts a JPEG cover in: over the file's cover image when that
// is a JPEG already, else as a new manifest item the cover markers point to.
func (b *Book) replaceCover(opf string, jpeg []byte, v3 bool) string {
	items := manifestItems(opf)
	coverID := ""
	if m := regexp.MustCompile(`<(?:[A-Za-z0-9_-]+:)?meta\b[^>]*name\s*=\s*["']cover["'][^>]*>`).FindString(opf); m != "" {
		coverID = attrs(m)["content"]
	}
	var cur *manifestItem
	for i := range items {
		it := &items[i]
		if strings.Contains(" "+it.attrs["properties"]+" ", " cover-image ") || (coverID != "" && it.attrs["id"] == coverID) {
			cur = it
			break
		}
	}
	if cur != nil && strings.Contains(cur.attrs["media-type"], "jpeg") {
		b.Put(path.Clean(b.opfDir()+cur.attrs["href"]), jpeg)
		return opf
	}
	name := "pocket-library-cover.jpg"
	b.Put(b.opfDir()+name, jpeg)
	if cur != nil && v3 {
		// the old image keeps its place in the book, but is no longer "the" cover
		newTag := regexp.MustCompile(`\s*\bcover-image\b`).ReplaceAllString(cur.tag, "")
		newTag = strings.Replace(newTag, `properties=""`, "", 1)
		opf = strings.Replace(opf, cur.tag, newTag, 1)
	}
	props := ""
	if v3 {
		props = ` properties="cover-image"`
	}
	item := fmt.Sprintf(`<item id="pl-cover" href="%s" media-type="image/jpeg"%s/>`, name, props)
	if i := regexp.MustCompile(`</(?:[A-Za-z0-9_-]+:)?manifest\s*>`).FindStringIndex(opf); i != nil {
		opf = opf[:i[0]] + "  " + item + "\n  " + opf[i[0]:]
	}
	metaCover := regexp.MustCompile(`<(?:[A-Za-z0-9_-]+:)?meta\b[^>]*name\s*=\s*["']cover["'][^>]*/?>`)
	if metaCover.MatchString(opf) {
		opf = metaCover.ReplaceAllString(opf, `<meta name="cover" content="pl-cover"/>`)
	} else if i := regexp.MustCompile(`</(?:[A-Za-z0-9_-]+:)?metadata\s*>`).FindStringIndex(opf); i != nil {
		opf = opf[:i[0]] + `  <meta name="cover" content="pl-cover"/>` + "\n  " + opf[i[0]:]
	}
	return opf
}
