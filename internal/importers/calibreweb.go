package importers

import (
	"database/sql"
	"net/mail"
	"os"
	"path/filepath"
	"strings"
)

// CalibreWeb is what a Calibre-Web (or Calibre-Web Automated) app.db holds
// about its people. Book ids are the Calibre library's.
type CalibreWeb struct {
	Users      []CWUser
	Mail       *CWMail // nil when no mail server is set
	MailError  string  // why the mail password could not be read
	Shelves    []CWShelf
	ReadStates []CWRead
}

// CWUser is a Calibre-Web account.
type CWUser struct {
	ID     int64
	Name   string
	Email  string
	Kindle string // its "Send to E-Reader" address
}

// CWMail is Calibre-Web's mail server.
type CWMail struct {
	Host     string
	Port     int
	TLS      string // none | starttls | tls
	User     string
	Password string
	From     string // address
	FromName string
}

// CWShelf is a shelf with its books in order.
type CWShelf struct {
	UserID int64
	Name   string
	Books  []int64
}

// CWRead is one person's status of one book: read or reading.
type CWRead struct {
	UserID   int64
	BookID   int64
	Status   string // read | reading
	Modified string
}

// ReadCalibreWeb reads an app.db (a copy of it); the Fernet key for the mail
// password is taken from ".key" next to it.
func ReadCalibreWeb(path string) (*CalibreWeb, error) {
	db, done, err := openCopy(path)
	if err != nil {
		return nil, err
	}
	defer done()
	out := &CalibreWeb{}
	rows, err := db.Query(`SELECT id, COALESCE(name, ''), COALESCE(email, ''), COALESCE(kindle_mail, '') FROM user`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var u CWUser
		if err := rows.Scan(&u.ID, &u.Name, &u.Email, &u.Kindle); err != nil {
			rows.Close()
			return nil, err
		}
		u.Email, u.Kindle = strings.ToLower(strings.TrimSpace(u.Email)), strings.ToLower(strings.TrimSpace(u.Kindle))
		if strings.EqualFold(u.Name, "guest") && u.Kindle == "" {
			continue
		}
		out.Users = append(out.Users, u)
	}
	rows.Close()

	if hasColumn(db, "settings", "mail_server") {
		var m CWMail
		var ssl int
		var pwE, pw sql.NullString
		err := db.QueryRow(`SELECT COALESCE(mail_server, ''), COALESCE(mail_port, 0), COALESCE(mail_use_ssl, 0), COALESCE(mail_login, ''), mail_password_e, mail_password, COALESCE(mail_from, '') FROM settings LIMIT 1`).
			Scan(&m.Host, &m.Port, &ssl, &m.User, &pwE, &pw, &m.From)
		if err == nil && m.Host != "" && m.Host != "mail.example.org" {
			m.TLS = [...]string{"none", "starttls", "tls"}[min(max(ssl, 0), 2)]
			if a, err := mail.ParseAddress(m.From); err == nil {
				m.From, m.FromName = a.Address, a.Name
			}
			switch {
			case pwE.Valid && pwE.String != "":
				key, kerr := os.ReadFile(filepath.Join(filepath.Dir(path), ".key"))
				if kerr != nil {
					out.MailError = "the mail password is sealed with the key in .key next to app.db, which could not be read"
					break
				}
				p, derr := FernetDecrypt(string(key), pwE.String)
				if derr != nil {
					out.MailError = "the mail password could not be unsealed: " + derr.Error()
					break
				}
				m.Password = string(p)
			case pw.Valid:
				m.Password = pw.String
			}
			out.Mail = &m
		}
	}

	if hasTable(db, "shelf") && hasTable(db, "book_shelf_link") {
		rows, err := db.Query(`SELECT s.id, s.user_id, s.name FROM shelf s ORDER BY s.id`)
		if err != nil {
			return nil, err
		}
		type sh struct {
			id int64
			CWShelf
		}
		var shelves []sh
		for rows.Next() {
			var x sh
			if err := rows.Scan(&x.id, &x.UserID, &x.Name); err != nil {
				rows.Close()
				return nil, err
			}
			shelves = append(shelves, x)
		}
		rows.Close()
		for _, x := range shelves {
			ids, err := ints(db, `SELECT book_id FROM book_shelf_link WHERE shelf = ? ORDER BY "order", id`, x.id)
			if err != nil {
				return nil, err
			}
			x.Books = ids
			out.Shelves = append(out.Shelves, x.CWShelf)
		}
	}

	if hasTable(db, "book_read_link") {
		rows, err := db.Query(`SELECT user_id, book_id, read_status, COALESCE(last_modified, '') FROM book_read_link WHERE read_status IN (1, 2)`)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var r CWRead
			var st int
			if err := rows.Scan(&r.UserID, &r.BookID, &st, &r.Modified); err != nil {
				rows.Close()
				return nil, err
			}
			r.Status = "reading"
			if st == 1 {
				r.Status = "read"
			}
			out.ReadStates = append(out.ReadStates, r)
		}
		rows.Close()
	}
	return out, nil
}

func ints(db *sql.DB, q string, args ...any) ([]int64, error) {
	rows, err := db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var v int64
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// CalibreBook is a book of a Calibre library.
type CalibreBook struct {
	ID     int64
	UUID   string
	Title  string
	Author string
	ISBN   string
}

// ReadCalibre reads the books of a Calibre metadata.db (a copy of it).
func ReadCalibre(path string) (map[int64]CalibreBook, error) {
	db, done, err := openCopy(path)
	if err != nil {
		return nil, err
	}
	defer done()
	rows, err := db.Query(`SELECT b.id, COALESCE(b.uuid, ''), b.title, COALESCE(b.author_sort, ''),
		COALESCE((SELECT val FROM identifiers i WHERE i.book = b.id AND i.type = 'isbn' LIMIT 1), '') FROM books b`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]CalibreBook{}
	for rows.Next() {
		var b CalibreBook
		if err := rows.Scan(&b.ID, &b.UUID, &b.Title, &b.Author, &b.ISBN); err != nil {
			return nil, err
		}
		out[b.ID] = b
	}
	return out, rows.Err()
}
