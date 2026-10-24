// Package config loads runtime configuration from environment variables.
//
// Every option has a sensible default so that `docker run` with a single
// volume and a folder of books works out of the box. See
// docs/configuration.md for the full list.
package config

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// Config holds all runtime settings.
type Config struct {
	Host           string
	Port           int
	BaseURL        string
	DataDir        string
	DBPath         string
	CoversDir      string
	CacheDir       string // converted files, extracted pages
	LogLevel       string
	LogFormat      string // text | json
	TrustProxy     bool
	MetricsEnabled bool

	MaxUploadBytes     int64 // JSON imports
	MaxBookUploadBytes int64 // book uploads, all files of one request together

	AuthMode             string // local | proxy | none
	AuthProxyHeader      string
	AuthProxyEmailHeader string
	SessionTTL           time.Duration
	SecureCookies        bool
	AdminEmail           string
	AdminPassword        string
	AdminName            string

	// Sign in with an OpenID Connect provider. On a box with Pocket Core none
	// of these are needed: the app uses the client Core issues on registration.
	OIDCIssuer       string
	OIDCClientID     string
	OIDCClientSecret string
	OIDCName         string // button label
	OIDCAutoCreate   bool   // unknown emails get a member account

	// Libraries created on the first start (only while there are none):
	// "Books=/books;Comics@comics=/comics;Manga@manga=/manga|/more-manga".
	Libraries string
	// A folder watched for new books (Calibre-Web Automated's ingest folder):
	// files dropped there are filed into the uploads library and removed.
	IngestDir string
	// How often every library is scanned for new, changed and removed files.
	ScanEvery time.Duration

	// Calibre's ebook-convert, for conversions (MOBI → EPUB, EPUB → AZW3, …).
	// Empty = looked up on PATH; "off" = no conversions besides KEPUB.
	EbookConvert string

	// Mail for Send to Kindle (and other e-mail devices): the box's account,
	// copied once into Settings → Mail where the admin can change it.
	SMTPHost     string
	SMTPPort     int
	SMTPTLS      string // starttls | tls | none
	SMTPUser     string
	SMTPPassword string
	SMTPFrom     string
	SMTPFromName string
	// The owner's Kindle address, added once as their first device.
	KindleEmail string

	// Metadata lookups: Open Library needs nothing, Google Books a key.
	GoogleBooksKey string

	// Read-only paths to the apps this one replaces, for Settings → Import.
	CalibreWebDB string // Calibre-Web's app.db (its .key beside it decrypts the mail password)
	CalibreDB    string // a Calibre library's metadata.db
	KavitaDB     string // Kavita's kavita.db

	// The owner's other integrations, copied once into the owner's own
	// settings; the values saved under Settings win.
	NotifyURL    string
	NotifyFormat string // apprise | ntfy | json

	// Outbound lookups identify as this.
	UserAgent string

	// Pocket Core: when CORE_URL is set the app registers itself on start,
	// publishes events and accepts the tokens Core hands to paired phones.
	CoreURL             string
	CoreRegistrationKey string
	CoreAppURL          string // how Core reaches this app; defaults to BASE_URL
}

func env(key, def string) string {
	if v, ok := os.LookupEnv(key); ok && strings.TrimSpace(v) != "" {
		return strings.TrimSpace(v)
	}
	return def
}

func envBool(key string, def bool) bool {
	switch strings.ToLower(env(key, "")) {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	}
	return def
}

func envInt(key string, def int) int {
	if v := env(key, ""); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

func envInt64(key string, def int64) int64 {
	if v := env(key, ""); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			return n
		}
	}
	return def
}

func envDuration(key string, def time.Duration) time.Duration {
	if v := env(key, ""); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return def
}

