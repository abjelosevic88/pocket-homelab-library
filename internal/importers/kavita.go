package importers

import (
	"database/sql"
	"strings"
)

// Kavita is what a kavita.db knows about its people. Kavita keeps progress
// per chapter (one file for books, one archive per comic or manga chapter)
// and most choices per series, so everything points at files.
type Kavita struct {
	Users        []KUser
	Progress     []KProgress
	Want         []KSeriesRef
	Ratings      []KRating
	Collections  []KList
	ReadingLists []KList
	Annotations  []KAnnotation
	Series       map[int64]KSeries // the series the above refer to
}

// KUser is a Kavita account.
type KUser struct {
	ID    int64
	Name  string
	Email string
}

// KFile is one file as Kavita saw it (paths are inside Kavita's container).
type KFile struct {
	Path  string
	Hash  string // KOReader's partial MD5, when Kavita computed it
	Bytes int64
	Pages int
}

// KProgress is how far someone is in one chapter.
type KProgress struct {
	UserID     int64
	ChapterID  int64
	Files      []KFile
	PagesRead  int
	Pages      int // the chapter's pages
	TotalReads int
	Modified   string // UTC
	ScrollID   string // the reader's XPath in EPUBs
}

// KSeriesRef points at a series for a person.
type KSeriesRef struct {
	UserID   int64
	SeriesID int64
}

// KRating is a person's rating of a series (0–5).
type KRating struct {
	UserID   int64
	SeriesID int64
	Rating   float64
	Review   string
}

// KList is a collection (of series) or a reading list (of chapters, ordered).
type KList struct {
	UserID  int64
	Title   string
	Summary string
	Series  []int64   // collections
	Items   [][]KFile // reading lists: the files of each chapter, in order
}

// KAnnotation is a highlight made in Kavita's reader.
type KAnnotation struct {
	UserID  int64
	Files   []KFile
	Text    string
	Comment string
	Chapter string
	Page    int
	XPath   string
	Created string
}

// KSeries is a series with its files.
type KSeries struct {
	ID    int64
	Name  string
	Files []KFile
}

