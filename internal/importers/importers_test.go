package importers

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/binary"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fernetEncrypt follows the Fernet spec, to make test tokens.
func fernetEncrypt(key, plain []byte) string {
	iv := []byte("0123456789abcdef")
	pad := aes.BlockSize - len(plain)%aes.BlockSize
	p := append(append([]byte{}, plain...), []byte(strings.Repeat(string(rune(pad)), pad))...)
	block, _ := aes.NewCipher(key[16:])
	ct := make([]byte, len(p))
	cipher.NewCBCEncrypter(block, iv).CryptBlocks(ct, p)
	body := []byte{0x80}
	ts := make([]byte, 8)
	binary.BigEndian.PutUint64(ts, 1700000000)
	body = append(append(append(body, ts...), iv...), ct...)
	mac := hmac.New(sha256.New, key[:16])
	mac.Write(body)
	return base64.URLEncoding.EncodeToString(append(body, mac.Sum(nil)...))
}

func TestFernet(t *testing.T) {
	key := []byte("0123456789ABCDEF0123456789abcdef")
	k := base64.URLEncoding.EncodeToString(key)
	tok := fernetEncrypt(key, []byte("s3cret-pässword"))
	got, err := FernetDecrypt(k+"\n", tok)
	if err != nil || string(got) != "s3cret-pässword" {
		t.Fatalf("%q %v", got, err)
	}
	other := base64.URLEncoding.EncodeToString([]byte("XXXXXXXXXXXXXXXX0123456789abcdef"))
	if _, err := FernetDecrypt(other, tok); err == nil {
		t.Fatal("wrong key accepted")
	}
}

func TestReadCalibreWeb(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "app.db")
	db, err := sql.Open("sqlite", p)
	if err != nil {
		t.Fatal(err)
	}
	key := []byte("0123456789ABCDEF0123456789abcdef")
	for _, q := range []string{
		`CREATE TABLE user (id INTEGER PRIMARY KEY, name TEXT, email TEXT, kindle_mail TEXT)`,
		`INSERT INTO user VALUES (1, 'admin', 'Ana@Example.com', 'ana@kindle.com'), (2, 'Guest', 'no@email', '')`,
		`CREATE TABLE settings (mail_server TEXT, mail_port INT, mail_use_ssl INT, mail_login TEXT, mail_password_e TEXT, mail_password TEXT, mail_from TEXT)`,
		`INSERT INTO settings VALUES ('smtp.example.com', 587, 1, 'login', '` + fernetEncrypt(key, []byte("pw")) + `', NULL, 'Kindle <kindle@example.com>')`,
		`CREATE TABLE shelf (id INTEGER PRIMARY KEY, user_id INT, name TEXT)`,
		`INSERT INTO shelf VALUES (5, 1, 'Summer')`,
		`CREATE TABLE book_shelf_link (id INTEGER PRIMARY KEY, book_id INT, "order" INT, shelf INT)`,
		`INSERT INTO book_shelf_link VALUES (1, 30, 2, 5), (2, 20, 1, 5)`,
		`CREATE TABLE book_read_link (id INTEGER PRIMARY KEY, book_id INT, user_id INT, read_status INT, last_modified TEXT)`,
		`INSERT INTO book_read_link VALUES (1, 3, 1, 0, ''), (2, 7, 1, 1, '2026-09-07'), (3, 8, 1, 2, '')`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(q, err)
		}
	}
	db.Close()
	os.WriteFile(filepath.Join(dir, ".key"), []byte(base64.URLEncoding.EncodeToString(key)), 0o600)
	cw, err := ReadCalibreWeb(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(cw.Users) != 1 || cw.Users[0].Email != "ana@example.com" || cw.Users[0].Kindle != "ana@kindle.com" {
		t.Fatalf("users %+v", cw.Users)
	}
	if cw.Mail == nil || cw.Mail.Password != "pw" || cw.Mail.TLS != "starttls" || cw.Mail.From != "kindle@example.com" || cw.Mail.FromName != "Kindle" {
		t.Fatalf("mail %+v err %s", cw.Mail, cw.MailError)
	}
	if len(cw.Shelves) != 1 || cw.Shelves[0].Books[0] != 20 {
		t.Fatalf("shelves %+v", cw.Shelves)
	}
	if len(cw.ReadStates) != 2 || cw.ReadStates[0].Status != "read" || cw.ReadStates[1].Status != "reading" {
		t.Fatalf("reads %+v", cw.ReadStates)
	}
}

// TestRealCopies reads copies of the real databases when their paths are
// given (PLB_CW_DB, PLB_KAVITA_DB) and prints counts only.
func TestRealCopies(t *testing.T) {
	if p := os.Getenv("PLB_CW_DB"); p != "" {
		cw, err := ReadCalibreWeb(p)
		if err != nil {
			t.Fatal(err)
		}
		mail := "none"
		if cw.Mail != nil {
			mail = cw.Mail.Host + " " + cw.Mail.TLS + " password-unsealed=" + map[bool]string{true: "yes", false: "no"}[cw.Mail.Password != ""]
		}
		t.Logf("calibre-web: %d users, mail %s %s, %d shelves, %d read states", len(cw.Users), mail, cw.MailError, len(cw.Shelves), len(cw.ReadStates))
	}
	if p := os.Getenv("PLB_KAVITA_DB"); p != "" {
		k, err := ReadKavita(p)
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("kavita: %d users, %d progress, %d want, %d ratings, %d collections, %d lists, %d annotations", len(k.Users), len(k.Progress), len(k.Want), len(k.Ratings), len(k.Collections), len(k.ReadingLists), len(k.Annotations))
		for _, p := range k.Progress {
			if len(p.Files) > 0 {
				t.Logf("  %d/%d pages %s hash=%q", p.PagesRead, p.Pages, p.Files[0].Path, p.Files[0].Hash)
			}
		}
	}
}

func TestClippings(t *testing.T) {
	clips, err := ParseClippings(strings.NewReader("Na Drini ćuprija (Andrić, Ivo)\r\n- Your Highlight on page 21 | Location 312-314 | Added on Sunday, 3 May 2021 21:14:02\r\n\r\nA bridge.\r\n==========\r\n"))
	if err != nil || len(clips) != 1 {
		t.Fatal(clips, err)
	}
	books := GroupClippings(clips)
	if len(books) != 1 || books[0].Author != "Ivo Andrić" || books[0].Entries[0].Text != "A bridge." {
		t.Fatalf("%+v", books)
	}
}
