package store

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
	"sync"
)

// Tag rules are the box's clean-up of tags that came with the files
// (uploader names, converter keywords, a collection's name on every book):
// a removed tag is blocked, a merged or renamed one is redirected. Every write
// of a book's metadata goes through them, so a scan never brings a removed tag
// back and a new file arrives with the tidy name.

// TagRules is the box's blocked tags and renames (old name → new name).
type TagRules struct {
	Blocked []string          `json:"blocked"`
	Renames map[string]string `json:"renames"`
}

type tagRuleCache struct {
	mu      sync.Mutex
	loaded  bool
	blocked map[string]bool
	renames map[string]string
}

var tagCache sync.Map // *Store → *tagRuleCache

func (s *Store) tagRules(ctx context.Context) *tagRuleCache {
	v, _ := tagCache.LoadOrStore(s, &tagRuleCache{})
	c := v.(*tagRuleCache)
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.loaded {
		r := s.readTagRules(ctx)
		c.blocked, c.renames = map[string]bool{}, map[string]string{}
		for _, b := range r.Blocked {
			c.blocked[strings.ToLower(b)] = true
		}
		for k, v := range r.Renames {
			c.renames[strings.ToLower(k)] = v
		}
		c.loaded = true
	}
	return c
}

func (s *Store) readTagRules(ctx context.Context) TagRules {
	r := TagRules{Blocked: []string{}, Renames: map[string]string{}}
	var raw string
	if err := s.DB.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = 'tag_rules'`).Scan(&raw); err == nil {
		_ = json.Unmarshal([]byte(raw), &r)
	}
	if r.Blocked == nil {
		r.Blocked = []string{}
	}
	if r.Renames == nil {
		r.Renames = map[string]string{}
	}
	return r
}

func (s *Store) saveTagRules(ctx context.Context, r TagRules) error {
	sort.Slice(r.Blocked, func(i, j int) bool { return strings.ToLower(r.Blocked[i]) < strings.ToLower(r.Blocked[j]) })
	b, _ := json.Marshal(r)
	_, err := s.DB.ExecContext(ctx, `INSERT INTO settings (key, value) VALUES ('tag_rules', ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value`, string(b))
	tagCache.Delete(s)
	return err
}

// GetTagRules returns the box's tag rules.
func (s *Store) GetTagRules(ctx context.Context) TagRules { return s.readTagRules(ctx) }

// applyTagRules drops blocked tags and renames redirected ones.
func (s *Store) applyTagRules(ctx context.Context, tags []string) []string {
	if len(tags) == 0 {
		return tags
	}
	c := s.tagRules(ctx)
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []string
	for _, t := range tags {
		k := strings.ToLower(strings.TrimSpace(t))
		for i := 0; i < 5; i++ { // follow a chain of renames, not a loop
			n, ok := c.renames[k]
			if !ok {
				break
			}
			t, k = n, strings.ToLower(n)
		}
		if !c.blocked[k] {
			out = append(out, t)
		}
	}
	return cleanList(out)
}

// booksWithTags returns the books carrying any of the tags.
func (s *Store) booksWithTags(ctx context.Context, names []string) ([]int64, error) {
	if len(names) == 0 {
		return nil, nil
	}
	q := `SELECT DISTINCT book_id FROM book_tags WHERE tag IN (?` + strings.Repeat(",?", len(names)-1) + `)`
	args := make([]any, len(names))
	for i, n := range names {
		args[i] = n
	}
	return s.queryIDs(ctx, q, args...)
}

// RemoveTags takes tags off every book and blocks them from coming back.
// It returns how many books changed.
func (s *Store) RemoveTags(ctx context.Context, names []string) (int, error) {
	names = cleanList(names)
	ids, err := s.booksWithTags(ctx, names)
	if err != nil {
		return 0, err
	}
	r := s.readTagRules(ctx)
	for _, n := range names {
		r.Blocked = cleanList(append(r.Blocked, n))
		for k, v := range r.Renames {
			if strings.EqualFold(v, n) {
				delete(r.Renames, k) // nothing should be renamed into a blocked tag
			}
		}
	}
	if err := s.saveTagRules(ctx, r); err != nil {
		return 0, err
	}
	for _, n := range names {
		if _, err := s.DB.ExecContext(ctx, `DELETE FROM book_tags WHERE tag = ?`, n); err != nil {
			return 0, err
		}
	}
	for _, id := range ids {
		_ = s.RefreshFTS(ctx, id)
	}
	return len(ids), nil
}

// MergeTags renames tags into one (an existing tag or a new name) on every
// book, and remembers it for books that arrive later. It returns how many
// books changed.
func (s *Store) MergeTags(ctx context.Context, names []string, into string) (int, error) {
	into = strings.TrimSpace(into)
	names = cleanList(names)
	ids, err := s.booksWithTags(ctx, names)
	if err != nil {
		return 0, err
	}
	r := s.readTagRules(ctx)
	var blocked []string
	for _, b := range r.Blocked {
		if !strings.EqualFold(b, into) {
			blocked = append(blocked, b) // merging into a tag allows it again
		}
	}
	r.Blocked = blocked
	for _, n := range names {
		if n != into {
			r.Renames[n] = into
		}
	}
	delete(r.Renames, into) // never redirect the target itself
	if err := s.saveTagRules(ctx, r); err != nil {
		return 0, err
	}
	for _, n := range names {
		if n == into {
			continue
		}
		if _, err := s.DB.ExecContext(ctx, `INSERT OR IGNORE INTO book_tags (book_id, tag) SELECT book_id, ? FROM book_tags WHERE tag = ?`, into, n); err != nil {
			return 0, err
		}
		if _, err := s.DB.ExecContext(ctx, `DELETE FROM book_tags WHERE tag = ? AND tag <> ?`, n, into); err != nil {
			return 0, err
		}
	}
	// a case-only rename: the row above was ignored as the same tag (NOCASE); set the spelling
	if _, err := s.DB.ExecContext(ctx, `UPDATE book_tags SET tag = ? WHERE tag = ?`, into, into); err != nil {
		return 0, err
	}
	for _, id := range ids {
		_ = s.RefreshFTS(ctx, id)
	}
	return len(ids), nil
}

// AllowTag lifts a block or a rename on a tag (books get it again on their next scan).
func (s *Store) AllowTag(ctx context.Context, name string) error {
	r := s.readTagRules(ctx)
	var blocked []string
	for _, b := range r.Blocked {
		if !strings.EqualFold(b, name) {
			blocked = append(blocked, b)
		}
	}
	r.Blocked = blocked
	for k := range r.Renames {
		if strings.EqualFold(k, name) {
			delete(r.Renames, k)
		}
	}
	return s.saveTagRules(ctx, r)
}
