// Package importers reads what the apps Pocket Library replaces know about
// their people: Calibre-Web (Kindle addresses, the mail account, shelves,
// read status), a Calibre library (book ids), Kavita (reading progress,
// want to read, ratings, collections, reading lists, highlights) and a
// Kindle's My Clippings.txt.
//
// Databases are never opened in place: the file and its -wal/-shm are
// copied to a temporary folder first and the copy is read, so a running app
// is never disturbed and its newest writes are seen.
package importers

import (
	"database/sql"
	"fmt"
	"io"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"
)

// openCopy copies an SQLite database (with its WAL) and opens the copy
// read-only. close removes the copy.
func openCopy(path string) (db *sql.DB, closeFn func(), err error) {
	if _, err := os.Stat(path); err != nil {
		return nil, nil, err
	}
	dir, err := os.MkdirTemp("", "plb-import-*")
	if err != nil {
		return nil, nil, err
	}
	cleanup := func() { os.RemoveAll(dir) }
	dst := filepath.Join(dir, filepath.Base(path))
	for _, ext := range []string{"", "-wal", "-shm"} {
		if err := copyFile(path+ext, dst+ext); err != nil {
			if ext != "" && os.IsNotExist(err) {
				continue
			}
			cleanup()
			return nil, nil, err
		}
	}
	db, err = sql.Open("sqlite", "file:"+dst+"?mode=ro&_pragma=busy_timeout(5000)")
	if err != nil {
		cleanup()
		return nil, nil, err
	}
	if err := db.Ping(); err != nil {
		db.Close()
		cleanup()
		return nil, nil, fmt.Errorf("open %s: %w", filepath.Base(path), err)
	}
	return db, func() { db.Close(); cleanup() }, nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// hasTable reports whether a table exists.
func hasTable(db *sql.DB, name string) bool {
	var n int
	_ = db.QueryRow(`SELECT COUNT(1) FROM sqlite_master WHERE type = 'table' AND name = ?`, name).Scan(&n)
	return n > 0
}

// hasColumn reports whether a table has a column.
func hasColumn(db *sql.DB, table, col string) bool {
	rows, err := db.Query(`SELECT name FROM pragma_table_info(?)`, table)
	if err != nil {
		return false
	}
	defer rows.Close()
	for rows.Next() {
		var n string
		if rows.Scan(&n) == nil && n == col {
			return true
		}
	}
	return false
}
