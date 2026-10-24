// Package mail sends messages over SMTP: STARTTLS, implicit TLS or plain,
// with PLAIN or LOGIN authentication, and attachments (Send to Kindle).
package mail

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"mime"
	"net"
	"net/mail"
	"net/smtp"
	"strconv"
	"strings"
	"time"
	"unicode"
)

// Account is how to reach the mail server.
type Account struct {
	Host     string
	Port     int
	TLS      string // starttls | tls | none
	User     string
	Password string
	From     string // address
	FromName string
	// InsecureSkipVerify accepts any server certificate (tests only).
	InsecureSkipVerify bool
}

// Configured reports whether the account can send.
func (a Account) Configured() bool { return a.Host != "" && a.From != "" }

// Attachment is a file sent along.
type Attachment struct {
	Name string // file name; non-ASCII names are encoded
	Type string // media type
	Data []byte
}

// Message is one mail.
type Message struct {
	To          []string
	Subject     string
	Text        string
	Attachments []Attachment
}

// Timeout bounds a whole send.
const Timeout = 60 * time.Second

// Send delivers a message.
func Send(ctx context.Context, a Account, m Message) error {
	if !a.Configured() {
		return errors.New("mail is not set up (Settings → Mail)")
	}
	if len(m.To) == 0 {
		return errors.New("no recipient")
	}
	from, err := mail.ParseAddress(a.From)
	if err != nil {
		return fmt.Errorf("sender address %q: %w", a.From, err)
	}
	for _, to := range m.To {
		if _, err := mail.ParseAddress(to); err != nil {
			return fmt.Errorf("recipient address %q: %w", to, err)
		}
	}
	body, err := Build(a, m, time.Now())
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, Timeout)
	defer cancel()
	port := a.Port
	if port == 0 {
		port = 587
		if a.TLS == "tls" {
			port = 465
		}
	}
	addr := net.JoinHostPort(a.Host, strconv.Itoa(port))
	tlsCfg := &tls.Config{ServerName: a.Host, InsecureSkipVerify: a.InsecureSkipVerify} //nolint:gosec // tests only
	d := net.Dialer{}
	var conn net.Conn
	if a.TLS == "tls" {
		conn, err = (&tls.Dialer{NetDialer: &d, Config: tlsCfg}).DialContext(ctx, "tcp", addr)
	} else {
		conn, err = d.DialContext(ctx, "tcp", addr)
	}
	if err != nil {
		return fmt.Errorf("connect to %s: %w", addr, err)
	}
	if dl, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(dl)
	}
	c, err := smtp.NewClient(conn, a.Host)
	if err != nil {
		conn.Close()
		return fmt.Errorf("smtp: %w", err)
	}
	defer c.Close()
	if err := c.Hello(helloName(from.Address)); err != nil {
		return fmt.Errorf("smtp hello: %w", err)
	}
	if a.TLS == "starttls" {
		if ok, _ := c.Extension("STARTTLS"); !ok {
			return errors.New("the mail server does not offer STARTTLS (choose TLS or none)")
		}
		if err := c.StartTLS(tlsCfg); err != nil {
			return fmt.Errorf("starttls: %w", err)
		}
	}
	if a.User != "" {
		if err := c.Auth(authFor(c, a)); err != nil {
			return fmt.Errorf("sign-in to the mail server failed: %w", err)
		}
	}
	if err := c.Mail(from.Address); err != nil {
		return fmt.Errorf("smtp MAIL FROM: %w", err)
	}
	for _, to := range m.To {
		addr, _ := mail.ParseAddress(to)
		if err := c.Rcpt(addr.Address); err != nil {
			return fmt.Errorf("smtp RCPT TO %s: %w", addr.Address, err)
		}
	}
	w, err := c.Data()
	if err != nil {
		return fmt.Errorf("smtp DATA: %w", err)
	}
	if _, err := w.Write(body); err != nil {
		return fmt.Errorf("smtp write: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("smtp: %w", err)
	}
	return c.Quit()
}

func helloName(from string) string {
	if _, dom, ok := strings.Cut(from, "@"); ok && dom != "" {
		return dom
	}
	return "localhost"
}

