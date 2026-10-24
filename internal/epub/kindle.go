package epub

import (
	"fmt"
	"path"
	"regexp"
	"strings"
	"unicode/utf8"
)

// KindleFix makes an EPUB acceptable to Amazon's Send to Kindle. It follows
// Calibre-Web Automated's kindle-epub-fixer (itself after innocenat's
// kindle-epub-fix): links to a chapter's <body> id, duplicate XML
// declarations in container.xml, missing or incomplete encoding
// declarations, a missing or malformed language, and images without a
// source. defaultLang is used when the book names no usable language.
// What changed is appended to b.Fixed.
func (b *Book) KindleFix(defaultLang string) error {
	if defaultLang == "" {
		defaultLang = "en"
	}
	b.fixBodyIDLinks()
	b.fixContainer()
	b.fixEncoding()
	if err := b.fixLanguage(defaultLang); err != nil {
		return err
	}
	b.fixStrayImages()
	return nil
}

func (b *Book) fixed(format string, a ...any) { b.Fixed = append(b.Fixed, fmt.Sprintf(format, a...)) }

var bodyIDRE = regexp.MustCompile(`(?is)<body\b[^>]*\bid\s*=\s*["']([^"']+)["']`)

// fixBodyIDLinks: Kindle shows a link to "chapter.xhtml#bodyid" as broken;
// pointing at the file itself works.
func (b *Book) fixBodyIDLinks() {
	type link struct{ from, to string }
	var links []link
	for _, n := range b.entriesWithExt("html", "xhtml") {
		d, err := b.Read(n)
		if err != nil {
			continue
		}
		if m := bodyIDRE.FindSubmatch(d); m != nil {
			base := path.Base(n)
			links = append(links, link{base + "#" + string(m[1]), base})
		}
	}
	if len(links) == 0 {
		return
	}
	for _, n := range b.entriesWithExt("html", "xhtml", "htm", "ncx", "opf") {
		d, err := b.Read(n)
		if err != nil {
			continue
		}
		s := string(d)
		changed := false
		for _, l := range links {
			if strings.Contains(s, l.from) {
				s = strings.ReplaceAll(s, l.from, l.to)
				changed = true
				b.fixed("Replaced link target %s with %s in %s", l.from, l.to, n)
			}
		}
		if changed {
			b.Put(n, []byte(s))
		}
	}
}

var xmlDeclRE = regexp.MustCompile(`<\?xml[^>]*\?>`)

func (b *Book) fixContainer() {
	const n = "META-INF/container.xml"
	d, err := b.Read(n)
	if err != nil {
		return
	}
	s := strings.TrimLeft(string(d), " \t\r\n\ufeff")
	decls := xmlDeclRE.FindAllStringIndex(s, -1)
	if len(decls) <= 1 {
		return
	}
	first := decls[0][1]
	b.Put(n, []byte(s[:first]+xmlDeclRE.ReplaceAllString(s[first:], "")))
	b.fixed("Removed duplicate XML declaration(s) in %s", n)
}

var (
	leadDeclRE     = regexp.MustCompile(`^(\s*)<\?xml[^>]*\?>`)
	declEncodingRE = regexp.MustCompile(`(?i)^\s*<\?xml[^>]*encoding\s*=\s*["']([^"']+)["']`)
)

// fixEncoding: Kindle wants every XML document to declare its encoding.
// Documents that are valid UTF-8 get a UTF-8 declaration; a declaration of
// another encoding is left alone (the bytes are in that encoding).
func (b *Book) fixEncoding() {
	const decl = `<?xml version="1.0" encoding="utf-8"?>`
	for _, n := range b.entriesWithExt("xhtml", "xml", "opf", "ncx", "svg") {
		d, err := b.Read(n)
		if err != nil {
			continue
		}
		hasBOM := strings.HasPrefix(string(d), "\ufeff")
		s := strings.TrimPrefix(string(d), "\ufeff")
		if m := declEncodingRE.FindStringSubmatch(s); m != nil {
			if !hasBOM {
				continue
			}
			// a BOM before the declaration confuses some readers
			b.Put(n, []byte(s))
			b.fixed("Removed the byte order mark in %s", n)
			continue
		}
		if !utf8.ValidString(s) {
			continue
		}
		if leadDeclRE.MatchString(s) {
			s = leadDeclRE.ReplaceAllString(s, "${1}"+decl)
			b.fixed("Added utf-8 to the XML declaration of %s", n)
		} else {
			s = decl + "\n" + s
			b.fixed("Added an XML declaration to %s", n)
		}
		b.Put(n, []byte(s))
	}
}

