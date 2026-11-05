package server

// Pocket AI sorts new books (uploads, the ingest folder, scans; or books a
// person asks about): each gets a suggested home, the main library ("Books"),
// a tech library, or the review library, with a reason, shown on the Review
// page for an admin to apply (the book moves, keeping its id) or to keep
// where it is. The AI is told what the checks already found (a junk title,
// an uploader as author, a copy already in the main library), and answers in
// JSON, ten books a request, as the person who added the books (or an admin).

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/abjelosevic88/pocket-homelab-library/internal/store"
)

// aiModel answers a prompt (Pocket AI through Core; tests use a fake).
type aiModel interface {
	Complete(ctx context.Context, system, prompt string, jsonMode bool) (string, error)
}

type sortState struct {
	mu   sync.Mutex // one round at a time
	wake chan struct{}
	// model is who answers for a person; nil = Pocket AI through Core.
	model func(ctx context.Context, email string) (aiModel, error)
}

func newSortState() *sortState { return &sortState{wake: make(chan struct{}, 1)} }

func (s *Server) routesSort(api chi.Router) {
	api.Get("/review/imports", s.handleSortList)
	api.Post("/review/imports/apply", s.handleSortApply)
	api.Post("/review/imports/keep", s.handleSortKeep)
	api.Post("/review/imports/ask", s.handleSortAsk)
}

func (s *Server) kickSort() {
	select {
	case s.sorting.wake <- struct{}{}:
	default:
	}
}

// queueSort asks Pocket AI about books that just came in, if they landed in
// a library the sorting knows (main, review, tech).
func (s *Server) queueSort(ctx context.Context, ids []int64, source string, by int64) {
	if len(ids) == 0 {
		return
	}
	main, review, tech := s.cleanupLibraries(ctx)
	known := map[int64]bool{}
	for _, l := range []*store.Library{main, review, tech} {
		if l != nil {
			known[l.ID] = true
		}
	}
	keep := []int64{}
	for _, id := range ids {
		if b, err := s.store.GetBookAny(ctx, id); err == nil && known[b.LibraryID] && b.Kind == "book" {
			keep = append(keep, id)
		}
	}
	if len(keep) == 0 {
		return
	}
	if err := s.store.QueueSort(ctx, keep, source, by, false); err != nil {
		s.log.Warn("sort: queue", "err", err)
		return
	}
	s.kickSort()
}

func (s *Server) sortModel(ctx context.Context, email string) (aiModel, error) {
	if s.sorting.model != nil {
		return s.sorting.model(ctx, email)
	}
	if !s.coreConnected() {
		return nil, errors.New("Pocket AI needs Pocket Core")
	}
	pocket, status := s.core.SelectAI(ctx, email, "pocket", false)
	if pocket == nil || !status.Ready {
		return nil, errors.New("Pocket AI: " + status.Detail)
	}
	return pocketModel{pocket}, nil
}

func (s *Server) sortWorker(ctx context.Context) {
	for {
		for s.sortRound(ctx) {
		}
		select {
		case <-ctx.Done():
			return
		case <-s.sorting.wake:
		case <-time.After(2 * time.Minute):
		}
	}
}

// sortRound sorts up to ten waiting books; true when it did some (go on).
func (s *Server) sortRound(ctx context.Context) bool {
	s.sorting.mu.Lock()
	defer s.sorting.mu.Unlock()
	items, err := s.store.PendingSort(ctx, 10)
	if err != nil || len(items) == 0 {
		return false
	}
	// as the person who added them, else the first admin
	email := ""
	users, _ := s.store.ListUsers(ctx)
	for _, u := range users {
		if u.ID == items[0].RequestedBy {
			email = u.Email
		}
	}
	for _, u := range users {
		if email == "" && u.Role == "admin" {
			email = u.Email
		}
	}
	model, err := s.sortModel(ctx, email)
	if err != nil {
		// no AI now: leave them waiting, try again later
		s.log.Debug("sort: no AI", "err", err)
		return false
	}
	answers, err := s.askSort(ctx, model, items)
	for _, it := range items {
		a, ok := answers[it.BookID]
		switch {
		case err != nil:
			_ = s.store.SaveSort(ctx, it.BookID, "failed", "", "", 0, err.Error())
		case !ok:
			_ = s.store.SaveSort(ctx, it.BookID, "failed", "", "", 0, "Pocket AI did not answer for this book")
		default:
			_ = s.store.SaveSort(ctx, it.BookID, "suggested", a.Target, a.Reason, a.Confidence, "")
		}
	}
	return err == nil
}

type sortAnswer struct {
	ID         int64   `json:"id"`
	Target     string  `json:"target"`
	Confidence float64 `json:"confidence"`
	Reason     string  `json:"reason"`
}

