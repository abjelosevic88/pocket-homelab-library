package server

// Suggested clean-up (Review page, admins): the main library keeps good books
// only. Books with bad details and extra copies of a book move to the review
// library ("Review", or "Unsorted", which the clean-up renames); books in the
// review library that look fine move to the main library, or to a tech
// library when they are about programming or learning a language. Of two
// copies of a book the one kept is the one someone reads (or has highlights
// in), else an EPUB, else the one with a cover, a description, an ISBN.
// Books someone reads never leave the main library. It runs in the
// background, one move at a time (move.go), and can be run again later.

import (
	"context"
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/abjelosevic88/pocket-homelab-library/internal/store"
)

var techTitleRE = regexp.MustCompile(`(?i)\b(programming|programmer|javascript|typescript|python|java|css|html|php|sql|mysql|mongodb|linux|bash|arduino|raspberry|packt|o.reilly|apress|manning|hands-on|developer|development|framework|react|angular|vue|android|ios|swift|kotlin|flutter|dart|node\.?js|golang|rust|docker|kubernetes|devops|aws|azure|algorithms?|database|networking|security|machine learning|data science|excel|grammar|verbs?|vocabulary|learn german|learn english|deutsch|qt|c\+\+|c#|\.net|unity|game programming|web design|sencha|eclipse|assembly|debugging|software|computer|wordpress|seo|photoshop|internet of things)\b`)

type cleanupMove struct {
	To      int64          `json:"to_library_id"`
	ToName  string         `json:"to"`
	From    string         `json:"from"`
	Rename  bool           `json:"-"`
	IDs     []int64        `json:"-"`
	Count   int            `json:"count"`
	Reasons map[string]int `json:"reasons"`
	Samples []string       `json:"samples"`
}

type cleanupPlan struct {
	Main      *store.Library `json:"main"`
	Review    *store.Library `json:"review"`
	Tech      *store.Library `json:"tech,omitempty"`
	RenameTo  string         `json:"rename_review_to,omitempty"` // "Unsorted" becomes "Review"
	Moves     []*cleanupMove `json:"moves"`
	Protected int            `json:"protected"` // flagged, but someone reads them: they stay
}

// cleanupLibraries finds the main library ("Books"), the review library
// ("Review" or "Unsorted") and a tech library (a name with "tech"), books only.
func (s *Server) cleanupLibraries(ctx context.Context) (main, review, tech *store.Library) {
	libs, err := s.store.ListLibraries(ctx)
	if err != nil {
		return nil, nil, nil
	}
	for i := range libs {
		l := &libs[i]
		if l.OwnerUserID != 0 || l.Kind != "books" || len(l.Paths) == 0 {
			continue
		}
		switch n := strings.ToLower(strings.TrimSpace(l.Name)); {
		case n == "books" && main == nil:
			main = l
		case (n == "review" || n == "unsorted") && review == nil:
			review = l
		case strings.Contains(n, "tech") && tech == nil:
			tech = l
		}
	}
	return main, review, tech
}

func (s *Server) planCleanup(ctx context.Context) (*cleanupPlan, error) {
	main, review, tech := s.cleanupLibraries(ctx)
	if main == nil || review == nil {
		return nil, fmt.Errorf("the clean-up needs a library named “Books” and one named “Review” or “Unsorted”")
	}
	plan := &cleanupPlan{Main: main, Review: review, Tech: tech, Moves: []*cleanupMove{}}
	if strings.EqualFold(review.Name, "unsorted") {
		plan.RenameTo = "Review"
	}
	books, err := s.store.ReviewBooks(ctx, []int64{main.ID, review.ID})
	if err != nil {
		return nil, err
	}
	people := authorBooks(books)
	bad := map[int64][]string{}
	for _, b := range books {
		if f := hardFlags(b, people); len(f) > 0 {
			bad[b.ID] = f
		}
	}
	// copies of one book across both libraries (duplicates.go), books with bad details aside
	good := []store.ReviewBook{}
	for _, b := range books {
		if bad[b.ID] == nil {
			good = append(good, b)
		}
	}
	groups := duplicateGroups(good)
	score := func(b store.ReviewBook) int {
		n := 0
		if b.Used {
			n += 100
		}
		if strings.Contains(b.Formats, "epub") {
			n += 10
		}
		if b.Cover != "" {
			n += 4
		}
		if b.HasDesc {
			n += 2
		}
		if b.HasISBN {
			n += 2
		}
		if b.LibraryID == main.ID {
			n++
		}
		return n
	}
	extra := map[int64]bool{}
	for _, g := range groups {
		if len(g) < 2 {
			continue
		}
		sort.SliceStable(g, func(i, j int) bool { return score(g[i]) > score(g[j]) })
		for _, b := range g[1:] {
			extra[b.ID] = true
		}
	}
	toReview := &cleanupMove{To: review.ID, ToName: plan.renamedReview(), From: main.Name, Reasons: map[string]int{}, Samples: []string{}}
	toMain := &cleanupMove{To: main.ID, ToName: main.Name, From: review.Name, Rename: true, Reasons: map[string]int{}, Samples: []string{}}
	var toTech *cleanupMove
	if tech != nil {
		toTech = &cleanupMove{To: tech.ID, ToName: tech.Name, From: review.Name, Rename: true, Reasons: map[string]int{}, Samples: []string{}}
	}
	add := func(m *cleanupMove, b store.ReviewBook, why string) {
		m.IDs = append(m.IDs, b.ID)
		m.Reasons[why]++
		if len(m.Samples) < 8 {
			m.Samples = append(m.Samples, strings.TrimSpace(b.Title+" · "+strings.Join(b.Authors, ", ")))
		}
	}
	for _, b := range books {
		why := ""
		if f := bad[b.ID]; f != nil {
			why = f[0]
		} else if extra[b.ID] {
			why = "duplicate"
		}
		switch {
		case b.LibraryID == main.ID && why != "" && b.Used:
			plan.Protected++
		case b.LibraryID == main.ID && why != "":
			add(toReview, b, why)
		case b.LibraryID == review.ID && why == "" && toTech != nil && techTitleRE.MatchString(b.Title):
			add(toTech, b, "tech")
		case b.LibraryID == review.ID && why == "":
			add(toMain, b, "looks_good")
		}
	}
	for _, m := range []*cleanupMove{toReview, toMain, toTech} {
		if m != nil && len(m.IDs) > 0 {
			m.Count = len(m.IDs)
			plan.Moves = append(plan.Moves, m)
		}
	}
	return plan, nil
}

