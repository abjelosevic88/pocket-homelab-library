package metadata

import (
	"context"
	"os"
	"testing"
)

func TestConfident(t *testing.T) {
	c := Candidate{Title: "Na Drini Ćuprija", Authors: []string{"Ivo Andric"}}
	if !Confident(c, "Na Drini ćuprija", []string{"Ivo Andrić"}) {
		t.Error("same book not recognised")
	}
	if Confident(c, "Prokleta avlija", []string{"Ivo Andrić"}) {
		t.Error("another title taken")
	}
}

// TestLive asks the real catalogues when PLB_NET is set.
func TestLive(t *testing.T) {
	if os.Getenv("PLB_NET") == "" {
		t.Skip("set PLB_NET=1")
	}
	c := New("PocketLibrary-test/1.0")
	ctx := context.Background()
	cands, warn, err := c.Search(ctx, "The Hobbit Tolkien", "", "", 5)
	if err != nil || len(cands) == 0 {
		t.Fatal(err, warn)
	}
	t.Logf("%+v", cands[0])
	byISBN, _, err := c.Search(ctx, "", "9780261103344", "", 3)
	if err != nil || len(byISBN) == 0 {
		t.Fatal(err)
	}
	t.Logf("isbn: %s / %s / %s / %d", byISBN[0].Title, byISBN[0].Publisher, byISBN[0].Published, byISBN[0].Pages)
	a, err := c.Author(ctx, "Ivo Andrić")
	if err != nil || a == nil {
		t.Fatal(err)
	}
	t.Logf("author %s photo=%q bio=%.80q links=%v", a.Key, a.Photo, a.Bio, a.Links)
}
