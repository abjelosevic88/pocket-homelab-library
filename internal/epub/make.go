package epub

import (
	"archive/zip"
	"bytes"
	"fmt"
	"strings"
	"time"
)

// Minimal builds a small valid EPUB 3 with one page of text (paragraphs
// separated by blank lines), e.g. the test document Devices → Test mails.
func Minimal(title, author, lang, text string) ([]byte, error) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	put := func(name, body string, method uint16) error {
		w, err := zw.CreateHeader(&zip.FileHeader{Name: name, Method: method, Modified: time.Now()})
		if err != nil {
			return err
		}
		_, err = w.Write([]byte(body))
		return err
	}
	var paras strings.Builder
	for _, p := range strings.Split(strings.TrimSpace(text), "\n\n") {
		paras.WriteString("<p>" + esc(strings.TrimSpace(p)) + "</p>\n")
	}
	files := []struct{ name, body string }{
		{"META-INF/container.xml", `<?xml version="1.0" encoding="utf-8"?>
<container version="1.0" xmlns="urn:oasis:names:tc:opendocument:xmlns:container"><rootfiles><rootfile full-path="OEBPS/content.opf" media-type="application/oebps-package+xml"/></rootfiles></container>`},
		{"OEBPS/content.opf", fmt.Sprintf(`<?xml version="1.0" encoding="utf-8"?>
<package xmlns="http://www.idpf.org/2007/opf" version="3.0" unique-identifier="uid">
  <metadata xmlns:dc="http://purl.org/dc/elements/1.1/">
    <dc:identifier id="uid">urn:uuid:pocket-library-%d</dc:identifier>
    <dc:title>%s</dc:title>
    <dc:creator>%s</dc:creator>
    <dc:language>%s</dc:language>
    <meta property="dcterms:modified">%s</meta>
  </metadata>
  <manifest>
    <item id="nav" href="nav.xhtml" media-type="application/xhtml+xml" properties="nav"/>
    <item id="p1" href="page.xhtml" media-type="application/xhtml+xml"/>
  </manifest>
  <spine><itemref idref="p1"/></spine>
</package>`, time.Now().UnixNano(), esc(title), esc(author), esc(lang), time.Now().UTC().Format("2006-01-02T15:04:05Z"))},
		{"OEBPS/nav.xhtml", fmt.Sprintf(`<?xml version="1.0" encoding="utf-8"?>
<html xmlns="http://www.w3.org/1999/xhtml" xmlns:epub="http://www.idpf.org/2007/ops"><head><title>%s</title></head>
<body><nav epub:type="toc"><ol><li><a href="page.xhtml">%s</a></li></ol></nav></body></html>`, esc(title), esc(title))},
		{"OEBPS/page.xhtml", fmt.Sprintf(`<?xml version="1.0" encoding="utf-8"?>
<html xmlns="http://www.w3.org/1999/xhtml"><head><title>%s</title></head>
<body><h1>%s</h1>
%s</body></html>`, esc(title), esc(title), paras.String())},
	}
	if err := put("mimetype", "application/epub+zip", zip.Store); err != nil {
		return nil, err
	}
	for _, f := range files {
		if err := put(f.name, f.body, zip.Deflate); err != nil {
			return nil, err
		}
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
