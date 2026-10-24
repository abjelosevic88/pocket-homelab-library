package formats

import (
	"encoding/binary"
	"errors"
	"io"
	"os"
	"strings"

	"golang.org/x/text/encoding/charmap"
)

// MOBI, AZW, AZW3 and PRC are Palm database files: a record list, record 0
// with the PalmDOC and MOBI headers and the EXTH metadata block, then text
// and image records.

type pdb struct {
	f       *os.File
	size    int64
	offsets []uint32
}

func openPDB(path string) (*pdb, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	fi, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}
	hdr := make([]byte, 78)
	if _, err := f.ReadAt(hdr, 0); err != nil {
		f.Close()
		return nil, err
	}
	n := int(binary.BigEndian.Uint16(hdr[76:78]))
	if n == 0 || n > 65000 {
		f.Close()
		return nil, errors.New("mobi: bad record count")
	}
	list := make([]byte, n*8)
	if _, err := f.ReadAt(list, 78); err != nil {
		f.Close()
		return nil, err
	}
	p := &pdb{f: f, size: fi.Size(), offsets: make([]uint32, n)}
	for i := 0; i < n; i++ {
		p.offsets[i] = binary.BigEndian.Uint32(list[i*8:])
	}
	return p, nil
}

func (p *pdb) record(i int, limit int) ([]byte, error) {
	if i < 0 || i >= len(p.offsets) {
		return nil, errors.New("mobi: no such record")
	}
	start := int64(p.offsets[i])
	end := p.size
	if i+1 < len(p.offsets) {
		end = int64(p.offsets[i+1])
	}
	if end <= start || end > p.size {
		return nil, errors.New("mobi: bad record")
	}
	if end-start > int64(limit) {
		return nil, errors.New("mobi: record too large")
	}
	b := make([]byte, end-start)
	if _, err := p.f.ReadAt(b, start); err != nil && err != io.EOF {
		return nil, err
	}
	return b, nil
}

func readMOBI(path string, wantCover bool) (*Meta, error) {
	p, err := openPDB(path)
	if err != nil {
		return nil, err
	}
	defer p.f.Close()
	r0, err := p.record(0, 1<<20)
	if err != nil {
		return nil, err
	}
	if len(r0) < 132 || string(r0[16:20]) != "MOBI" {
		return nil, errors.New("mobi: no MOBI header")
	}
	m := &Meta{Identifiers: map[string]string{}}
	hdrLen := int(binary.BigEndian.Uint32(r0[20:24]))
	encoding := binary.BigEndian.Uint32(r0[28:32])
	decode := func(b []byte) string {
		if encoding == 1252 {
			s, err := charmap.Windows1252.NewDecoder().Bytes(b)
			if err == nil {
				return string(s)
			}
		}
		return strings.ToValidUTF8(string(b), "")
	}
	nameOff := int(binary.BigEndian.Uint32(r0[84:88]))
	nameLen := int(binary.BigEndian.Uint32(r0[88:92]))
	if nameOff > 0 && nameOff+nameLen <= len(r0) {
		m.Title = decode(r0[nameOff : nameOff+nameLen])
	}
	firstImage := int(binary.BigEndian.Uint32(r0[108:112]))
	exthFlags := binary.BigEndian.Uint32(r0[128:132])
	coverOff, thumbOff := -1, -1
	if exthFlags&0x40 != 0 && 16+hdrLen+12 <= len(r0) {
		e := r0[16+hdrLen:]
		if string(e[:4]) == "EXTH" {
			count := int(binary.BigEndian.Uint32(e[8:12]))
			pos := 12
			for i := 0; i < count && pos+8 <= len(e); i++ {
				typ := binary.BigEndian.Uint32(e[pos:])
				ln := int(binary.BigEndian.Uint32(e[pos+4:]))
				if ln < 8 || pos+ln > len(e) {
					break
				}
				data := e[pos+8 : pos+ln]
				pos += ln
				num := func() int {
					if len(data) == 4 {
						return int(binary.BigEndian.Uint32(data))
					}
					return -1
				}
				switch typ {
				case 100:
					m.Authors = append(m.Authors, splitMOBIAuthors(decode(data))...)
				case 101:
					m.Publisher = decode(data)
				case 103:
					m.Description = decode(data)
				case 104:
					if m.ISBN == "" {
						m.ISBN = NormalizeISBN(decode(data))
					}
				case 105:
					for _, s := range strings.Split(decode(data), ";") {
						m.Tags = append(m.Tags, s)
					}
				case 106:
					m.Published = decode(data)
				case 113, 504:
					if v := strings.TrimSpace(decode(data)); asinRE.MatchString(strings.ToUpper(v)) {
						m.Identifiers["asin"] = v
					}
				case 503:
					m.Title = decode(data)
				case 524:
					m.Language = decode(data)
				case 201:
					coverOff = num()
				case 202:
					thumbOff = num()
				}
			}
		}
	}
	if wantCover && firstImage > 0 && firstImage < len(p.offsets) {
		for _, off := range []int{coverOff, thumbOff} {
			if off < 0 {
				continue
			}
			if b, err := p.record(firstImage+off, maxCover); err == nil && imageMIME(b) != "" {
				m.Cover = b
				break
			}
		}
		if m.Cover == nil {
			// no cover record named: the first image
			for i := firstImage; i < len(p.offsets) && i < firstImage+5; i++ {
				if b, err := p.record(i, maxCover); err == nil && imageMIME(b) != "" {
					m.Cover = b
					break
				}
			}
		}
	}
	// a text length estimate: the PalmDOC header's uncompressed text length
	if tl := int(binary.BigEndian.Uint32(r0[4:8])); tl > 0 {
		m.Words = tl / 6
	}
	return m, nil
}

// splitMOBIAuthors: Calibre writes "Last, First" or several authors joined with " & ".
func splitMOBIAuthors(s string) []string {
	var out []string
	for _, a := range splitAuthors(s) {
		if parts := strings.Split(a, ", "); len(parts) == 2 && !strings.Contains(parts[1], " ") && len(strings.Fields(parts[0])) == 1 {
			a = parts[1] + " " + parts[0]
		}
		out = append(out, a)
	}
	return out
}
