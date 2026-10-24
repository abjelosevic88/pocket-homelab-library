package metadata

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"syscall"
	"time"
)

// ErrPrivateAddress is returned when an image URL points into a private network.
var ErrPrivateAddress = errors.New("the address points to a private or local network")

// cgnat is 100.64.0.0/10 — carrier-grade NAT, and Tailscale's address space.
var cgnat = netip.MustParsePrefix("100.64.0.0/10")

func publicOnly(_, address string, _ syscall.RawConn) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return err
	}
	ip, err := netip.ParseAddr(host)
	if err != nil {
		return err
	}
	ip = ip.Unmap()
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsMulticast() || ip.IsUnspecified() || cgnat.Contains(ip) {
		return ErrPrivateAddress
	}
	return nil
}

// publicHTTP fetches from any public web server but refuses private,
// loopback and tailnet addresses (also after redirects), so a pasted URL
// cannot be used to poke at the local network.
func publicHTTP(timeout time.Duration) *http.Client {
	dialer := &net.Dialer{Timeout: 10 * time.Second, Control: publicOnly}
	tr := &http.Transport{DialContext: dialer.DialContext, TLSHandshakeTimeout: 10 * time.Second, MaxIdleConns: 4, IdleConnTimeout: 30 * time.Second}
	return &http.Client{Timeout: timeout, Transport: tr, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return errors.New("too many redirects")
		}
		return nil
	}}
}

// FetchImage downloads an image of at most maxBytes from a public address.
// Tiny answers are refused: catalogues answer "no cover" with a 1×1 pixel.
func (c *Client) FetchImage(ctx context.Context, rawURL string, maxBytes int64) ([]byte, error) {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return nil, fmt.Errorf("not a web address: %q", rawURL)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", c.UserAgent)
	res, err := publicHTTP(30 * time.Second).Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("image download: HTTP %d", res.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(res.Body, maxBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > maxBytes {
		return nil, fmt.Errorf("image larger than %d MB", maxBytes>>20)
	}
	if !strings.HasPrefix(http.DetectContentType(body), "image/") {
		return nil, errors.New("not an image")
	}
	if len(body) < 1024 {
		return nil, errors.New("no image at that address")
	}
	return body, nil
}
