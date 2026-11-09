package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	core "github.com/abjelosevic88/pocket-homelab-core/sdk"
	"github.com/abjelosevic88/pocket-homelab-library/internal/epub"
	"github.com/abjelosevic88/pocket-homelab-library/internal/store"
)

const importSource = "https://www.balkandownload.org/topic/12-author/#entry123"

func balkanFixture(t *testing.T) (*devFixture, []byte) {
	t.Helper()
	f := newDevFixture(t)
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/auth/introspect" {
			http.NotFound(w, r)
			return
		}
		var in map[string]string
		_ = json.NewDecoder(r.Body).Decode(&in)
		token := in["token"]
		person := f.bob
		if strings.Contains(token, "ana") {
			person = f.ana
		}
		actor, audience, scope := "books", "library", "library:import"
		if token == "pcx_other" {
			actor = "notes"
		}
		if token == "pcx_audience" {
			audience = "bookshelf"
		}
		if token == "pcx_readonly" {
			scope = "library:read"
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"active": token != "pcx_revoked", "kind": "delegated", "scope": scope, "audience": audience,
			"user": map[string]any{"id": person.ID, "email": person.Email, "name": person.Name}, "app": map[string]any{"id": 100, "slug": actor}})
	}))
	t.Cleanup(fake.Close)
	f.s.core = core.New(fake.URL, core.Options{DB: f.st.DB, Token: "synthetic-application-token"})
	data, err := epub.Minimal("Synthetic imported title", "Test author", "en", "Only synthetic ebook content.")
	if err != nil {
		t.Fatal(err)
	}
	return f, data
}

func uploadBalkan(t *testing.T, f *devFixture, token, name, source string, data []byte) (int, balkanImportResult, []byte) {
	t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	_ = mw.WriteField("title", "Synthetic imported title")
	_ = mw.WriteField("source_url", source)
	h := sha256.Sum256(data)
	_ = mw.WriteField("idempotency_key", "books:synthetic-user:"+hex.EncodeToString(h[:]))
	p, err := mw.CreateFormFile("file", name)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = p.Write(data)
	_ = mw.Close()
	r, _ := http.NewRequest("POST", f.srv.URL+"/api/v1/imports/balkan-books", &body)
	r.Header.Set("Authorization", "Bearer "+token)
	r.Header.Set("Content-Type", mw.FormDataContentType())
	res, err := http.DefaultClient.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	var result balkanImportResult
	_ = json.Unmarshal(b, &result)
	return res.StatusCode, result, b
}

func TestBalkanImportRequiresOnlyTheAuthorizedBooksLink(t *testing.T) {
	f, data := balkanFixture(t)
	for _, tok := range []string{f.anaTok, f.bobTok, "pcx_other", "pcx_audience", "pcx_readonly"} {
		code, _, body := uploadBalkan(t, f, tok, "book.epub", importSource, data)
		if code != 403 {
			t.Fatalf("%s: %d %s", tok, code, body)
		}
	}
	if code, _, _ := uploadBalkan(t, f, "pcx_revoked", "book.epub", importSource, data); code != 401 {
		t.Fatal(code)
	}
	r, _ := http.NewRequest("POST", "/api/v1/imports/balkan-books", nil)
	if linkAllows([]string{"library:read", "reading:read"}, r) || !linkAllows([]string{"library:import"}, r) {
		t.Fatal("import scope expanded existing read access")
	}
	for _, path := range []string{"/api/v1/upload", "/api/v1/shelves", "/api/v1/libraries"} {
		r, _ := http.NewRequest("POST", path, nil)
		if linkAllows([]string{"library:import"}, r) {
			t.Fatalf("import scope grants broad mutation: %s", path)
		}
	}
}

