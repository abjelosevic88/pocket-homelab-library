package formats

import (
	"bufio"
	"bytes"
	"context"
	"io"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// PDFs are read with poppler's pdfinfo and pdftoppm when they are installed
// (the container image has them); without them only a page count is guessed.

func readPDF(path string, wantCover bool) (*Meta, error) {
	m := &Meta{Identifiers: map[string]string{}}
	if bin, err := exec.LookPath("pdfinfo"); err == nil {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		out, err := exec.CommandContext(ctx, bin, "-enc", "UTF-8", path).Output()
		cancel()
		if err == nil {
			sc := bufio.NewScanner(bytes.NewReader(out))
			for sc.Scan() {
				k, v, ok := strings.Cut(sc.Text(), ":")
				if !ok {
					continue
				}
				v = strings.TrimSpace(v)
				switch k {
				case "Title":
					m.Title = v
				case "Author":
					m.Authors = splitPDFAuthors(v)
				case "Subject":
					m.Description = v
				// Keywords are left out: in practice they are what the converting
				// tool wrote about itself ("Document Converter Pro", a web address).
				case "Pages":
					m.Pages, _ = strconv.Atoi(v)
				}
			}
		}
	} else {
		m.Pages = guessPDFPages(path)
	}
	// producers write file names and placeholders into Title
	if t := strings.ToLower(m.Title); t == "untitled" || strings.HasSuffix(t, ".pdf") || strings.HasSuffix(t, ".doc") || strings.HasPrefix(t, "microsoft word") {
		m.Title = ""
	}
	if len(m.Authors) == 1 && (strings.EqualFold(m.Authors[0], "user") || strings.EqualFold(m.Authors[0], "admin") || strings.EqualFold(m.Authors[0], "owner")) {
		m.Authors = nil
	}
	if wantCover {
		m.Cover = pdfCover(path)
	}
	return m, nil
}

func splitPDFAuthors(s string) []string {
	var out []string
	for _, a := range strings.FieldsFunc(s, func(r rune) bool { return r == ';' || r == '&' }) {
		if a = strings.TrimSpace(a); a != "" {
			out = append(out, a)
		}
	}
	return out
}

func pdfCover(path string) []byte {
	bin, err := exec.LookPath("pdftoppm")
	if err != nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, bin, "-jpeg", "-r", "72", "-f", "1", "-l", "1", "-scale-to", "900", "-singlefile", path).Output()
	if err != nil || imageMIME(out) == "" {
		return nil
	}
	return out
}

var pdfCount = regexp.MustCompile(`/Type\s*/Pages\b[^>]*?/Count\s+(\d+)`)

// guessPDFPages finds the page tree's count in the last few megabytes (or all of a small file).
func guessPDFPages(path string) int {
	f, err := os.Open(path)
	if err != nil {
		return 0
	}
	defer f.Close()
	fi, _ := f.Stat()
	const window = 4 << 20
	var b []byte
	if fi.Size() <= 2*window {
		b, _ = io.ReadAll(f)
	} else {
		b = make([]byte, window)
		_, _ = f.ReadAt(b, 0)
	}
	best := 0
	for _, m := range pdfCount.FindAllSubmatch(b, -1) {
		if n, _ := strconv.Atoi(string(m[1])); n > best {
			best = n
		}
	}
	return best
}
