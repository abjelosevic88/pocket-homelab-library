package store

// User is an account on this box.
type User struct {
	ID           int64  `json:"id"`
	Email        string `json:"email"`
	Name         string `json:"name"`
	PasswordHash string `json:"-"`
	Role         string `json:"role"`     // admin | member
	CanEdit      bool   `json:"can_edit"` // may edit metadata, upload and delete (admins always)
	CreatedAt    string `json:"created_at"`
}

// Editor reports whether the person may change the catalogue.
func (u *User) Editor() bool { return u != nil && (u.Role == "admin" || u.CanEdit) }

// APIToken is a personal access token or a device's app password (hash stored, plaintext shown once).
type APIToken struct {
	ID         int64   `json:"id"`
	UserID     int64   `json:"user_id"`
	Name       string  `json:"name"`
	Kind       string  `json:"kind"` // token | device
	Prefix     string  `json:"prefix"`
	LastUsedAt *string `json:"last_used_at"`
	CreatedAt  string  `json:"created_at"`
}

// Library is a set of folders of one kind.
type Library struct {
	ID             int64    `json:"id"`
	Name           string   `json:"name"`
	Kind           string   `json:"kind"` // books | comics | manga
	Paths          []string `json:"paths"`
	Access         string   `json:"access"` // all | people
	People         []int64  `json:"people"` // when access = people
	Uploads        bool     `json:"uploads"`
	UploadTemplate string   `json:"upload_template"`
	ScanEvery      int      `json:"scan_every"` // minutes
	Position       int      `json:"position"`
	LastScanAt     string   `json:"last_scan_at"`
	LastScanMS     int64    `json:"last_scan_ms"`
	LastScanError  string   `json:"last_scan_error"`
	CreatedAt      string   `json:"created_at"`
	Books          int      `json:"books"` // count, filled by listings
}

// Author of books in the catalogue.
type Author struct {
	ID       int64             `json:"id"`
	Name     string            `json:"name"`
	SortName string            `json:"sort_name"`
	Bio      string            `json:"bio,omitempty"`
	Photo    string            `json:"photo,omitempty"` // URL to the photo, '' = none
	Links    map[string]string `json:"links,omitempty"`
	Books    int               `json:"books"`
}

// Series groups books in order.
type Series struct {
	ID          int64   `json:"id"`
	Name        string  `json:"name"`
	SortName    string  `json:"sort_name"`
	Description string  `json:"description,omitempty"`
	Books       int     `json:"books"`
	Read        int     `json:"read"`   // the person's finished books in it
	Covers      []string `json:"covers"` // up to 3 cover URLs of the first books
}

// File is one format of a book.
type File struct {
	ID         int64  `json:"id"`
	BookID     int64  `json:"book_id"`
	LibraryID  int64  `json:"library_id"`
	Path       string `json:"-"`
	Name       string `json:"name"` // base name
	Format     string `json:"format"`
	Size       int64  `json:"size"`
	MTime      int64  `json:"mtime"`
	PartialMD5 string `json:"partial_md5"`
	NameMD5    string `json:"-"`
	SHA256     string `json:"-"`
	Pages      int    `json:"pages"`
	Missing    bool   `json:"missing"`
	AddedAt    string `json:"added_at"`
}

// Book is a work in a library with its files and, for the person asking, where they stand.
type Book struct {
	ID               int64             `json:"id"`
	LibraryID        int64             `json:"library_id"`
	GroupKey         string            `json:"-"`
	Title            string            `json:"title"`
	SortTitle        string            `json:"sort_title"`
	Subtitle         string            `json:"subtitle"`
	Authors          []AuthorRef       `json:"authors"`
	Description      string            `json:"description,omitempty"`
	Language         string            `json:"language"`
	Publisher        string            `json:"publisher"`
	Published        string            `json:"published"`
	ISBN             string            `json:"isbn"`
	Identifiers      map[string]string `json:"identifiers"`
	SeriesID         int64             `json:"series_id"`
	Series           string            `json:"series"`
	SeriesIndex      float64           `json:"series_index"`
	Tags             []string          `json:"tags"`
	Pages            int               `json:"pages"`
	Words            int               `json:"words"`
	Kind             string            `json:"kind"` // book | comic | manga
	ReadingDirection string            `json:"reading_direction"`
	AgeRating        string            `json:"age_rating"`
	Cover            string            `json:"cover"` // URL ('' = none)
	CoverColor       string            `json:"cover_color"`
	Locked           []string          `json:"locked"`
	MetaSource       string            `json:"meta_source"`
	Missing          bool              `json:"missing"`
	AddedAt          string            `json:"added_at"`
	UpdatedAt        string            `json:"updated_at"`
	Formats          []string          `json:"formats"`         // distinct formats of its files
	Files            []File            `json:"files,omitempty"` // detail only
	State            *UserBook         `json:"state,omitempty"` // the asking person's
}

// AuthorRef is an author as a book lists it.
type AuthorRef struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

