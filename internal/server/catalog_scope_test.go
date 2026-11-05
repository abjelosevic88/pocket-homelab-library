package server

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"net/http"
	"testing"

	"github.com/abjelosevic88/pocket-homelab-library/internal/store"
)

func TestCatalogScopeAndReview(t *testing.T) {
	w := newSyncWorld(t)
	ctx := context.Background()
	titles := func(path string) []string {
		t.Helper()
		res, body := w.basic("GET", path, "ana@example.com", w.anaPass, nil)
		if res.StatusCode != 200 {
			t.Fatalf("%s: %d", path, res.StatusCode)
		}
		var f testFeed
		if err := xml.Unmarshal(body, &f); err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		out := []string{}
		for _, e := range f.Entries {
			out = append(out, e.Title)
		}
		return out
	}
	if got := titles("/opds/recent"); len(got) != 2 {
		t.Fatalf("everything: %v", got)
	}

	// kept off e-readers: gone from every list, search included
	if err := w.st.SetEReaderOff(ctx, []int64{w.pdfBook}, true); err != nil {
		t.Fatal(err)
	}
	if got := titles("/opds/recent"); len(got) != 1 || got[0] != "1984" {
		t.Fatalf("kept off: %v", got)
	}
	if got := titles("/opds/search?q=Manual"); len(got) != 0 {
		t.Fatalf("kept off, searched: %v", got)
	}
	_ = w.st.SetEReaderOff(ctx, []int64{w.pdfBook}, false)

	// one shelf: only its books, and only their authors
	sh := &store.Shelf{Name: "E-reader", Kind: "manual"}
	if err := w.st.CreateShelf(ctx, w.ana.ID, sh); err != nil {
		t.Fatal(err)
	}
	if err := w.st.AddToShelf(ctx, w.ana.ID, sh.ID, []int64{w.pdfBook}); err != nil {
		t.Fatal(err)
	}
	setCatalog := func(v string) {
		t.Helper()
		w.device.Settings = map[string]any{"catalog": v}
		if err := w.st.UpdateDevice(ctx, w.ana.ID, w.device); err != nil {
			t.Fatal(err)
		}
	}
	setCatalog(fmt.Sprintf("shelf:%d", sh.ID))
	if got := titles("/opds/recent"); len(got) != 1 || got[0] != "Manual" {
		t.Fatalf("shelf scope: %v", got)
	}
	if got := titles("/opds/search?q=1984"); len(got) != 0 {
		t.Fatalf("shelf scope, searched: %v", got)
	}
	if got := titles("/opds/authors/letter/G"); len(got) != 0 {
		t.Fatalf("shelf scope, authors: %v", got)
	}
	// a library that isn't there shows nothing, not everything
	setCatalog("library:999")
	if got := titles("/opds/recent"); len(got) != 0 {
		t.Fatalf("missing library: %v", got)
	}
	setCatalog("")
	if got := titles("/opds/recent"); len(got) != 2 {
		t.Fatalf("back to everything: %v", got)
	}

	// review: Manual has no author, neither has a cover; members can't look
	tok, _, _ := w.st.CreateAPIToken(ctx, w.ana.ID, "t", "token")
	bobTok, _, _ := w.st.CreateAPIToken(ctx, w.bob.ID, "t", "token")
	get := func(tok, path string) (int, map[string]any) {
		req, _ := http.NewRequest("GET", w.srv.URL+path, nil)
		req.Header.Set("Authorization", "Bearer "+tok)
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		out := map[string]any{}
		_ = json.NewDecoder(res.Body).Decode(&out)
		return res.StatusCode, out
	}
	if code, _ := get(bobTok, "/api/v1/review"); code != http.StatusForbidden {
		t.Fatalf("member reviewed: %d", code)
	}
	code, rv := get(tok, "/api/v1/review?flag=no_author")
	counts, _ := rv["counts"].(map[string]any)
	items, _ := rv["items"].([]any)
	if code != 200 || counts["no_author"] != float64(1) || counts["no_cover"] != float64(2) || len(items) != 1 {
		t.Fatalf("review: %d %v", code, rv)
	}
	if it := items[0].(map[string]any); it["title"] != "Manual" || it["ereader_off"] != false {
		t.Fatalf("review item: %v", it)
	}
}
