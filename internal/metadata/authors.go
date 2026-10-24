package metadata

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
)

// AuthorInfo is what Open Library knows about an author.
type AuthorInfo struct {
	Key   string // "OL23919A"
	Name  string
	Bio   string
	Photo string            // remote URL ('' = none)
	Links map[string]string // wikipedia, website, openlibrary …
}

// Author finds an author by name in Open Library: among the records with
// that name the one with most works (empty duplicates are common), then its
// biography, photo and links.
func (c *Client) Author(ctx context.Context, name string) (*AuthorInfo, error) {
	v := url.Values{}
	v.Set("q", name)
	v.Set("limit", "10")
	var res struct {
		Docs []struct {
			Key       string   `json:"key"`
			Name      string   `json:"name"`
			Alternate []string `json:"alternate_names"`
			WorkCount int      `json:"work_count"`
		} `json:"docs"`
	}
	if err := c.getJSON(ctx, olBase+"/search/authors.json?"+v.Encode(), &res); err != nil {
		return nil, err
	}
	want := Fold(name)
	best, bestWorks := "", -1
	for _, d := range res.Docs {
		match := Fold(d.Name) == want
		for _, a := range d.Alternate {
			if Fold(a) == want {
				match = true
			}
		}
		if match && d.WorkCount > bestWorks {
			best, bestWorks = d.Key, d.WorkCount
		}
	}
	if best == "" {
		return nil, nil
	}
	return c.AuthorDetails(ctx, best)
}

// AuthorDetails reads one Open Library author record.
func (c *Client) AuthorDetails(ctx context.Context, key string) (*AuthorInfo, error) {
	key = strings.TrimPrefix(strings.TrimPrefix(key, "/authors/"), "/")
	var a struct {
		Name   string          `json:"name"`
		Bio    json.RawMessage `json:"bio"`
		Photos []int           `json:"photos"`
		Wiki   string          `json:"wikipedia"`
		Links  []struct {
			Title string `json:"title"`
			URL   string `json:"url"`
		} `json:"links"`
		RemoteIDs map[string]string `json:"remote_ids"`
	}
	if err := c.getJSON(ctx, olBase+"/authors/"+url.PathEscape(key)+".json", &a); err != nil {
		return nil, err
	}
	info := &AuthorInfo{Key: key, Name: a.Name, Bio: PlainText(stripOLSources(olText(a.Bio))), Links: map[string]string{"openlibrary": olBase + "/authors/" + key}}
	for _, p := range a.Photos {
		if p > 0 {
			info.Photo = fmt.Sprintf("https://covers.openlibrary.org/a/id/%d-L.jpg", p)
			break
		}
	}
	if a.Wiki != "" {
		info.Links["wikipedia"] = a.Wiki
	}
	for _, l := range a.Links {
		t := strings.ToLower(l.Title)
		switch {
		case strings.Contains(l.URL, "wikipedia.org") && info.Links["wikipedia"] == "":
			info.Links["wikipedia"] = l.URL
		case strings.Contains(t, "official") || strings.Contains(t, "website") || strings.Contains(t, "homepage"):
			info.Links["website"] = l.URL
		}
	}
	if q := a.RemoteIDs["wikidata"]; q != "" {
		info.Links["wikidata"] = "https://www.wikidata.org/wiki/" + q
	}
	return info, nil
}