var (
	langTagRE = regexp.MustCompile(`(?i)^[a-z]{2,3}(-[a-z0-9]{2,8})*$`)
	// three-letter (ISO 639-2) codes Amazon reads by their first two letters, mapped properly
	lang3 = map[string]string{
		"eng": "en", "srp": "sr", "scc": "sr", "hrv": "hr", "scr": "hr", "bos": "bs", "slv": "sl", "mkd": "mk", "mac": "mk", "bul": "bg",
		"deu": "de", "ger": "de", "fra": "fr", "fre": "fr", "spa": "es", "ita": "it", "por": "pt", "rus": "ru", "ukr": "uk", "pol": "pl",
		"ces": "cs", "cze": "cs", "slk": "sk", "slo": "sk", "hun": "hu", "ron": "ro", "rum": "ro", "nld": "nl", "dut": "nl", "swe": "sv",
		"nor": "no", "nob": "nb", "nno": "nn", "dan": "da", "fin": "fi", "est": "et", "lav": "lv", "lit": "lt", "ell": "el", "gre": "el",
		"tur": "tr", "ara": "ar", "heb": "he", "jpn": "ja", "zho": "zh", "chi": "zh", "kor": "ko", "hin": "hi", "lat": "la", "cat": "ca",
		"glg": "gl", "eus": "eu", "baq": "eu", "isl": "is", "ice": "is", "gle": "ga", "cym": "cy", "wel": "cy", "sqi": "sq", "alb": "sq",
		"bel": "be", "vie": "vi", "tha": "th", "ind": "id", "msa": "ms", "may": "ms", "fas": "fa", "per": "fa", "epo": "eo", "afr": "af",
	}
	dcLangRE = regexp.MustCompile(`(?s)<([A-Za-z0-9_-]+):language\b[^>]*>(.*?)</[A-Za-z0-9_-]+:language\s*>`)
)

// NormalizeLanguage turns a language tag into the base code Amazon reads
// ("sr-Latn" → "sr", "srp" → "sr"); "" when it is not a language tag.
func NormalizeLanguage(tag string) string {
	tag = strings.TrimSpace(tag)
	if !langTagRE.MatchString(tag) {
		return ""
	}
	base := strings.ToLower(strings.SplitN(tag, "-", 2)[0])
	if len(base) == 3 {
		if two, ok := lang3[base]; ok {
			return two
		}
	}
	return base
}

func (b *Book) fixLanguage(defaultLang string) error {
	raw, err := b.Read(b.OPFPath)
	if err != nil {
		return err
	}
	opf := string(raw)
	if m := dcLangRE.FindStringSubmatchIndex(opf); m != nil {
		cur := strings.TrimSpace(xmlUnescape(opf[m[4]:m[5]]))
		want := NormalizeLanguage(cur)
		if want == "" {
			want = defaultLang
		}
		if want != cur {
			opf = opf[:m[4]] + esc(want) + opf[m[5]:]
			b.Put(b.OPFPath, []byte(opf))
			b.fixed("Language %q → %q", cur, want)
		}
		return nil
	}
	dc, ok := nsPrefix(opf, dcNS)
	if !ok {
		return nil // an unusual package document: leave it
	}
	i := regexp.MustCompile(`</(?:[A-Za-z0-9_-]+:)?metadata\s*>`).FindStringIndex(opf)
	if i == nil {
		return nil
	}
	opf = opf[:i[0]] + fmt.Sprintf("  <%s:language>%s</%s:language>\n  ", dc, esc(defaultLang), dc) + opf[i[0]:]
	b.Put(b.OPFPath, []byte(opf))
	b.fixed("No language: set to %q", defaultLang)
	return nil
}

var (
	imgNoSrcRE    = regexp.MustCompile(`(?i)<img\b[^>]*>`)
	imgHasSrcRE   = regexp.MustCompile(`(?i)\bsrc\s*=`)
	imgEmptySrcRE = regexp.MustCompile(`(?i)\bsrc\s*=\s*(?:"\s*"|'\s*')`)
)

// fixStrayImages removes <img> tags without a source, which Kindle rejects.
func (b *Book) fixStrayImages() {
	for _, n := range b.entriesWithExt("html", "xhtml") {
		d, err := b.Read(n)
		if err != nil {
			continue
		}
		s := imgNoSrcRE.ReplaceAllStringFunc(string(d), func(tag string) string {
			if !imgHasSrcRE.MatchString(tag) || imgEmptySrcRE.MatchString(tag) {
				return ""
			}
			return tag
		})
		if s != string(d) {
			b.Put(n, []byte(s))
			b.fixed("Removed stray image tag(s) in %s", n)
		}
	}
}