// authFor picks PLAIN, or LOGIN when the server only offers that. Go's
// PlainAuth refuses unencrypted connections except to localhost; with TLS
// "none" the person chose plain text, so it is allowed there.
func authFor(c *smtp.Client, a Account) smtp.Auth {
	_, mechs := c.Extension("AUTH")
	if strings.Contains(strings.ToUpper(mechs), "PLAIN") || mechs == "" {
		return &plainAuth{user: a.User, pass: a.Password}
	}
	return &loginAuth{user: a.User, pass: a.Password}
}

type plainAuth struct{ user, pass string }

func (p *plainAuth) Start(*smtp.ServerInfo) (string, []byte, error) {
	return "PLAIN", []byte("\x00" + p.user + "\x00" + p.pass), nil
}

func (p *plainAuth) Next(_ []byte, more bool) ([]byte, error) {
	if more {
		return nil, errors.New("unexpected server challenge")
	}
	return nil, nil
}

type loginAuth struct{ user, pass string }

func (l *loginAuth) Start(*smtp.ServerInfo) (string, []byte, error) { return "LOGIN", nil, nil }

func (l *loginAuth) Next(from []byte, more bool) ([]byte, error) {
	if !more {
		return nil, nil
	}
	switch strings.ToLower(strings.TrimSpace(string(from))) {
	case "username:":
		return []byte(l.user), nil
	case "password:":
		return []byte(l.pass), nil
	}
	return nil, fmt.Errorf("unexpected server challenge %q", from)
}

// Build renders a message as RFC 5322 bytes with CRLF line endings.
func Build(a Account, m Message, now time.Time) ([]byte, error) {
	var b bytes.Buffer
	h := func(k, v string) { b.WriteString(k + ": " + v + "\r\n") }
	from := (&mail.Address{Name: a.FromName, Address: a.From}).String()
	h("From", from)
	var tos []string
	for _, t := range m.To {
		addr, err := mail.ParseAddress(t)
		if err != nil {
			return nil, err
		}
		tos = append(tos, addr.String())
	}
	h("To", strings.Join(tos, ", "))
	h("Subject", mime.QEncoding.Encode("utf-8", oneLine(m.Subject)))
	h("Date", now.Format(time.RFC1123Z))
	h("Message-ID", "<"+randomID()+"@"+helloName(a.From)+">")
	h("MIME-Version", "1.0")
	h("X-Mailer", "Pocket Library")
	text := strings.ReplaceAll(strings.ReplaceAll(m.Text, "\r\n", "\n"), "\n", "\r\n")
	if len(m.Attachments) == 0 {
		h("Content-Type", "text/plain; charset=utf-8")
		h("Content-Transfer-Encoding", "base64")
		b.WriteString("\r\n")
		writeBase64(&b, []byte(text))
		return b.Bytes(), nil
	}
	boundary := "plb-" + randomID()
	h("Content-Type", `multipart/mixed; boundary="`+boundary+`"`)
	b.WriteString("\r\n--" + boundary + "\r\n")
	b.WriteString("Content-Type: text/plain; charset=utf-8\r\nContent-Transfer-Encoding: base64\r\n\r\n")
	writeBase64(&b, []byte(text))
	for _, at := range m.Attachments {
		typ := at.Type
		if typ == "" {
			typ = "application/octet-stream"
		}
		b.WriteString("--" + boundary + "\r\n")
		b.WriteString("Content-Type: " + typ + "; name=" + quoteParam(at.Name) + "\r\n")
		b.WriteString("Content-Transfer-Encoding: base64\r\n")
		b.WriteString("Content-Disposition: attachment; " + FilenameParams(at.Name) + "\r\n\r\n")
		writeBase64(&b, at.Data)
	}
	b.WriteString("--" + boundary + "--\r\n")
	return b.Bytes(), nil
}

// FilenameParams is a Content-Disposition filename: an ASCII fallback plus
// the UTF-8 name per RFC 2231 when it differs.
func FilenameParams(name string) string {
	ascii := ASCIIName(name)
	if ascii == name {
		return "filename=" + quoteParam(name)
	}
	return "filename=" + quoteParam(ascii) + "; filename*=UTF-8''" + pctEncode(name)
}

func quoteParam(v string) string {
	if isASCII(v) {
		return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(v) + `"`
	}
	return `"` + mime.QEncoding.Encode("utf-8", v) + `"`
}