// UserBook is where one person stands with one book.
type UserBook struct {
	Status     string  `json:"status"` // '' | want | reading | read | dnf
	Rating     int     `json:"rating"` // 1..10 half stars
	Favorite   bool    `json:"favorite"`
	Hidden     bool    `json:"hidden"`
	Percentage float64 `json:"percentage"`
	Locator    string  `json:"locator"` // JSON
	FileID     int64   `json:"file_id"`
	LastReadAt string  `json:"last_read_at"`
	LastDevice string  `json:"last_device"`
	StartedAt  string  `json:"started_at"`
	FinishedAt string  `json:"finished_at"`
	TimesRead  int     `json:"times_read"`
	UpdatedAt  string  `json:"updated_at"`
}

// Read is one finished (or abandoned) read of a book.
type Read struct {
	ID         int64  `json:"id"`
	BookID     int64  `json:"book_id"`
	Status     string `json:"status"` // read | dnf
	StartedAt  string `json:"started_at"`
	FinishedAt string `json:"finished_at"`
	Day        string `json:"day"`
}

// ReadingSession is time spent in a book.
type ReadingSession struct {
	ID        int64   `json:"id"`
	BookID    int64   `json:"book_id"`
	FileID    int64   `json:"file_id"`
	Device    string  `json:"device"`
	Source    string  `json:"source"` // web | kosync | koreader | import
	Day       string  `json:"day"`
	StartedAt string  `json:"started_at"`
	EndedAt   string  `json:"ended_at"`
	Seconds   int     `json:"seconds"`
	StartPct  float64 `json:"start_pct"`
	EndPct    float64 `json:"end_pct"`
	Pages     int     `json:"pages"`
	Estimated bool    `json:"estimated"`
}

// KosyncProgress is a KOReader sync position for one document.
type KosyncProgress struct {
	Document   string  `json:"document"`
	BookID     int64   `json:"book_id"`
	FileID     int64   `json:"file_id"`
	Progress   string  `json:"progress"`
	Percentage float64 `json:"percentage"`
	Device     string  `json:"device"`
	DeviceID   string  `json:"device_id"`
	Timestamp  int64   `json:"timestamp"`
}

// Annotation is a highlight, note or bookmark.
type Annotation struct {
	ID         int64   `json:"id"`
	BookID     int64   `json:"book_id"`
	FileID     int64   `json:"file_id"`
	Kind       string  `json:"kind"` // highlight | note | bookmark
	Text       string  `json:"text"`
	Note       string  `json:"note"`
	Color      string  `json:"color"`
	Locator    string  `json:"locator"` // JSON
	Chapter    string  `json:"chapter"`
	Percentage float64 `json:"percentage"`
	Page       int     `json:"page"`
	Source     string  `json:"source"` // web | koreader | kindle | import
	ExternalID string  `json:"external_id,omitempty"`
	CreatedAt  string  `json:"created_at"`
	UpdatedAt  string  `json:"updated_at"`
	// listings across books
	BookTitle string `json:"book_title,omitempty"`
	BookCover string `json:"book_cover,omitempty"`
}

// Shelf is a person's hand-picked or smart collection.
type Shelf struct {
	ID          int64    `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Icon        string   `json:"icon"`
	Kind        string   `json:"kind"`   // manual | smart
	Filter      string   `json:"filter"` // JSON BookQuery for smart shelves
	Ordered     bool     `json:"ordered"`
	OPDS        bool     `json:"opds"`
	Position    int      `json:"position"`
	Books       int      `json:"books"`
	Covers      []string `json:"covers"`
	CreatedAt   string   `json:"created_at"`
	UpdatedAt   string   `json:"updated_at"`
}

// Device is one of a person's e-readers.
type Device struct {
	ID         int64          `json:"id"`
	Kind       string         `json:"kind"` // kindle | crosspoint | koreader | kobo | email
	Name       string         `json:"name"`
	Email      string         `json:"email"`
	Address    string         `json:"address"`
	Format     string         `json:"format"`
	TokenID    int64          `json:"token_id"`
	Settings   map[string]any `json:"settings"`
	IsDefault  bool           `json:"is_default"`
	LastSeenAt string         `json:"last_seen_at"`
	CreatedAt  string         `json:"created_at"`
	Pending    int            `json:"pending"` // sends waiting to be fetched
}

// Send is one book on its way to a device.
type Send struct {
	ID         int64  `json:"id"`
	DeviceID   int64  `json:"device_id"`
	DeviceName string `json:"device_name"`
	BookID     int64  `json:"book_id"`
	FileID     int64  `json:"file_id"`
	Title      string `json:"title"`
	Format     string `json:"format"`
	Method     string `json:"method"` // email | push | pull
	Status     string `json:"status"` // queued | sending | sent | failed | cancelled
	Error      string `json:"error"`
	Bytes      int64  `json:"bytes"`
	CreatedAt  string `json:"created_at"`
	SentAt     string `json:"sent_at"`
	Cover      string `json:"cover,omitempty"`
}
