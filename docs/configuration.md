# Configuration

Everything is read from environment variables; every value has a default, so a bare `docker run -v ./data:/data` works.
Integration values seed the owner's (first admin's) own settings on first start, once; after that the values saved under
**Settings** win, and clearing one there keeps it cleared.

## Server

| Variable | Default | What it does |
|---|---|---|
| `PORT` / `HOST` | `8080` / `0.0.0.0` | Listen address |
| `BASE_URL` | – | Public URL: notification links, the OIDC callback, what Pocket Core shows |
| `DATA_DIR` | `./data` (`/data` in the image) | SQLite database, covers, cache, `secret.key`, backups |
| `DB_PATH` | `$DATA_DIR/pocket-library.db` | Override the database location |
| `TZ` | – | Time zone for "today", reading days, streaks and the weekly recap |
| `LOG_LEVEL` / `LOG_FORMAT` | `info` / `text` | `debug`, `warn`, `error`; `json` |
| `TRUST_PROXY` | `true` | Honour `X-Forwarded-For` |
| `METRICS_ENABLED` | `true` | Prometheus `/metrics` (row counts only, never content) |
| `USER_AGENT` | `PocketLibrary/1.0 (+repo URL)` | Sent with outgoing requests |

## Sign-in

| Variable | Default | What it does |
|---|---|---|
| `AUTH_MODE` | `local` | `local` (built-in accounts), `proxy` (trust `AUTH_PROXY_HEADER`), `none` (single user, LAN only) |
| `AUTH_PROXY_HEADER` / `AUTH_PROXY_EMAIL_HEADER` | `Remote-User` / `Remote-Email` | Headers for proxy mode |
| `SESSION_TTL` | `720h` | Cookie lifetime |
| `SECURE_COOKIES` | `false` | Set `true` behind HTTPS (on plain http the browser drops the cookie and login bounces) |
| `ADMIN_EMAIL` / `ADMIN_PASSWORD` / `ADMIN_NAME` | – | Create the first admin on an empty database (otherwise the UI asks) |
| `OIDC_ISSUER` / `OIDC_CLIENT_ID` / `OIDC_CLIENT_SECRET` | – | Sign in with an OpenID Connect provider (PocketID, Authelia, Authentik, Keycloak…). The callback is `BASE_URL/api/v1/auth/oidc/callback`. With Pocket Core none of these are needed. |
| `OIDC_NAME` / `OIDC_AUTO_CREATE` | `single sign-on` / `true` | Button label; give an unknown verified email a member account |

## Libraries and files

| Variable | Default | What it does |
|---|---|---|
| `LIBRARIES` | – | Libraries made on the first start (only while there are none): `Name[@books\|comics\|manga]=/folder[\|/another]`, separated by `;`, e.g. `Books=/books;Comics@comics=/comics;Manga@manga=/manga`. Add and change them later under Library admin. |
| `SCAN_EVERY` | `1h` | How often libraries are looked through for new, changed and removed files (each library can set its own interval; minimum 5 minutes) |
| `INGEST_DIR` | – | A drop folder: files put there are filed into the uploads library with its file-name template and removed from the folder (like Calibre-Web Automated's ingest folder). Switch it off under Settings without restarting. |
| `COVERS_DIR` | `$DATA_DIR/covers` | Covers and author photos (thumbnails are made on demand in `thumbs/`) |
| `CACHE_DIR` | `$DATA_DIR/cache` | Converted files, EPUBs with embedded metadata, extracted comic pages; safe to delete |
| `MAX_BOOK_UPLOAD_BYTES` | `2147483648` | Largest upload (all files of one request) |
| `MAX_UPLOAD_BYTES` | `52428800` | Largest JSON import |
| `EBOOK_CONVERT` | looked up on `PATH` | Calibre's `ebook-convert` for conversions (MOBI/AZW3/FB2 → EPUB for Send to Kindle and CrossPoint, EPUB → AZW3, …). The `-calibre` image has it; `off` switches conversions off. EPUB → KEPUB never needs calibre. |

The book folders are read; they are only written to by uploads, the ingest folder and "delete with files". Mount
them read-only if you want none of that.

## Mail (Send to Kindle)

Amazon accepts books mailed from an address on your **Approved Personal Document E-mail List** (Amazon → Manage your
content and devices → Preferences → Personal Document Settings). These seed Settings → Mail once; change them there.

| Variable | Default | What it does |
|---|---|---|
| `SMTP_HOST` / `SMTP_PORT` | – / `587` | Mail server |
| `SMTP_TLS` | `starttls` | `starttls`, `tls` (implicit, port 465) or `none` |
| `SMTP_USER` / `SMTP_PASSWORD` | – | Login (the password is sealed with `DATA_DIR/secret.key`) |
| `SMTP_FROM` / `SMTP_FROM_NAME` | – | The sender Amazon must know |
| `KINDLE_EMAIL` | – | The owner's `…@kindle.com` address, added once as their first device |

## Metadata

| Variable | Default | What it does |
|---|---|---|
| `GOOGLE_BOOKS_API_KEY` | – | Google Books as a second source next to Open Library (no key: Open Library only) |

## Importing from Calibre-Web and Kavita

Mount the old apps' data read-only and point at it; Settings → Import does the rest (a dry run first for Kavita).
Live databases are copied before they are read, never opened in place.

| Variable | What it does |
|---|---|
| `CALIBRE_WEB_DB` | Calibre-Web's `app.db`; the `.key` file next to it decrypts the saved mail password. Brings each person's Kindle address, the mail account, shelves and read status. |
| `CALIBRE_DB` | A Calibre library's `metadata.db`, to match Calibre-Web's book ids by UUID when the files carry no `calibre` identifier |
| `KAVITA_DB` | Kavita's `kavita.db`: reading progress, want to read, ratings, collections and reading lists, per person (matched by email) |

## Notifications

| Variable | Default | What it does |
|---|---|---|
| `NOTIFY_URL` / `NOTIFY_FORMAT` | – / guessed | The owner's push target (Apprise `…/notify/<key>`, an ntfy topic, or JSON). Everyone sets their own under Settings → Notifications. |

## Pocket Core

| Variable | Default | What it does |
|---|---|---|
| `CORE_URL` | – | Register with Pocket Core: one sign-in (Sign in with Pocket Core), events (`library.started`, `library.finished`, `library.highlighted`, `library.sent`), and app links |
| `CORE_REGISTRATION_KEY` | – | Core's registration key |
| `CORE_APP_URL` | `BASE_URL` | How Core reaches this app |

With `CORE_URL` empty the app behaves exactly the same, minus those.
