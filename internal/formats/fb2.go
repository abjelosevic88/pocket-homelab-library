package formats

import (
	"archive/zip"
	"encoding/base64"
	"errors"
	"io"
	"os"
	"strconv"
	"strings"
)

// FictionBook 2: one XML file (FB2), or that file in a zip (FBZ).

func readFB2File(path string, wantCover bool) (*Meta, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return readFB2(io.LimitReader(f, 256<<20), wantCover)
}

func readFBZ(path string, wantCover bool) (*Meta, error) {
	zr, err := zip.OpenReader(path)
	if err != nil {
		return nil, err
	}
	defer zr.Close()
	for _, f := range zr.File {
		if strings.HasSuffix(strings.ToLower(f.Name), ".fb2") {
			rc, err := f.Open()
			if err != nil {
				return nil, err
			}
			defer rc.Close()
			return readFB2(io.LimitReader(rc, 256<<20), wantCover)
		}
	}
	return nil, errors.New("fbz: no .fb2 inside")
}

func readFB2(r io.Reader, wantCover bool) (*Meta, error) {
	root, err := parseXML(r)
	if err != nil {
		return nil, err
	}
	m := &Meta{Identifiers: map[string]string{}}
	ti := root.first("title-info")
	if ti == nil {
		return nil, errors.New("fb2: no title-info")
	}
	if t := ti.first("book-title"); t != nil {
		m.Title = t.text()
	}
	for _, a := range ti.Children {
		if a.Name != "author" {
			continue
		}
		var parts []string
		for _, k := range []string{"first-name", "middle-name", "last-name"} {
			if n := a.first(k); n != nil && strings.TrimSpace(n.text()) != "" {
				parts = append(parts, strings.TrimSpace(n.text()))
			}
		}
		if len(parts) == 0 {
			if n := a.first("nickname"); n != nil {
				parts = append(parts, n.text())
			}
		}
		if len(parts) > 0 {
			m.Authors = append(m.Authors, strings.Join(parts, " "))
		}
	}
	for _, g := range ti.all("genre") {
		m.Tags = append(m.Tags, g.text())
	}
	if l := ti.first("lang"); l != nil {
		m.Language = l.text()
	}
	if s := ti.first("sequence"); s != nil {
		m.Series = s.attr("name")
		m.SeriesIndex, _ = strconv.ParseFloat(strings.TrimSpace(s.attr("number")), 64)
	}
	if a := ti.first("annotation"); a != nil {
		m.Description = a.text()
	}
	if d := ti.first("date"); d != nil {
		m.Published = d.attr("value")
		if m.Published == "" {
			m.Published = d.text()
		}
	}
	if pi := root.first("publish-info"); pi != nil {
		if p := pi.first("publisher"); p != nil {
			m.Publisher = p.text()
		}
		if i := pi.first("isbn"); i != nil {
			m.ISBN = i.text()
		}
		if y := pi.first("year"); y != nil && m.Published == "" {
			m.Published = y.text()
		}
	}
	if body := root.first("body"); body != nil {
		m.Words = countWords([]byte(body.text()))
	}
	if wantCover {
		if cp := ti.first("coverpage"); cp != nil {
			if img := cp.first("image"); img != nil {
				id := strings.TrimPrefix(img.attr("href"), "#")
				for _, b := range root.all("binary") {
					if b.attr("id") == id {
						data, err := base64.StdEncoding.DecodeString(strings.Map(func(r rune) rune {
							if r == ' ' || r == '\n' || r == '\r' || r == '\t' {
								return -1
							}
							return r
						}, b.Text))
						if err == nil && imageMIME(data) != "" {
							m.Cover = data
						}
						break
					}
				}
			}
		}
	}
	return m, nil
}
