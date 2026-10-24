package formats

import (
	"archive/zip"
	"bytes"
)

// DOCX: the title and author of docProps/core.xml.
func readDOCX(path string) (*Meta, error) {
	m := &Meta{Identifiers: map[string]string{}}
	zr, err := zip.OpenReader(path)
	if err != nil {
		return m, nil
	}
	defer zr.Close()
	ix := indexZip(&zr.Reader)
	b, err := readZipFile(ix.get("docProps/core.xml"), 1<<20)
	if err != nil {
		return m, nil
	}
	root, err := parseXML(bytes.NewReader(b))
	if err != nil {
		return m, nil
	}
	if t := root.first("title"); t != nil {
		m.Title = t.text()
	}
	if c := root.first("creator"); c != nil {
		m.Authors = splitPDFAuthors(c.text())
	}
	if d := root.first("description"); d != nil {
		m.Description = d.text()
	}
	return m, nil
}