func (p *cleanupPlan) renamedReview() string {
	if p.RenameTo != "" {
		return p.RenameTo
	}
	return p.Review.Name
}

// the clean-up running now (or the last one)
type cleanupState struct {
	Running  bool     `json:"running"`
	Total    int      `json:"total"`
	Done     int      `json:"done"`
	Moved    int      `json:"moved"`
	Errors   []string `json:"errors"`
	Started  string   `json:"started_at,omitempty"`
	Finished string   `json:"finished_at,omitempty"`
}

var (
	cleanupMu  sync.Mutex
	cleanupNow = cleanupState{Errors: []string{}}
)

// GET /review/cleanup — the plan and the last run. Admins.
func (s *Server) handleCleanupPlan(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	cleanupMu.Lock()
	st := cleanupNow
	cleanupMu.Unlock()
	plan, err := s.planCleanup(r.Context())
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"error": err.Error(), "run": st})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"plan": plan, "run": st})
}

// POST /review/cleanup — run it in the background. Admins.
func (s *Server) handleCleanupRun(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	if s.scanning(0) {
		writeErr(w, http.StatusConflict, "a scan is running; clean up when it is done")
		return
	}
	plan, err := s.planCleanup(r.Context())
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	cleanupMu.Lock()
	if cleanupNow.Running {
		cleanupMu.Unlock()
		writeErr(w, http.StatusConflict, "the clean-up is already running")
		return
	}
	total := 0
	for _, m := range plan.Moves {
		total += len(m.IDs)
	}
	cleanupNow = cleanupState{Running: true, Total: total, Errors: []string{}, Started: store.Now()}
	cleanupMu.Unlock()
	go s.runCleanup(plan)
	writeJSON(w, http.StatusAccepted, map[string]any{"run": cleanupNow})
}

func (s *Server) runCleanup(plan *cleanupPlan) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Hour)
	defer cancel()
	moveMu.Lock()
	defer moveMu.Unlock()
	libs := map[int64]*store.Library{}
	for _, m := range plan.Moves {
		to, err := s.store.GetLibrary(ctx, m.To)
		if err != nil {
			continue
		}
		for _, id := range m.IDs {
			err := s.moveBookCtx(ctx, id, to, m.Rename, libs)
			cleanupMu.Lock()
			cleanupNow.Done++
			switch {
			case err == nil:
				cleanupNow.Moved++
			case err != errSameLibrary && len(cleanupNow.Errors) < 50:
				cleanupNow.Errors = append(cleanupNow.Errors, fmt.Sprintf("#%d: %v", id, err))
			}
			cleanupMu.Unlock()
		}
	}
	if plan.RenameTo != "" {
		if l, err := s.store.GetLibrary(ctx, plan.Review.ID); err == nil {
			l.Name = plan.RenameTo
			if err := s.store.UpdateLibrary(ctx, l); err != nil {
				s.log.Warn("clean-up: rename library", "err", err)
			}
		}
	}
	cleanupMu.Lock()
	cleanupNow.Running, cleanupNow.Finished = false, store.Now()
	st := cleanupNow
	cleanupMu.Unlock()
	s.log.Info("clean-up done", "moved", st.Moved, "of", st.Total, "errors", len(st.Errors))
}