// ReadKavita reads a kavita.db (a copy of it), using only the tables it has.
func ReadKavita(path string) (*Kavita, error) {
	db, done, err := openCopy(path)
	if err != nil {
		return nil, err
	}
	defer done()
	k := &Kavita{Series: map[int64]KSeries{}}
	userTable := "AspNetUsers"
	if !hasTable(db, userTable) {
		userTable = "AppUser"
	}
	rows, err := db.Query(`SELECT Id, COALESCE(UserName, ''), COALESCE(Email, '') FROM ` + userTable)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var u KUser
		if err := rows.Scan(&u.ID, &u.Name, &u.Email); err != nil {
			rows.Close()
			return nil, err
		}
		u.Email = strings.ToLower(strings.TrimSpace(u.Email))
		k.Users = append(k.Users, u)
	}
	rows.Close()

	chapterFiles := func(id int64) ([]KFile, error) { return kFiles(db, `WHERE ChapterId = ?`, id) }

	if hasTable(db, "AppUserProgresses") {
		var list []KProgress
		rows, err := db.Query(`SELECT p.AppUserId, p.ChapterId, p.PagesRead, COALESCE(p.TotalReads, 0), COALESCE(p.LastModifiedUtc, p.LastModified, ''), COALESCE(p.BookScrollId, ''),
			COALESCE((SELECT c.Pages FROM Chapter c WHERE c.Id = p.ChapterId), 0) FROM AppUserProgresses p ORDER BY p.Id`)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var p KProgress
			if err := rows.Scan(&p.UserID, &p.ChapterID, &p.PagesRead, &p.TotalReads, &p.Modified, &p.ScrollID, &p.Pages); err != nil {
				rows.Close()
				return nil, err
			}
			list = append(list, p)
		}
		rows.Close()
		for _, p := range list {
			if p.Files, err = chapterFiles(p.ChapterID); err != nil {
				return nil, err
			}
			k.Progress = append(k.Progress, p)
		}
	}

	seriesWanted := map[int64]bool{}
	if hasTable(db, "AppUserWantToRead") {
		rows, err := db.Query(`SELECT AppUserId, SeriesId FROM AppUserWantToRead`)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var r KSeriesRef
			if err := rows.Scan(&r.UserID, &r.SeriesID); err != nil {
				rows.Close()
				return nil, err
			}
			k.Want = append(k.Want, r)
			seriesWanted[r.SeriesID] = true
		}
		rows.Close()
	}
	if hasTable(db, "AppUserRating") {
		rows, err := db.Query(`SELECT AppUserId, SeriesId, COALESCE(Rating, 0), COALESCE(Review, '') FROM AppUserRating WHERE COALESCE(HasBeenRated, 1) = 1`)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var r KRating
			if err := rows.Scan(&r.UserID, &r.SeriesID, &r.Rating, &r.Review); err != nil {
				rows.Close()
				return nil, err
			}
			if r.Rating > 0 {
				k.Ratings = append(k.Ratings, r)
				seriesWanted[r.SeriesID] = true
			}
		}
		rows.Close()
	}
	if hasTable(db, "AppUserCollection") && hasTable(db, "AppUserCollectionSeries") {
		rows, err := db.Query(`SELECT Id, AppUserId, COALESCE(Title, ''), COALESCE(Summary, '') FROM AppUserCollection`)
		if err != nil {
			return nil, err
		}
		type c struct {
			id int64
			KList
		}
		var cs []c
		for rows.Next() {
			var x c
			if err := rows.Scan(&x.id, &x.UserID, &x.Title, &x.Summary); err != nil {
				rows.Close()
				return nil, err
			}
			cs = append(cs, x)
		}
		rows.Close()
		for _, x := range cs {
			if x.Series, err = ints(db, `SELECT ItemsId FROM AppUserCollectionSeries WHERE CollectionsId = ?`, x.id); err != nil {
				return nil, err
			}
			for _, sid := range x.Series {
				seriesWanted[sid] = true
			}
			k.Collections = append(k.Collections, x.KList)
		}
	}
	if hasTable(db, "ReadingList") && hasTable(db, "ReadingListItem") {
		rows, err := db.Query(`SELECT Id, AppUserId, COALESCE(Title, ''), COALESCE(Summary, '') FROM ReadingList`)
		if err != nil {
			return nil, err
		}
		type rl struct {
			id int64
			KList
		}
		var ls []rl
		for rows.Next() {
			var x rl
			if err := rows.Scan(&x.id, &x.UserID, &x.Title, &x.Summary); err != nil {
				rows.Close()
				return nil, err
			}
			ls = append(ls, x)
		}
		rows.Close()
		for _, x := range ls {
			chapters, err := ints(db, `SELECT ChapterId FROM ReadingListItem WHERE ReadingListId = ? ORDER BY "Order", Id`, x.id)
			if err != nil {
				return nil, err
			}
			for _, ch := range chapters {
				fs, err := chapterFiles(ch)
				if err != nil {
					return nil, err
				}
				x.Items = append(x.Items, fs)
			}
			k.ReadingLists = append(k.ReadingLists, x.KList)
		}
	}
	if hasTable(db, "AppUserAnnotation") {
		type an struct {
			chapter int64
			KAnnotation
		}
		var list []an
		rows, err := db.Query(`SELECT AppUserId, ChapterId, COALESCE(SelectedText, ''), COALESCE(CommentPlainText, Comment, ''), COALESCE(ChapterTitle, ''), COALESCE(PageNumber, 0), COALESCE(XPath, ''), COALESCE(CreatedUtc, Created, '') FROM AppUserAnnotation`)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var a an
			if err := rows.Scan(&a.UserID, &a.chapter, &a.Text, &a.Comment, &a.Chapter, &a.Page, &a.XPath, &a.Created); err != nil {
				rows.Close()
				return nil, err
			}
			list = append(list, a)
		}
		rows.Close()
		for _, a := range list {
			if a.Files, err = chapterFiles(a.chapter); err != nil {
				return nil, err
			}
			k.Annotations = append(k.Annotations, a.KAnnotation)
		}
	}
	for sid := range seriesWanted {
		var name string
		_ = db.QueryRow(`SELECT COALESCE(Name, '') FROM Series WHERE Id = ?`, sid).Scan(&name)
		fs, err := kFiles(db, `WHERE ChapterId IN (SELECT c.Id FROM Chapter c JOIN Volume v ON v.Id = c.VolumeId WHERE v.SeriesId = ?)`, sid)
		if err != nil {
			return nil, err
		}
		k.Series[sid] = KSeries{ID: sid, Name: name, Files: fs}
	}
	return k, nil
}

func kFiles(db *sql.DB, where string, args ...any) ([]KFile, error) {
	hashCol := "''"
	if hasColumn(db, "MangaFile", "KoreaderHash") {
		hashCol = "COALESCE(KoreaderHash, '')"
	}
	rows, err := db.Query(`SELECT FilePath, `+hashCol+`, COALESCE(Bytes, 0), COALESCE(Pages, 0) FROM MangaFile `+where+` ORDER BY Id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []KFile
	for rows.Next() {
		var f KFile
		if err := rows.Scan(&f.Path, &f.Hash, &f.Bytes, &f.Pages); err != nil {
			return nil, err
		}
		f.Hash = strings.ToLower(f.Hash)
		out = append(out, f)
	}
	return out, rows.Err()
}