// Load reads configuration from the environment.
func Load() (*Config, error) {
	dataDir := env("DATA_DIR", "./data")
	c := &Config{
		Host:           env("HOST", "0.0.0.0"),
		Port:           envInt("PORT", 8080),
		BaseURL:        strings.TrimRight(env("BASE_URL", ""), "/"),
		DataDir:        dataDir,
		DBPath:         env("DB_PATH", dataDir+"/pocket-library.db"),
		CoversDir:      env("COVERS_DIR", dataDir+"/covers"),
		CacheDir:       env("CACHE_DIR", dataDir+"/cache"),
		LogLevel:       strings.ToLower(env("LOG_LEVEL", "info")),
		LogFormat:      strings.ToLower(env("LOG_FORMAT", "text")),
		TrustProxy:     envBool("TRUST_PROXY", true),
		MetricsEnabled: envBool("METRICS_ENABLED", true),

		MaxUploadBytes:     envInt64("MAX_UPLOAD_BYTES", 50<<20),
		MaxBookUploadBytes: envInt64("MAX_BOOK_UPLOAD_BYTES", 2<<30),

		AuthMode:             strings.ToLower(env("AUTH_MODE", "local")),
		AuthProxyHeader:      env("AUTH_PROXY_HEADER", "Remote-User"),
		AuthProxyEmailHeader: env("AUTH_PROXY_EMAIL_HEADER", "Remote-Email"),
		SessionTTL:           envDuration("SESSION_TTL", 720*time.Hour),
		SecureCookies:        envBool("SECURE_COOKIES", false),
		AdminEmail:           strings.ToLower(env("ADMIN_EMAIL", "")),
		AdminPassword:        env("ADMIN_PASSWORD", ""),
		AdminName:            env("ADMIN_NAME", "Admin"),
		OIDCIssuer:           strings.TrimRight(env("OIDC_ISSUER", ""), "/"),
		OIDCClientID:         env("OIDC_CLIENT_ID", ""),
		OIDCClientSecret:     env("OIDC_CLIENT_SECRET", ""),
		OIDCName:             env("OIDC_NAME", "single sign-on"),
		OIDCAutoCreate:       envBool("OIDC_AUTO_CREATE", true),

		Libraries:    env("LIBRARIES", ""),
		IngestDir:    strings.TrimRight(env("INGEST_DIR", ""), "/"),
		ScanEvery:    envDuration("SCAN_EVERY", time.Hour),
		EbookConvert: env("EBOOK_CONVERT", ""),

		SMTPHost:     env("SMTP_HOST", ""),
		SMTPPort:     envInt("SMTP_PORT", 587),
		SMTPTLS:      strings.ToLower(env("SMTP_TLS", "starttls")),
		SMTPUser:     env("SMTP_USER", ""),
		SMTPPassword: env("SMTP_PASSWORD", ""),
		SMTPFrom:     env("SMTP_FROM", ""),
		SMTPFromName: env("SMTP_FROM_NAME", ""),
		KindleEmail:  strings.ToLower(env("KINDLE_EMAIL", "")),

		GoogleBooksKey: env("GOOGLE_BOOKS_API_KEY", ""),

		CalibreWebDB: env("CALIBRE_WEB_DB", ""),
		CalibreDB:    env("CALIBRE_DB", ""),
		KavitaDB:     env("KAVITA_DB", ""),

		NotifyURL:    env("NOTIFY_URL", ""),
		NotifyFormat: strings.ToLower(env("NOTIFY_FORMAT", "")),

		UserAgent: env("USER_AGENT", "PocketLibrary/1.0 (+https://github.com/abjelosevic88/pocket-homelab-library)"),

		CoreURL:             strings.TrimRight(env("CORE_URL", ""), "/"),
		CoreRegistrationKey: env("CORE_REGISTRATION_KEY", ""),
		CoreAppURL:          strings.TrimRight(env("CORE_APP_URL", ""), "/"),
	}
	if c.CoreAppURL == "" {
		c.CoreAppURL = c.BaseURL
	}
	switch c.AuthMode {
	case "local", "proxy", "none":
	default:
		return nil, fmt.Errorf("AUTH_MODE must be one of local, proxy, none (got %q)", c.AuthMode)
	}
	if c.Port <= 0 || c.Port > 65535 {
		return nil, fmt.Errorf("PORT out of range: %d", c.Port)
	}
	if c.OIDCIssuer != "" && (c.OIDCClientID == "" || c.BaseURL == "") {
		return nil, fmt.Errorf("OIDC_ISSUER needs OIDC_CLIENT_ID and BASE_URL (the callback is BASE_URL/api/v1/auth/oidc/callback)")
	}
	switch c.SMTPTLS {
	case "starttls", "tls", "none":
	default:
		return nil, fmt.Errorf("SMTP_TLS must be one of starttls, tls, none (got %q)", c.SMTPTLS)
	}
	if c.MaxBookUploadBytes <= 0 {
		return nil, fmt.Errorf("MAX_BOOK_UPLOAD_BYTES must be positive")
	}
	if c.ScanEvery > 0 && c.ScanEvery < 5*time.Minute {
		c.ScanEvery = 5 * time.Minute
	}
	return c, nil
}

// Addr returns host:port.
func (c *Config) Addr() string { return fmt.Sprintf("%s:%d", c.Host, c.Port) }

// ConvertPath is the ebook-convert binary to use, "" when conversions are off
// or calibre is not installed.
func (c *Config) ConvertPath() string {
	switch c.EbookConvert {
	case "off", "none", "false", "0":
		return ""
	case "":
		if p, err := exec.LookPath("ebook-convert"); err == nil {
			return p
		}
		return ""
	}
	if _, err := os.Stat(c.EbookConvert); err == nil {
		return c.EbookConvert
	}
	return ""
}

// SeedLibrary is one library from LIBRARIES.
type SeedLibrary struct {
	Name  string
	Kind  string // books | comics | manga
	Paths []string
}

// SeedLibraries parses LIBRARIES ("Books=/books;Comics@comics=/comics|/more").
func (c *Config) SeedLibraries() []SeedLibrary {
	var out []SeedLibrary
	for _, part := range strings.Split(c.Libraries, ";") {
		name, paths, ok := strings.Cut(strings.TrimSpace(part), "=")
		if !ok || strings.TrimSpace(name) == "" {
			continue
		}
		kind := "books"
		if n, k, ok := strings.Cut(name, "@"); ok {
			name, kind = n, strings.ToLower(strings.TrimSpace(k))
		}
		switch kind {
		case "books", "comics", "manga":
		default:
			kind = "books"
		}
		var ps []string
		for _, p := range strings.Split(paths, "|") {
			if p = strings.TrimRight(strings.TrimSpace(p), "/"); p != "" {
				ps = append(ps, p)
			}
		}
		if len(ps) > 0 {
			out = append(out, SeedLibrary{Name: strings.TrimSpace(name), Kind: kind, Paths: ps})
		}
	}
	return out
}
