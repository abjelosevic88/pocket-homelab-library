// Package crosspoint talks to the web server of an Xteink X4 (or X3)
// running the CrossPoint firmware: the server only runs while the reader is
// on its File Transfer screen, on port 80, without authentication.
package crosspoint

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Client reaches one device.
type Client struct {
	Base string // http://host[:port]
	HTTP *http.Client
}

// New makes a client for an address like "192.168.1.50", "crosspoint.local"
// or "http://192.168.4.1".
func New(address string) (*Client, error) {
	a := strings.TrimSpace(address)
	if a == "" {
		return nil, errors.New("no address")
	}
	if !strings.Contains(a, "://") {
		a = "http://" + a
	}
	u, err := url.Parse(a)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return nil, fmt.Errorf("not a device address: %q", address)
	}
	return &Client{Base: u.Scheme + "://" + u.Host, HTTP: &http.Client{Timeout: 10 * time.Minute}}, nil
}

// ErrUnreachable means the device did not answer (off, asleep, not on its
// File Transfer screen, or on a network this server cannot reach).
var ErrUnreachable = errors.New("the reader did not answer")

func (c *Client) do(req *http.Request) (*http.Response, error) {
	res, err := c.HTTP.Do(req)
	if err != nil {
		var ue *url.Error
		if errors.As(err, &ue) {
			return nil, fmt.Errorf("%w: %v", ErrUnreachable, ue.Err)
		}
		return nil, fmt.Errorf("%w: %v", ErrUnreachable, err)
	}
	return res, nil
}

// Status is what the device reports about itself.
type Status struct {
	Version  string `json:"version"`
	Family   string `json:"firmwareFamily"`
	IP       string `json:"ip"`
	Mode     string `json:"mode"` // STA | AP
	RSSI     int    `json:"rssi"`
	FreeHeap int    `json:"freeHeap"`
	Uptime   int64  `json:"uptime"`
	Device   string `json:"device"`
}

// Status asks GET /api/status (a quick probe: 5 seconds).
func (c *Client) Status(ctx context.Context) (*Status, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.Base+"/api/status", nil)
	if err != nil {
		return nil, err
	}
	res, err := c.do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("the device answered %s (is it a CrossPoint reader?)", res.Status)
	}
	var st Status
	if err := json.NewDecoder(io.LimitReader(res.Body, 64<<10)).Decode(&st); err != nil {
		return nil, fmt.Errorf("the device's status is not CrossPoint's: %w", err)
	}
	return &st, nil
}

func davPath(folder, name string) string {
	var parts []string
	for _, p := range strings.Split(strings.Trim(folder, "/"), "/") {
		if p != "" {
			parts = append(parts, url.PathEscape(p))
		}
	}
	if name != "" {
		parts = append(parts, url.PathEscape(name))
	}
	return "/" + strings.Join(parts, "/")
}

// Upload puts a file into a folder on the SD card ("/Books"), replacing a
// file of that name: WebDAV PUT (the device writes a temp file and renames
// it), creating the folder first; the multipart form of the web page as a
// fallback for firmware without WebDAV.
func (c *Client) Upload(ctx context.Context, folder, name string, data []byte) error {
	if strings.ContainsAny(name, `/\:`) || name == "" || name == "." || name == ".." {
		return fmt.Errorf("file name %q is not allowed on the device", name)
	}
	if strings.Trim(folder, "/") != "" {
		if err := c.mkcol(ctx, folder); err != nil {
			return err
		}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, c.Base+davPath(folder, name), bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.ContentLength = int64(len(data))
	req.Header.Set("Content-Type", "application/epub+zip")
	res, err := c.do(req)
	if err != nil {
		return err
	}
	io.Copy(io.Discard, res.Body)
	res.Body.Close()
	switch {
	case res.StatusCode >= 200 && res.StatusCode < 300:
		return nil
	case res.StatusCode == http.StatusMethodNotAllowed || res.StatusCode == http.StatusNotImplemented || res.StatusCode == http.StatusNotFound:
		return c.uploadForm(ctx, folder, name, data)
	}
	return fmt.Errorf("the device refused the book: %s", res.Status)
}

func (c *Client) mkcol(ctx context.Context, folder string) error {
	path := ""
	for _, p := range strings.Split(strings.Trim(folder, "/"), "/") {
		if p == "" {
			continue
		}
		path += "/" + p
		req, err := http.NewRequestWithContext(ctx, "MKCOL", c.Base+davPath(path, ""), nil)
		if err != nil {
			return err
		}
		res, err := c.do(req)
		if err != nil {
			return err
		}
		io.Copy(io.Discard, res.Body)
		res.Body.Close()
		// 201 made, 405 exists already; anything else: let the upload say what is wrong
	}
	return nil
}

func (c *Client) uploadForm(ctx context.Context, folder, name string, data []byte) error {
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, err := mw.CreateFormFile("file", name)
	if err != nil {
		return err
	}
	fw.Write(data)
	mw.Close()
	dir := "/" + strings.Trim(folder, "/")
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.Base+"/upload?path="+url.QueryEscape(dir), &body)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	res, err := c.do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	msg, _ := io.ReadAll(io.LimitReader(res.Body, 4<<10))
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("the device refused the book: %s %s", res.Status, strings.TrimSpace(string(msg)))
	}
	return nil
}

// AddOPDS saves a catalogue on the device (Settings → OPDS servers), so the
// reader can browse and download from it on its own.
func (c *Client) AddOPDS(ctx context.Context, name, catalog, username, password string) error {
	b, _ := json.Marshal(map[string]string{"name": name, "url": catalog, "username": username, "password": password})
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.Base+"/api/opds", bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := c.do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	msg, _ := io.ReadAll(io.LimitReader(res.Body, 4<<10))
	if res.StatusCode >= 300 {
		return fmt.Errorf("the device refused the catalogue: %s %s", res.Status, strings.TrimSpace(string(msg)))
	}
	return nil
}