func TestBalkanImportHashesBytesAndKeepsPeoplePrivate(t *testing.T) {
	f, data := balkanFixture(t)
	code, first, body := uploadBalkan(t, f, "pcx_books_bob", "book.epub", importSource+"?not-a-query", data)
	if code != 400 {
		t.Fatalf("malformed post fragment: %d %s", code, body)
	}
	code, first, body = uploadBalkan(t, f, "pcx_books_bob", "book.epub", "http://balkandownload.org/topic/12-author/?s=private-token#entry123", data)
	if code != 201 || first.BookID == 0 || first.Duplicate || first.Collection != balkanCollection || first.SourceURL != importSource {
		t.Fatalf("import: %d %s", code, body)
	}
	h := sha256.Sum256(data)
	if first.SHA256 != hex.EncodeToString(h[:]) {
		t.Fatal("receiver did not hash file bytes")
	}
	file, err := f.st.GetFile(t.Context(), first.FileID)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(file.Path)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("private file permissions", err)
	}
	lib, _ := f.st.GetLibrary(t.Context(), file.LibraryID)
	if lib.OwnerUserID != f.bob.ID || lib.Access != "people" || len(lib.People) != 1 || lib.People[0] != f.bob.ID || !strings.HasPrefix(file.Path, f.s.importRoot()+string(os.PathSeparator)) {
		t.Fatalf("wrong owner/destination: %+v", lib)
	}
	for _, path := range []string{fmt.Sprintf("/api/v1/books/%d", first.BookID), fmt.Sprintf("/api/v1/files/%d/content", first.FileID), fmt.Sprintf("/api/v1/files/%d/download", first.FileID), fmt.Sprintf("/api/v1/shelves/%d", first.CollectionID)} {
		if got, _, _ := f.do(f.anaTok, "GET", path, nil); got != 404 {
			t.Fatalf("other admin sees private data: %s = %d", path, got)
		}
	}
	var page struct {
		Items []store.Book `json:"items"`
	}
	f.json(f.anaTok, "GET", "/api/v1/books?q=Synthetic", nil, 200, &page)
	if len(page.Items) != 0 {
		t.Fatal("private book leaked into admin catalogue")
	}
	var detail map[string]any
	f.json(f.bobTok, "GET", fmt.Sprintf("/api/v1/books/%d", first.BookID), nil, 200, &detail)
	provenance, ok := detail["provenance"].(map[string]any)
	if !ok || provenance["source_url"] != importSource || provenance["source_app"] != "books" {
		t.Fatalf("missing own provenance: %v", detail)
	}
	code, second, body := uploadBalkan(t, f, "pcx_books_ana", "book.epub", importSource, data)
	if code != 201 || second.BookID == first.BookID || second.FileID == first.FileID || second.CollectionID == first.CollectionID {
		t.Fatalf("content dedup crossed people: %d %s", code, body)
	}
	if got, _, _ := f.do(f.bobTok, "GET", fmt.Sprintf("/api/v1/books/%d", second.BookID), nil); got != 404 {
		t.Fatal("other person could open imported book", got)
	}
	if got, content, _ := f.do(f.bobTok, "GET", fmt.Sprintf("/api/v1/files/%d/content", first.FileID), nil); got != 200 || !bytes.Equal(content, data) {
		t.Fatal("own file bytes lost", got)
	}
}

func TestBalkanImportRetryAndInterruptedReceiptStayIdempotent(t *testing.T) {
	f, data := balkanFixture(t)
	code, first, body := uploadBalkan(t, f, "pcx_books_bob", "first.epub", importSource, data)
	if code != 201 {
		t.Fatalf("%d %s", code, body)
	}
	code, again, body := uploadBalkan(t, f, "pcx_books_bob", "renamed.epub", importSource, data)
	if code != 200 || !again.Duplicate || again.BookID != first.BookID || again.FileID != first.FileID {
		t.Fatalf("retry: %d %s", code, body)
	}
	// Model a stop after registering complete bytes and catalogue rows but before the receipt.
	if _, err := f.st.DB.ExecContext(t.Context(), `DELETE FROM linked_book_imports WHERE user_id=?`, f.bob.ID); err != nil {
		t.Fatal(err)
	}
	code, again, body = uploadBalkan(t, f, "pcx_books_bob", "another-name.epub", importSource, data)
	if code != 201 || again.BookID != first.BookID || again.FileID != first.FileID {
		t.Fatalf("interrupted receipt: %d %s", code, body)
	}
	lib, _ := f.st.ImportedLibrary(t.Context(), f.bob.ID)
	files, _ := os.ReadDir(lib.Paths[0])
	if len(files) != 1 {
		t.Fatalf("retry duplicated files: %v", files)
	}
	var count int
	_ = f.st.DB.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM books WHERE library_id=?`, lib.ID).Scan(&count)
	if count != 1 {
		t.Fatal("retry duplicated books", count)
	}
	_ = f.st.DB.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM shelf_books WHERE shelf_id=?`, first.CollectionID).Scan(&count)
	if count != 1 {
		t.Fatal("retry duplicated shelf membership", count)
	}
}

func TestBalkanImportRejectsTraversalFakeFormatsAndOversize(t *testing.T) {
	f, data := balkanFixture(t)
	for _, tc := range []struct {
		name, source string
		data         []byte
	}{
		{"../escape.epub", importSource, data}, {`..\escape.epub`, importSource, data}, {"book.epub", importSource, []byte("<html>sign in</html>")},
		{"book.zip", importSource, data}, {"book.epub", "https://other.example/topic/12/#entry1", data}, {"book.epub", "https://www.balkandownload.org/topic/12/", data},
	} {
		if code, _, body := uploadBalkan(t, f, "pcx_books_bob", tc.name, tc.source, tc.data); code != 400 {
			t.Fatalf("%s: %d %s", tc.name, code, body)
		}
	}
	f.s.cfg.MaxBookUploadBytes = 32
	if code, _, _ := uploadBalkan(t, f, "pcx_books_bob", "book.epub", importSource, data); code != 413 {
		t.Fatal("size limit", code)
	}
	if _, err := f.st.ImportedLibrary(t.Context(), f.bob.ID); !errorsIsNotFound(err) {
		t.Fatal("invalid file created a private library", err)
	}
}

