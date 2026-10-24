package crosspoint

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestUploadWebDAV(t *testing.T) {
	var got []byte
	var path string
	dirs := map[string]bool{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case "MKCOL":
			dirs[r.URL.Path] = true
			w.WriteHeader(201)
		case http.MethodPut:
			path = r.URL.EscapedPath()
			got, _ = io.ReadAll(r.Body)
			w.WriteHeader(201)
		case http.MethodGet:
			w.Write([]byte(`{"version":"1.6.5","mode":"STA","device":"X4","ip":"10.0.0.7"}`))
		}
	}))
	defer srv.Close()
	c, err := New(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	st, err := c.Status(context.Background())
	if err != nil || st.Version != "1.6.5" || st.Device != "X4" {
		t.Fatalf("status %+v %v", st, err)
	}
	if err := c.Upload(context.Background(), "/Books/Pocket", "Ivo Andrić - Na Drini ćuprija.epub", []byte("EPUB")); err != nil {
		t.Fatal(err)
	}
	if string(got) != "EPUB" || path != "/Books/Pocket/Ivo%20Andri%C4%87%20-%20Na%20Drini%20%C4%87uprija.epub" || !dirs["/Books"] || !dirs["/Books/Pocket"] {
		t.Fatalf("got %q at %q, dirs %v", got, path, dirs)
	}
	if err := c.Upload(context.Background(), "/Books", "a:b.epub", nil); err == nil {
		t.Fatal("colon accepted")
	}
}

func TestUploadFormFallback(t *testing.T) {
	var name, dir string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPut || r.Method == "MKCOL":
			w.WriteHeader(405)
		case r.Method == http.MethodPost && r.URL.Path == "/upload":
			dir = r.URL.Query().Get("path")
			f, h, err := r.FormFile("file")
			if err != nil {
				w.WriteHeader(400)
				return
			}
			f.Close()
			name = h.Filename
			w.Write([]byte("File uploaded successfully: " + name))
		}
	}))
	defer srv.Close()
	c, _ := New(srv.URL)
	if err := c.Upload(context.Background(), "Books", "x.epub", []byte("E")); err != nil {
		t.Fatal(err)
	}
	if name != "x.epub" || dir != "/Books" {
		t.Fatalf("%q %q", name, dir)
	}
}

func TestUnreachable(t *testing.T) {
	c, _ := New("127.0.0.1:1")
	if _, err := c.Status(context.Background()); !errors.Is(err, ErrUnreachable) {
		t.Fatalf("err = %v", err)
	}
}
