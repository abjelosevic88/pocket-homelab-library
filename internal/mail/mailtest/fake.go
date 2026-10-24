// Package mailtest is a tiny SMTP server for tests: it takes every message
// (optionally only after AUTH PLAIN/LOGIN) and keeps it in memory.
package mailtest

import (
	"bufio"
	"encoding/base64"
	"net"
	"strings"
	"sync"
)

// Received is one delivered message.
type Received struct {
	From string
	To   []string
	Data string
	User string // the signed-in user ('' = none)
}

// Server is a fake SMTP server on 127.0.0.1.
type Server struct {
	Addr     string
	User     string // when set, AUTH is required with this user and Password
	Password string
	ln       net.Listener
	mu       sync.Mutex
	msgs     []Received
}

// Start listens on a free port.
func Start(user, password string) (*Server, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	s := &Server{Addr: ln.Addr().String(), User: user, Password: password, ln: ln}
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go s.serve(c)
		}
	}()
	return s, nil
}

// Close stops the server.
func (s *Server) Close() { s.ln.Close() }

// Messages returns what was delivered so far.
func (s *Server) Messages() []Received {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Received(nil), s.msgs...)
}

// Port returns the port the server listens on.
func (s *Server) Port() string {
	_, p, _ := net.SplitHostPort(s.Addr)
	return p
}

func (s *Server) serve(c net.Conn) {
	defer c.Close()
	r := bufio.NewReader(c)
	say := func(line string) { _, _ = c.Write([]byte(line + "\r\n")) }
	read := func() string {
		l, err := r.ReadString('\n')
		if err != nil {
			return "QUIT"
		}
		return strings.TrimRight(l, "\r\n")
	}
	say("220 fake ESMTP")
	var cur Received
	authed := s.User == ""
	for {
		line := read()
		cmd := strings.ToUpper(line)
		switch {
		case strings.HasPrefix(cmd, "EHLO"), strings.HasPrefix(cmd, "HELO"):
			say("250-fake")
			say("250-AUTH PLAIN LOGIN")
			say("250 8BITMIME")
		case strings.HasPrefix(cmd, "AUTH PLAIN"):
			arg := strings.TrimSpace(line[len("AUTH PLAIN"):])
			if arg == "" {
				say("334 ")
				arg = read()
			}
			raw, _ := base64.StdEncoding.DecodeString(arg)
			parts := strings.Split(string(raw), "\x00")
			if len(parts) == 3 && parts[1] == s.User && parts[2] == s.Password {
				authed, cur.User = true, parts[1]
				say("235 ok")
			} else {
				say("535 bad credentials")
			}
		case strings.HasPrefix(cmd, "AUTH LOGIN"):
			say("334 " + base64.StdEncoding.EncodeToString([]byte("Username:")))
			u, _ := base64.StdEncoding.DecodeString(read())
			say("334 " + base64.StdEncoding.EncodeToString([]byte("Password:")))
			p, _ := base64.StdEncoding.DecodeString(read())
			if string(u) == s.User && string(p) == s.Password {
				authed, cur.User = true, string(u)
				say("235 ok")
			} else {
				say("535 bad credentials")
			}
		case strings.HasPrefix(cmd, "MAIL FROM:"):
			if !authed {
				say("530 authentication required")
				continue
			}
			cur.From = addrOf(line[len("MAIL FROM:"):])
			say("250 ok")
		case strings.HasPrefix(cmd, "RCPT TO:"):
			cur.To = append(cur.To, addrOf(line[len("RCPT TO:"):]))
			say("250 ok")
		case cmd == "DATA":
			say("354 go on")
			var b strings.Builder
			for {
				l := read()
				if l == "." || l == "QUIT" {
					break
				}
				b.WriteString(strings.TrimPrefix(l, ".") + "\r\n")
			}
			cur.Data = b.String()
			s.mu.Lock()
			s.msgs = append(s.msgs, cur)
			s.mu.Unlock()
			cur = Received{User: cur.User}
			say("250 queued")
		case cmd == "RSET", cmd == "NOOP":
			say("250 ok")
		case cmd == "QUIT":
			say("221 bye")
			return
		default:
			say("502 unknown")
		}
	}
}

// addrOf takes the address out of "<a@b> PARAM=…".
func addrOf(v string) string {
	v = strings.TrimSpace(v)
	if i := strings.IndexByte(v, '<'); i >= 0 {
		if j := strings.IndexByte(v[i:], '>'); j > 0 {
			return v[i+1 : i+j]
		}
	}
	return strings.Fields(v)[0]
}