func TestBalkanImportRecoversBeforeCatalogueAndAcrossExtensions(t *testing.T) {
	f, _ := balkanFixture(t)
	data := make([]byte, 100)
	copy(data[60:], "BOOKMOBI")
	code, first, body := uploadBalkan(t, f, "pcx_books_bob", "book.mobi", importSource, data)
	if code != 201 {
		t.Fatalf("first import: %d %s", code, body)
	}
	// Keep synced bytes but remove the catalogue rows and cascading receipt.
	if _, err := f.st.DB.ExecContext(t.Context(), `DELETE FROM books WHERE id=?`, first.BookID); err != nil {
		t.Fatal(err)
	}
	code, again, body := uploadBalkan(t, f, "pcx_books_bob", "book.azw3", importSource, data)
	if code != 201 {
		t.Fatalf("recovered import: %d %s", code, body)
	}
	file, err := f.st.GetFile(t.Context(), again.FileID)
	if err != nil || filepath.Ext(file.Path) != ".mobi" {
		t.Fatal("did not reuse complete original bytes", file, err)
	}
	lib, _ := f.st.ImportedLibrary(t.Context(), f.bob.ID)
	files, _ := os.ReadDir(lib.Paths[0])
	if len(files) != 1 {
		t.Fatal("equivalent extension duplicated bytes", files)
	}
}

func TestBalkanImportPreservesAnExistingSmartShelf(t *testing.T) {
	f, data := balkanFixture(t)
	shelf := &store.Shelf{Name: balkanCollection, Kind: "smart", Filter: `{"q":"existing"}`}
	if err := f.st.CreateShelf(t.Context(), f.bob.ID, shelf); err != nil {
		t.Fatal(err)
	}
	if code, _, body := uploadBalkan(t, f, "pcx_books_bob", "book.epub", importSource, data); code != 409 {
		t.Fatalf("smart shelf conflict: %d %s", code, body)
	}
	if _, err := f.st.ImportedLibrary(t.Context(), f.bob.ID); !errorsIsNotFound(err) {
		t.Fatal("conflicting shelf created private library", err)
	}
}

func errorsIsNotFound(err error) bool { return err == store.ErrNotFound }

func TestBalkanImportDestinationCannotBeSharedOrMoved(t *testing.T) {
	f, data := balkanFixture(t)
	code, imported, body := uploadBalkan(t, f, "pcx_books_bob", "book.epub", importSource, data)
	if code != 201 {
		t.Fatalf("%d %s", code, body)
	}
	lib, _ := f.st.ImportedLibrary(t.Context(), f.bob.ID)
	for _, method := range []string{"PUT", "DELETE"} {
		if code, _, _ := f.do(f.anaTok, method, fmt.Sprintf("/api/v1/libraries/%d", lib.ID), map[string]any{"name": "Exposed", "access": "all", "paths": lib.Paths}); code != 404 {
			t.Fatal("admin changed personal library", method, code)
		}
	}
	if code, _, _ := f.do(f.anaTok, "POST", fmt.Sprintf("/api/v1/libraries/%d/scan", lib.ID), nil); code != 404 {
		t.Fatal("admin scanned other's private files", code)
	}
	for _, path := range []string{f.s.importRoot(), lib.Paths[0], f.dir, filepath.Join(lib.Paths[0], "nested")} {
		if code, _, _ := f.do(f.anaTok, "POST", "/api/v1/libraries", map[string]any{"name": "Exposure", "paths": []string{path}, "access": "all"}); code != 400 {
			t.Fatal("overlapping shared library accepted", path, code)
		}
	}
	alias := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(f.s.importRoot(), alias); err != nil {
		t.Fatal(err)
	}
	if code, _, _ := f.do(f.anaTok, "POST", "/api/v1/libraries", map[string]any{"name": "Alias", "paths": []string{filepath.Join(alias, "future")}}); code != 400 {
		t.Fatal("symlink overlap accepted", code)
	}
	var moved map[string]any
	shared, _ := f.st.UploadLibrary(t.Context())
	f.json(f.anaTok, "POST", "/api/v1/books/move", map[string]any{"ids": []int64{imported.BookID}, "library_id": shared.ID}, 200, &moved)
	if moved["moved"] != float64(0) {
		t.Fatal("moved another person's imported file", moved)
	}
	f.s.scan.status = ScanStatus{LibraryID: lib.ID, Library: balkanCollection, Errors: []string{"private filename"}}
	var scan map[string]any
	f.json(f.anaTok, "GET", "/api/v1/scan", nil, 200, &scan)
	if status := scan["status"].(map[string]any); status["library_id"] != float64(0) {
		t.Fatal("private scan leaked", status)
	}
	libs, err := f.st.LibrariesFor(context.Background(), f.ana)
	if err != nil {
		t.Fatal(err)
	}
	for _, visible := range libs {
		if visible.ID == lib.ID {
			t.Fatal("admin bypasses personal import ownership")
		}
	}
}
