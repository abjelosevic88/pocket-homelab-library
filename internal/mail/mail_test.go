package mail

import (
	"context"
	"encoding/base64"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/abjelosevic88/pocket-homelab-library/internal/mail/mailtest"
)

func TestSendWithAttachment(t *testing.T) {
	srv, err := mailtest.Start("me@example.com", "secret")
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	port, _ := strconv.Atoi(srv.Port())
	a := Account{Host: "127.0.0.1", Port: port, TLS: "none", User: "me@example.com", Password: "secret", From: "kindle@example.com", FromName: "Pocket Library"}
	data := []byte(strings.Repeat("book ", 100))
	err = Send(context.Background(), a, Message{To: []string{"ana@kindle.com"}, Subject: "Na Drini ćuprija", Text: "Here it is.",
		Attachments: []Attachment{{Name: "Ivo Andrić - Na Drini ćuprija.epub", Type: "application/epub+zip", Data: data}}})
	if err != nil {
		t.Fatal(err)
	}
	msgs := srv.Messages()
	if len(msgs) != 1 || msgs[0].To[0] != "ana@kindle.com" || msgs[0].From != "kindle@example.com" || msgs[0].User != "me@example.com" {
		t.Fatalf("messages = %+v", msgs)
	}
	d := msgs[0].Data
	for _, want := range []string{`filename="Ivo Andric - Na Drini cuprija.epub"`, "filename*=UTF-8''Ivo%20Andri%C4%87", "Subject: =?utf-8?q?", "Content-Type: application/epub+zip", base64.StdEncoding.EncodeToString(data)[:60]} {
		if !strings.Contains(d, want) {
			t.Errorf("message lacks %q:\n%s", want, d[:min(len(d), 1500)])
		}
	}
}

func TestSendBadPassword(t *testing.T) {
	srv, err := mailtest.Start("me@example.com", "secret")
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	port, _ := strconv.Atoi(srv.Port())
	a := Account{Host: "127.0.0.1", Port: port, TLS: "none", User: "me@example.com", Password: "nope", From: "kindle@example.com"}
	if err := Send(context.Background(), a, Message{To: []string{"x@kindle.com"}, Subject: "x", Text: "x"}); err == nil || !strings.Contains(err.Error(), "sign-in") {
		t.Fatalf("err = %v", err)
	}
}

func TestBuildPlain(t *testing.T) {
	b, err := Build(Account{From: "a@b.c", FromName: "Džon Đokić"}, Message{To: []string{"x@y.z"}, Subject: "Hi", Text: "line1\nline2"}, time.Unix(0, 0))
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	if !strings.Contains(s, "From: =?utf-8?q?") || !strings.Contains(s, "Message-ID: <") || !strings.Contains(s, "@b.c>") {
		t.Fatal(s)
	}
}

func TestASCIIName(t *testing.T) {
	for in, want := range map[string]string{"Ivo Andrić - Na Drini ćuprija.epub": "Ivo Andric - Na Drini cuprija.epub", "Đorđe": "Djordje", "Чаробњак": "Carobnjak", "Ève": "Eve"} {
		if got := ASCIIName(in); got != want {
			t.Errorf("ASCIIName(%q) = %q, want %q", in, got, want)
		}
	}
}