func pctEncode(s string) string {
	var b strings.Builder
	for _, c := range []byte(s) {
		if c < 0x80 && (unicode.IsLetter(rune(c)) || unicode.IsDigit(rune(c)) || strings.IndexByte("!#$&+-.^_`|~", c) >= 0) {
			b.WriteByte(c)
		} else {
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return b.String()
}

func isASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= 0x80 {
			return false
		}
	}
	return true
}

var translit = map[rune]string{
	'đ': "dj", 'Đ': "Dj", 'ž': "z", 'Ž': "Z", 'č': "c", 'Č': "C", 'ć': "c", 'Ć': "C", 'š': "s", 'Š': "S",
	'ß': "ss", 'æ': "ae", 'Æ': "Ae", 'ø': "o", 'Ø': "O", 'œ': "oe", 'Œ': "Oe", 'ł': "l", 'Ł': "L",
	'–': "-", '—': "-", '‘': "'", '’': "'", '“': `"`, '”': `"`, '…': "...",
}

var cyr = map[rune]string{
	'а': "a", 'б': "b", 'в': "v", 'г': "g", 'д': "d", 'ђ': "dj", 'е': "e", 'ж': "z", 'з': "z", 'и': "i", 'ј': "j", 'к': "k", 'л': "l", 'љ': "lj",
	'м': "m", 'н': "n", 'њ': "nj", 'о': "o", 'п': "p", 'р': "r", 'с': "s", 'т': "t", 'ћ': "c", 'у': "u", 'ф': "f", 'х': "h", 'ц': "c", 'ч': "c",
	'џ': "dz", 'ш': "s", 'й': "j", 'ы': "y", 'э': "e", 'ю': "ju", 'я': "ja", 'ь': "", 'ъ': "", 'щ': "sc", 'ё': "e",
}

// ASCIIName transliterates a file name to plain ASCII (Serbian Latin and
// Cyrillic, common Western letters; accents dropped; anything else → '_').
func ASCIIName(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r < 0x80:
			b.WriteRune(r)
		case translit[r] != "":
			b.WriteString(translit[r])
		case unicode.Is(unicode.Cyrillic, r):
			t, ok := cyr[unicode.ToLower(r)]
			if !ok {
				t = "_"
			}
			if unicode.IsUpper(r) && t != "" {
				t = strings.ToUpper(t[:1]) + t[1:]
			}
			b.WriteString(t)
		default:
			if base := stripAccent(r); base != 0 {
				b.WriteRune(base)
			} else {
				b.WriteByte('_')
			}
		}
	}
	return b.String()
}

// stripAccent maps Latin letters with diacritics to their base letter.
func stripAccent(r rune) rune {
	const from = "àáâãäåāăąçćĉċčďèéêëēĕėęěĝğġģĥìíîïĩīĭįĵķĺļľñńņňòóôõöōŏőŕŗřśŝşšţťùúûüũūŭůűųŵýÿŷźżžÀÁÂÃÄÅĀĂĄÇĆĈĊČĎÈÉÊËĒĔĖĘĚĜĞĠĢĤÌÍÎÏĨĪĬĮĴĶĹĻĽÑŃŅŇÒÓÔÕÖŌŎŐŔŖŘŚŜŞŠŢŤÙÚÛÜŨŪŬŮŰŲŴÝŸŶŹŻŽ"
	const to = "aaaaaaaaacccccdeeeeeeeeegggghiiiiiiiijklllnnnnoooooooorrrssssttuuuuuuuuuuwyyyzzzAAAAAAAAACCCCCDEEEEEEEEEGGGGHIIIIIIIIJKLLLNNNNOOOOOOOORRRSSSSTTUUUUUUUUUUWYYYZZZ"
	fr := []rune(from)
	for i, c := range fr {
		if c == r {
			return rune(to[i])
		}
	}
	return 0
}

func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

func writeBase64(b *bytes.Buffer, data []byte) {
	enc := base64.StdEncoding.EncodeToString(data)
	for len(enc) > 76 {
		b.WriteString(enc[:76] + "\r\n")
		enc = enc[76:]
	}
	if enc != "" {
		b.WriteString(enc + "\r\n")
	}
}

func randomID() string {
	p := make([]byte, 12)
	_, _ = rand.Read(p)
	return hex.EncodeToString(p)
}
