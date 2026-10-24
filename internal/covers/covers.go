// Package covers stores book covers and author photos as normalised JPEGs
// and makes the smaller sizes the web app and e-readers ask for.
package covers

import (
	"bytes"
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	"image/color"
	_ "image/gif" // decoders
	"image/jpeg"
	_ "image/png"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"

	_ "golang.org/x/image/bmp"
	"golang.org/x/image/draw"
	_ "golang.org/x/image/webp"
)

// MaxHeight is the height a stored cover is reduced to.
const MaxHeight = 1600

// Widths are the thumbnail sizes made on request (a request snaps up to the next one).
var Widths = []int{120, 200, 300, 400, 600, 800}

// ErrNotImage is returned for bytes that are not a picture this package decodes.
var ErrNotImage = errors.New("not an image")

var keyRE = regexp.MustCompile(`[^a-zA-Z0-9_-]+`)

// Save stores an image as "<key>-<hash>.jpg" in dir and returns the file name
// and the picture's average colour (#rrggbb). Older files of the same key are removed.
func Save(dir, key string, img []byte) (name, avg string, err error) {
	jpg, avg, err := Prepare(img)
	if err != nil {
		return "", "", err
	}
	name, err = Store(dir, key, jpg)
	return name, avg, err
}

// Prepare decodes an image and makes the JPEG Store keeps (the slow part,
// safe to run in parallel), with its average colour.
func Prepare(img []byte) (jpg []byte, avg string, err error) {
	src, _, err := image.Decode(bytes.NewReader(img))
	if err != nil {
		return nil, "", fmt.Errorf("%w: %v", ErrNotImage, err)
	}
	b := src.Bounds()
	if b.Dx() < 8 || b.Dy() < 8 {
		return nil, "", ErrNotImage
	}
	if b.Dy() > MaxHeight {
		src = scale(src, b.Dx()*MaxHeight/b.Dy(), MaxHeight)
	}
	var out bytes.Buffer
	if err := jpeg.Encode(&out, flatten(src), &jpeg.Options{Quality: 88}); err != nil {
		return nil, "", err
	}
	return out.Bytes(), Average(src), nil
}

// Store writes a JPEG made by Prepare under key and removes the key's older files.
func Store(dir, key string, jpg []byte) (string, error) {
	sum := sha1.Sum(jpg)
	key = keyRE.ReplaceAllString(key, "_")
	name := key + "-" + hex.EncodeToString(sum[:5]) + ".jpg"
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	if err := writeAtomic(filepath.Join(dir, name), jpg); err != nil {
		return "", err
	}
	Remove(dir, key, name)
	return name, nil
}

// Remove deletes a key's stored files and thumbnails, except keep.
func Remove(dir, key, keep string) {
	key = keyRE.ReplaceAllString(key, "_")
	for _, pattern := range []string{filepath.Join(dir, key+"-*.jpg"), filepath.Join(dir, "thumbs", key+"-*.jpg")} {
		matches, _ := filepath.Glob(pattern)
		for _, m := range matches {
			base := filepath.Base(m)
			if keep != "" && (base == keep || strings.HasPrefix(base, strings.TrimSuffix(keep, ".jpg")+"-w")) {
				continue
			}
			// "<key>-<hash>.jpg" and its thumbnails "<key>-<hash>-w200.jpg" only, not "<key>0-…"
			if !strings.HasPrefix(base, key+"-") {
				continue
			}
			_ = os.Remove(m)
		}
	}
}

var thumbLocks sync.Map

// Thumb returns the path of the stored image resized to (about) width
// pixels wide, making and caching it in dir/thumbs on first use. A width of
// 0 or bigger than the picture returns the stored image itself.
func Thumb(dir, name string, width int) (string, error) {
	orig := filepath.Join(dir, filepath.Base(name))
	if width <= 0 {
		return orig, nil
	}
	w := Widths[len(Widths)-1]
	for _, s := range Widths {
		if width <= s {
			w = s
			break
		}
	}
	thumb := filepath.Join(dir, "thumbs", strings.TrimSuffix(filepath.Base(name), ".jpg")+fmt.Sprintf("-w%d.jpg", w))
	if _, err := os.Stat(thumb); err == nil {
		return thumb, nil
	}
	mu, _ := thumbLocks.LoadOrStore(thumb, &sync.Mutex{})
	mu.(*sync.Mutex).Lock()
	defer mu.(*sync.Mutex).Unlock()
	if _, err := os.Stat(thumb); err == nil {
		return thumb, nil
	}
	f, err := os.Open(orig)
	if err != nil {
		return "", err
	}
	src, _, err := image.Decode(f)
	f.Close()
	if err != nil {
		return "", err
	}
	b := src.Bounds()
	if b.Dx() <= w {
		return orig, nil
	}
	dst := scale(src, w, b.Dy()*w/b.Dx())
	var out bytes.Buffer
	if err := jpeg.Encode(&out, dst, &jpeg.Options{Quality: 82}); err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(thumb), 0o755); err != nil {
		return "", err
	}
	if err := writeAtomic(thumb, out.Bytes()); err != nil {
		return "", err
	}
	return thumb, nil
}

// Resize returns a JPEG of img at most width pixels wide (comic pages for small screens).
func Resize(img []byte, width int) ([]byte, error) {
	src, _, err := image.Decode(bytes.NewReader(img))
	if err != nil {
		return nil, ErrNotImage
	}
	b := src.Bounds()
	if b.Dx() > width && width > 0 {
		src = scale(src, width, b.Dy()*width/b.Dx())
	}
	var out bytes.Buffer
	if err := jpeg.Encode(&out, flatten(src), &jpeg.Options{Quality: 85}); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

func scale(src image.Image, w, h int) image.Image {
	if w < 1 {
		w = 1
	}
	if h < 1 {
		h = 1
	}
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.CatmullRom.Scale(dst, dst.Bounds(), src, src.Bounds(), draw.Src, nil)
	return dst
}

// flatten puts transparent images on white (JPEG has no alpha).
func flatten(src image.Image) image.Image {
	if _, ok := src.(*image.YCbCr); ok {
		return src
	}
	b := src.Bounds()
	dst := image.NewRGBA(b)
	draw.Draw(dst, b, image.NewUniform(color.White), image.Point{}, draw.Src)
	draw.Draw(dst, b, src, b.Min, draw.Over)
	return dst
}

// Average returns an image's average colour as #rrggbb (sampled).
func Average(img image.Image) string {
	b := img.Bounds()
	var r, g, bl, n uint64
	stepX, stepY := max(1, b.Dx()/32), max(1, b.Dy()/32)
	for y := b.Min.Y; y < b.Max.Y; y += stepY {
		for x := b.Min.X; x < b.Max.X; x += stepX {
			cr, cg, cb, _ := img.At(x, y).RGBA()
			r, g, bl, n = r+uint64(cr>>8), g+uint64(cg>>8), bl+uint64(cb>>8), n+1
		}
	}
	if n == 0 {
		return ""
	}
	return fmt.Sprintf("#%02x%02x%02x", r/n, g/n, bl/n)
}

func writeAtomic(path string, b []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