const sortSystem = `You sort e-books into a home library. For each book decide where it belongs:
- "books": a real book worth reading on an e-reader (fiction, non-fiction, poetry, children's, history, religion, any language) with a sensible title and a real author.
- "tech": programming, software, IT, engineering manuals, or textbooks for learning a language (grammar, verbs, vocabulary).
- "review": anything that should not be on the shelf as it is: the title is a file name, a printer or scan job, an uploader's nickname or a number; the author is missing or is clearly a nickname; title and author look swapped; it is a duplicate of a book already in "books"; it is a fragment, an article, a web page, a catalogue or junk.
Books are often in Serbian, Croatian or Bosnian (Latin or Cyrillic) or English; transliterated foreign names are normal (Džordž Orvel = George Orwell) and are not a problem.
Use the facts given ("checks"). Answer only JSON: {"books":[{"id":<id>,"target":"books|tech|review","confidence":<0..1>,"reason":"<at most 12 words, English>"}]} with one entry per book.`

func (s *Server) askSort(ctx context.Context, model aiModel, items []store.SortItem) (map[int64]sortAnswer, error) {
	main, review, tech := s.cleanupLibraries(ctx)
	libName := map[int64]string{}
	for _, l := range []*store.Library{main, review, tech} {
		if l != nil {
			libName[l.ID] = l.Name
		}
	}
	// the checks the Review page makes, and copies already in the main library
	var mainBooks []store.ReviewBook
	if main != nil {
		mainBooks, _ = s.store.ReviewBooks(ctx, []int64{main.ID})
	}
	people := authorBooks(mainBooks)
	inMain := map[string]bool{}
	for _, b := range mainBooks {
		inMain[fold(b.Title)+"|"+surnames(b.Authors)] = true
	}
	type promptBook struct {
		ID          int64    `json:"id"`
		Title       string   `json:"title"`
		Authors     []string `json:"authors"`
		Language    string   `json:"language,omitempty"`
		Publisher   string   `json:"publisher,omitempty"`
		Published   string   `json:"published,omitempty"`
		Pages       int      `json:"pages,omitempty"`
		Tags        []string `json:"tags,omitempty"`
		Description string   `json:"description,omitempty"`
		File        string   `json:"file,omitempty"`
		Library     string   `json:"now_in"`
		Checks      []string `json:"checks,omitempty"`
	}
	list := []promptBook{}
	for _, it := range items {
		b, err := s.store.GetBookAny(ctx, it.BookID)
		if err != nil {
			continue
		}
		pb := promptBook{ID: b.ID, Title: b.Title, Language: b.Language, Publisher: b.Publisher, Published: b.Published, Pages: b.Pages, Library: libName[b.LibraryID]}
		for _, a := range b.Authors {
			pb.Authors = append(pb.Authors, a.Name)
		}
		if len(b.Tags) > 8 {
			pb.Tags = b.Tags[:8]
		} else {
			pb.Tags = b.Tags
		}
		pb.Description = cut(plainText(b.Description), 300)
		for _, f := range b.Files {
			if !f.Missing {
				pb.File = f.Path[strings.LastIndexAny(f.Path, `/\`)+1:]
				break
			}
		}
		rb := store.ReviewBook{ID: b.ID, Title: b.Title, Authors: pb.Authors}
		pb.Checks = badFlags(rb, people)
		if main != nil && b.LibraryID != main.ID && inMain[fold(b.Title)+"|"+surnames(pb.Authors)] {
			pb.Checks = append(pb.Checks, "a copy is already in books")
		}
		list = append(list, pb)
	}
	prompt, _ := json.Marshal(map[string]any{"books": list})
	text, err := model.Complete(ctx, sortSystem, string(prompt), true)
	if err != nil {
		return nil, err
	}
	var out struct {
		Books []sortAnswer `json:"books"`
	}
	text = strings.TrimSpace(text)
	if i := strings.IndexByte(text, '{'); i > 0 {
		text = text[i:]
	}
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		return nil, fmt.Errorf("Pocket AI answered something that is not the JSON asked for")
	}
	answers := map[int64]sortAnswer{}
	for _, a := range out.Books {
		a.Target = strings.ToLower(strings.TrimSpace(a.Target))
		if a.Target != "books" && a.Target != "tech" && a.Target != "review" {
			continue
		}
		if a.Target == "tech" && tech == nil {
			a.Target = "books"
		}
		a.Confidence = min(max(a.Confidence, 0), 1)
		a.Reason = cut(strings.TrimSpace(a.Reason), 160)
		answers[a.ID] = a
	}
	return answers, nil
}

// surnames is a sorted, folded list of authors' last names ("andric,…").
func surnames(authors []string) string {
	sn := []string{}
	for _, a := range authors {
		if k := surnameKey(a); k != "" {
			sn = append(sn, k)
		}
	}
	sort.Strings(sn)
	return strings.Join(sn, ",")
}

// sortTarget is the library a target names.
func (s *Server) sortTarget(ctx context.Context, target string) (*store.Library, bool) {
	main, review, tech := s.cleanupLibraries(ctx)
	switch target {
	case "books":
		return main, main != nil
	case "tech":
		return tech, tech != nil
	case "review":
		return review, review != nil
	}
	return nil, false
}

// GET /review/imports?status=suggested|pending|done&offset=&limit= — editors.
func (s *Server) handleSortList(w http.ResponseWriter, r *http.Request) {
	if !s.requireEditor(w, r) {
		return
	}
	ctx := r.Context()
	u := userFrom(ctx)
	statuses := map[string][]string{"suggested": {"suggested"}, "pending": {"pending", "failed"}, "done": {"applied", "kept"}}[r.URL.Query().Get("status")]
	if statuses == nil {
		statuses = []string{"suggested"}
	}
	offset, limit := queryInt(r, "offset"), queryInt(r, "limit")
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	items, total, counts, err := s.store.ListSort(ctx, statuses, s.allowedLibraries(ctx, u), offset, limit)
	if err != nil {
		s.fail(w, err, "sort")
		return
	}
	libNames := map[int64]string{}
	if list, err := s.store.LibrariesFor(ctx, u); err == nil {
		for _, l := range list {
			libNames[l.ID] = l.Name
		}
	}
	type row struct {
		store.SortItem
		Library string `json:"library"`
		To      string `json:"to,omitempty"`    // the target library's name
		Stays   bool   `json:"stays,omitempty"` // already there
	}
	rows := []row{}
	for _, it := range items {
		rw := row{SortItem: it, Library: libNames[it.LibraryID]}
		if l, ok := s.sortTarget(ctx, it.Target); ok {
			rw.To, rw.Stays = l.Name, l.ID == it.LibraryID
		}
		rows = append(rows, rw)
	}
	ai := map[string]any{"ready": true}
	if email := u.Email; email != "" {
		if _, err := s.sortModel(ctx, email); err != nil {
			ai = map[string]any{"ready": false, "detail": err.Error()}
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": rows, "total": total, "counts": counts, "ai": ai})
}

func (s *Server) sortIDs(w http.ResponseWriter, r *http.Request) []int64 {
	var in struct {
		IDs []int64 `json:"ids"`
	}
	if err := decode(r, &in); err != nil || len(in.IDs) == 0 || len(in.IDs) > 2000 {
		writeErr(w, http.StatusBadRequest, "ids (1–2000 books) are required")
		return nil
	}
	return in.IDs
}

// POST /review/imports/apply {"ids"} — move each book where Pocket AI suggests. Admins.
func (s *Server) handleSortApply(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	ids := s.sortIDs(w, r)
	if ids == nil {
		return
	}
	if s.scanning(0) {
		writeErr(w, http.StatusConflict, "a scan is running; move books when it is done")
		return
	}
	ctx := r.Context()
	main, _, tech := s.cleanupLibraries(ctx)
	moveMu.Lock()
	defer moveMu.Unlock()
	libs := map[int64]*store.Library{}
	moved, stayed := 0, 0
	var errs []string
	for _, id := range ids {
		it, err := s.store.GetSort(ctx, id)
		if err != nil || it.Status != "suggested" {
			continue
		}
		to, ok := s.sortTarget(ctx, it.Target)
		if !ok {
			errs = append(errs, fmt.Sprintf("#%d: no %s library", id, it.Target))
			continue
		}
		// into the main or tech library: filed by its template; into review: kept as it is
		rename := (main != nil && to.ID == main.ID) || (tech != nil && to.ID == tech.ID)
		err = s.moveBookCtx(ctx, id, to, rename, libs)
		switch {
		case errors.Is(err, errSameLibrary):
			stayed++
		case err != nil:
			errs = append(errs, fmt.Sprintf("#%d: %v", id, err))
			continue
		default:
			moved++
		}
		_ = s.store.SetSortStatus(ctx, []int64{id}, "applied")
	}
	writeJSON(w, http.StatusOK, map[string]any{"moved": moved, "stayed": stayed, "errors": errs})
}

// POST /review/imports/keep {"ids"} — leave the books where they are. Editors.
func (s *Server) handleSortKeep(w http.ResponseWriter, r *http.Request) {
	if !s.requireEditor(w, r) {
		return
	}
	ids := s.sortIDs(w, r)
	if ids == nil {
		return
	}
	if err := s.store.SetSortStatus(r.Context(), ids, "kept"); err != nil {
		s.fail(w, err, "sort")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"kept": len(ids)})
}

// POST /review/imports/ask {"ids"} — ask Pocket AI about these books (again). Editors.
func (s *Server) handleSortAsk(w http.ResponseWriter, r *http.Request) {
	if !s.requireEditor(w, r) {
		return
	}
	ids := s.sortIDs(w, r)
	if ids == nil {
		return
	}
	ctx := r.Context()
	u := userFrom(ctx)
	libs := s.allowedLibraries(ctx, u)
	ok := []int64{}
	for _, id := range ids {
		if _, err := s.store.GetBook(ctx, u.ID, id, libs); err == nil {
			ok = append(ok, id)
		}
	}
	if err := s.store.QueueSort(ctx, ok, "asked", u.ID, true); err != nil {
		s.fail(w, err, "sort")
		return
	}
	s.kickSort()
	writeJSON(w, http.StatusOK, map[string]any{"queued": len(ok)})
}
