CREATE TABLE users (
  id            INTEGER PRIMARY KEY AUTOINCREMENT,
  email         TEXT NOT NULL UNIQUE,
  name          TEXT NOT NULL DEFAULT '',
  password_hash TEXT NOT NULL DEFAULT '',
  role          TEXT NOT NULL DEFAULT 'member',  -- admin | member
  can_edit      INTEGER NOT NULL DEFAULT 0,      -- a member who may edit metadata, upload and delete books
  created_at    TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
  updated_at    TEXT
);

CREATE TABLE sessions (
  id         TEXT PRIMARY KEY,
  user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  expires_at TEXT NOT NULL,
  created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);

-- Personal tokens. kind 'device' is an app password for an e-reader: it signs
-- in to OPDS (HTTP Basic, plain password) and KOReader sync (the MD5 of the
-- password), so key_md5 keeps the SHA-256 of that MD5 as well.
CREATE TABLE api_tokens (
  id           INTEGER PRIMARY KEY AUTOINCREMENT,
  user_id      INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  name         TEXT NOT NULL DEFAULT '',
  kind         TEXT NOT NULL DEFAULT 'token',   -- token | device
  token_hash   TEXT NOT NULL UNIQUE,
  md5_hash     TEXT NOT NULL DEFAULT '',        -- sha256(md5hex(token)) for KOSync
  prefix       TEXT NOT NULL DEFAULT '',
  last_used_at TEXT,
  created_at   TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);
CREATE INDEX api_tokens_md5 ON api_tokens(md5_hash) WHERE md5_hash <> '';

-- Box-wide settings (title, mail, conversions); everything else is personal.
CREATE TABLE settings (
  key   TEXT PRIMARY KEY,
  value TEXT NOT NULL DEFAULT ''
);

CREATE TABLE user_settings (
  user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  key     TEXT NOT NULL,
  value   TEXT NOT NULL DEFAULT '',
  PRIMARY KEY (user_id, key)
);

CREATE TABLE cache (
  key        TEXT PRIMARY KEY,
  value      TEXT NOT NULL DEFAULT '',
  fetched_at TEXT NOT NULL
);

-- ============ the catalogue (shared by the household) ============

-- A library is a set of folders of one kind. access 'all' = everyone on the
-- box; 'people' = only those in library_people (admins always).
CREATE TABLE libraries (
  id              INTEGER PRIMARY KEY AUTOINCREMENT,
  name            TEXT NOT NULL,
  kind            TEXT NOT NULL DEFAULT 'books',   -- books | comics | manga
  paths           TEXT NOT NULL DEFAULT '[]',      -- JSON array of folders
  access          TEXT NOT NULL DEFAULT 'all',     -- all | people
  uploads         INTEGER NOT NULL DEFAULT 0,      -- uploads and the ingest folder are filed here
  upload_template TEXT NOT NULL DEFAULT '{author}/{author} - {title}',
  scan_every      INTEGER NOT NULL DEFAULT 60,     -- minutes; 0 = only by hand
  position        INTEGER NOT NULL DEFAULT 0,
  last_scan_at    TEXT NOT NULL DEFAULT '',
  last_scan_ms    INTEGER NOT NULL DEFAULT 0,
  last_scan_error TEXT NOT NULL DEFAULT '',
  created_at      TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);

CREATE TABLE library_people (
  library_id INTEGER NOT NULL REFERENCES libraries(id) ON DELETE CASCADE,
  user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  PRIMARY KEY (library_id, user_id)
);

CREATE TABLE authors (
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  name       TEXT NOT NULL UNIQUE COLLATE NOCASE,
  sort_name  TEXT NOT NULL DEFAULT '',
  bio        TEXT NOT NULL DEFAULT '',
  photo      TEXT NOT NULL DEFAULT '',            -- file name in COVERS_DIR/authors
  ol_key     TEXT NOT NULL DEFAULT '',            -- Open Library author key
  links      TEXT NOT NULL DEFAULT '{}',          -- JSON {wikipedia: url, …}
  enriched_at TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);

CREATE TABLE series (
  id          INTEGER PRIMARY KEY AUTOINCREMENT,
  name        TEXT NOT NULL UNIQUE COLLATE NOCASE,
  sort_name   TEXT NOT NULL DEFAULT '',
  description TEXT NOT NULL DEFAULT '',
  created_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);

-- A book is one work in one library; its files (EPUB, MOBI, PDF twins in the
-- same folder with the same name) hang below it. group_key binds the twins:
-- the folder plus the file name without extension (lower case), or the
-- folder alone for Calibre's one-book-per-folder layout.
CREATE TABLE books (
  id                INTEGER PRIMARY KEY AUTOINCREMENT,
  library_id        INTEGER NOT NULL REFERENCES libraries(id) ON DELETE CASCADE,
  group_key         TEXT NOT NULL,
  title             TEXT NOT NULL DEFAULT '',
  sort_title        TEXT NOT NULL DEFAULT '',
  subtitle          TEXT NOT NULL DEFAULT '',
  description       TEXT NOT NULL DEFAULT '',     -- plain text / light HTML
  language          TEXT NOT NULL DEFAULT '',     -- ISO 639-1 where known
  publisher         TEXT NOT NULL DEFAULT '',
  published         TEXT NOT NULL DEFAULT '',     -- YYYY or YYYY-MM-DD
  isbn              TEXT NOT NULL DEFAULT '',     -- ISBN-13 where known
  identifiers       TEXT NOT NULL DEFAULT '{}',   -- JSON {isbn, asin, goodreads, google, openlibrary, calibre, uuid, …}
  series_id         INTEGER REFERENCES series(id) ON DELETE SET NULL,
  series_index      REAL NOT NULL DEFAULT 0,
  pages             INTEGER NOT NULL DEFAULT 0,   -- printed pages (comics: images)
  words             INTEGER NOT NULL DEFAULT 0,
  kind              TEXT NOT NULL DEFAULT 'book', -- book | comic | manga
  reading_direction TEXT NOT NULL DEFAULT 'ltr',  -- ltr | rtl (manga)
  age_rating        TEXT NOT NULL DEFAULT '',
  cover             TEXT NOT NULL DEFAULT '',     -- cover file name in COVERS_DIR ('' = none)
  cover_color       TEXT NOT NULL DEFAULT '',     -- #rrggbb average, for placeholders
  locked            TEXT NOT NULL DEFAULT '[]',   -- JSON field names edited by hand: scans leave them
  meta_source       TEXT NOT NULL DEFAULT 'file', -- file | sidecar | edited | openlibrary | google | calibre
  missing           INTEGER NOT NULL DEFAULT 0,   -- every file is gone
  added_at          TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
  updated_at        TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
  UNIQUE (library_id, group_key)
);
CREATE INDEX books_series ON books(series_id, series_index);
CREATE INDEX books_added ON books(added_at);
CREATE INDEX books_sort ON books(sort_title);

CREATE TABLE book_authors (
  book_id   INTEGER NOT NULL REFERENCES books(id) ON DELETE CASCADE,
  author_id INTEGER NOT NULL REFERENCES authors(id) ON DELETE CASCADE,
  position  INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY (book_id, author_id)
);
CREATE INDEX book_authors_author ON book_authors(author_id);

CREATE TABLE book_tags (
  book_id INTEGER NOT NULL REFERENCES books(id) ON DELETE CASCADE,
  tag     TEXT NOT NULL COLLATE NOCASE,
  PRIMARY KEY (book_id, tag)
);
CREATE INDEX book_tags_tag ON book_tags(tag);

CREATE TABLE files (
  id          INTEGER PRIMARY KEY AUTOINCREMENT,
  book_id     INTEGER NOT NULL REFERENCES books(id) ON DELETE CASCADE,
  library_id  INTEGER NOT NULL REFERENCES libraries(id) ON DELETE CASCADE,
  path        TEXT NOT NULL UNIQUE,              -- absolute path as this server sees it
  format      TEXT NOT NULL,                     -- epub | kepub | pdf | mobi | azw3 | azw | prc | fb2 | fbz | txt | rtf | doc | docx | djvu | cbz | cbr | cb7 | cbt
  size        INTEGER NOT NULL DEFAULT 0,
  mtime       INTEGER NOT NULL DEFAULT 0,        -- unix seconds
  partial_md5 TEXT NOT NULL DEFAULT '',          -- KOReader's document hash
  name_md5    TEXT NOT NULL DEFAULT '',          -- KOReader's "file name" hash (md5 of the base name)
  sha256      TEXT NOT NULL DEFAULT '',          -- whole file, filled lazily (duplicates)
  pages       INTEGER NOT NULL DEFAULT 0,        -- comic images / PDF pages
  missing     INTEGER NOT NULL DEFAULT 0,
  added_at    TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
  scanned_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);
CREATE INDEX files_book ON files(book_id);
CREATE INDEX files_md5 ON files(partial_md5) WHERE partial_md5 <> '';
CREATE INDEX files_name_md5 ON files(name_md5) WHERE name_md5 <> '';
CREATE INDEX files_sha ON files(sha256) WHERE sha256 <> '';

-- Search: rowid = books.id, kept in step by the store (not a content table).
CREATE VIRTUAL TABLE books_fts USING fts5(title, authors, series, tags, description, publisher, isbn, tokenize = 'unicode61 remove_diacritics 2');

-- ============ per person ============

-- Where one person stands with one book.
CREATE TABLE user_books (
  user_id      INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  book_id      INTEGER NOT NULL REFERENCES books(id) ON DELETE CASCADE,
  status       TEXT NOT NULL DEFAULT '',     -- '' | want | reading | read | dnf
  rating       INTEGER NOT NULL DEFAULT 0,   -- 1..10 = half stars, 0 = none
  favorite     INTEGER NOT NULL DEFAULT 0,
  hidden       INTEGER NOT NULL DEFAULT 0,   -- left out of Home and browsing
  percentage   REAL NOT NULL DEFAULT 0,      -- 0..1
  locator      TEXT NOT NULL DEFAULT '',     -- JSON reader position (see docs/api.md)
  file_id      INTEGER REFERENCES files(id) ON DELETE SET NULL,
  last_read_at TEXT NOT NULL DEFAULT '',
  last_device  TEXT NOT NULL DEFAULT '',
  started_at   TEXT NOT NULL DEFAULT '',
  finished_at  TEXT NOT NULL DEFAULT '',
  times_read   INTEGER NOT NULL DEFAULT 0,
  updated_at   TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
  PRIMARY KEY (user_id, book_id)
);
CREATE INDEX user_books_reading ON user_books(user_id, last_read_at);

-- Every finished (or given up) read, so re-reads count.
CREATE TABLE reads (
  id          INTEGER PRIMARY KEY AUTOINCREMENT,
  user_id     INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  book_id     INTEGER NOT NULL REFERENCES books(id) ON DELETE CASCADE,
  status      TEXT NOT NULL DEFAULT 'read',  -- read | dnf
  started_at  TEXT NOT NULL DEFAULT '',
  finished_at TEXT NOT NULL,
  day         TEXT NOT NULL                  -- local date finished
);
CREATE INDEX reads_user ON reads(user_id, finished_at);

-- Time spent reading: the web reader reports while open; e-readers' syncs
-- are joined into sessions (a gap over 30 minutes starts a new one).
CREATE TABLE reading_sessions (
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  book_id    INTEGER NOT NULL REFERENCES books(id) ON DELETE CASCADE,
  file_id    INTEGER REFERENCES files(id) ON DELETE SET NULL,
  device     TEXT NOT NULL DEFAULT '',
  source     TEXT NOT NULL DEFAULT 'web',    -- web | kosync | koreader | import
  day        TEXT NOT NULL,                  -- local date it started
  started_at TEXT NOT NULL,
  ended_at   TEXT NOT NULL,
  seconds    INTEGER NOT NULL DEFAULT 0,
  start_pct  REAL NOT NULL DEFAULT 0,
  end_pct    REAL NOT NULL DEFAULT 0,
  pages      INTEGER NOT NULL DEFAULT 0,
  estimated  INTEGER NOT NULL DEFAULT 0      -- minutes guessed from pace, not measured
);
CREATE INDEX reading_sessions_user ON reading_sessions(user_id, started_at);
CREATE INDEX reading_sessions_day ON reading_sessions(user_id, day);

-- KOReader sync: the exact position string a device sent, per document hash.
CREATE TABLE kosync_progress (
  user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  document   TEXT NOT NULL,
  book_id    INTEGER REFERENCES books(id) ON DELETE SET NULL,
  file_id    INTEGER REFERENCES files(id) ON DELETE SET NULL,
  progress   TEXT NOT NULL DEFAULT '',
  percentage REAL NOT NULL DEFAULT 0,
  device     TEXT NOT NULL DEFAULT '',
  device_id  TEXT NOT NULL DEFAULT '',
  timestamp  INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY (user_id, document)
);

-- Highlights, notes and bookmarks: from the web reader, KOReader (the plugin)
-- or imported (Kindle's My Clippings.txt). external_id dedupes re-syncs.
CREATE TABLE annotations (
  id          INTEGER PRIMARY KEY AUTOINCREMENT,
  user_id     INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  book_id     INTEGER NOT NULL REFERENCES books(id) ON DELETE CASCADE,
  file_id     INTEGER REFERENCES files(id) ON DELETE SET NULL,
  kind        TEXT NOT NULL DEFAULT 'highlight', -- highlight | note | bookmark
  text        TEXT NOT NULL DEFAULT '',
  note        TEXT NOT NULL DEFAULT '',
  color       TEXT NOT NULL DEFAULT 'yellow',    -- yellow | green | blue | pink | purple | underline
  locator     TEXT NOT NULL DEFAULT '',          -- JSON (cfi / xpointer pos0+pos1 / page)
  chapter     TEXT NOT NULL DEFAULT '',
  percentage  REAL NOT NULL DEFAULT 0,
  page        INTEGER NOT NULL DEFAULT 0,
  source      TEXT NOT NULL DEFAULT 'web',       -- web | koreader | kindle | import
  external_id TEXT NOT NULL DEFAULT '',
  created_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
  updated_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);
CREATE INDEX annotations_book ON annotations(user_id, book_id);
CREATE UNIQUE INDEX annotations_ext ON annotations(user_id, book_id, source, external_id) WHERE external_id <> '';

-- Shelves: hand-picked (optionally ordered = a reading list) or smart (a saved filter).
CREATE TABLE shelves (
  id          INTEGER PRIMARY KEY AUTOINCREMENT,
  user_id     INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  name        TEXT NOT NULL,
  description TEXT NOT NULL DEFAULT '',
  icon        TEXT NOT NULL DEFAULT '',
  kind        TEXT NOT NULL DEFAULT 'manual',  -- manual | smart
  filter      TEXT NOT NULL DEFAULT '{}',      -- JSON BookQuery (smart shelves)
  ordered     INTEGER NOT NULL DEFAULT 0,      -- a reading list in the person's order
  opds        INTEGER NOT NULL DEFAULT 1,      -- shown in the OPDS catalogue
  position    INTEGER NOT NULL DEFAULT 0,
  created_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
  updated_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
  UNIQUE (user_id, name)
);

CREATE TABLE shelf_books (
  shelf_id INTEGER NOT NULL REFERENCES shelves(id) ON DELETE CASCADE,
  book_id  INTEGER NOT NULL REFERENCES books(id) ON DELETE CASCADE,
  position INTEGER NOT NULL DEFAULT 0,
  added_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
  PRIMARY KEY (shelf_id, book_id)
);

-- A person's reading devices and how books reach them.
CREATE TABLE devices (
  id           INTEGER PRIMARY KEY AUTOINCREMENT,
  user_id      INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  kind         TEXT NOT NULL,                  -- kindle | crosspoint | koreader | kobo | email
  name         TEXT NOT NULL,
  email        TEXT NOT NULL DEFAULT '',       -- kindle / email: where books are mailed
  address      TEXT NOT NULL DEFAULT '',       -- crosspoint: http://<device> on the same network
  format       TEXT NOT NULL DEFAULT '',       -- preferred format ('' = best for the kind)
  token_id     INTEGER REFERENCES api_tokens(id) ON DELETE SET NULL, -- its app password
  settings     TEXT NOT NULL DEFAULT '{}',     -- JSON, per kind
  is_default   INTEGER NOT NULL DEFAULT 0,
  last_seen_at TEXT NOT NULL DEFAULT '',
  created_at   TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);
CREATE INDEX devices_user ON devices(user_id);

-- Books sent to a device: mailed (Kindle), pushed (CrossPoint on the same
-- network) or waiting to be fetched (KOReader plugin, CrossPoint's catalogue).
CREATE TABLE sends (
  id          INTEGER PRIMARY KEY AUTOINCREMENT,
  user_id     INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  device_id   INTEGER REFERENCES devices(id) ON DELETE SET NULL,
  device_name TEXT NOT NULL DEFAULT '',
  book_id     INTEGER REFERENCES books(id) ON DELETE SET NULL,
  file_id     INTEGER REFERENCES files(id) ON DELETE SET NULL,
  title       TEXT NOT NULL DEFAULT '',
  format      TEXT NOT NULL DEFAULT '',
  method      TEXT NOT NULL DEFAULT 'email',   -- email | push | pull
  status      TEXT NOT NULL DEFAULT 'queued',  -- queued | sending | sent | failed | cancelled
  error       TEXT NOT NULL DEFAULT '',
  bytes       INTEGER NOT NULL DEFAULT 0,
  created_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
  sent_at     TEXT NOT NULL DEFAULT ''
);
CREATE INDEX sends_user ON sends(user_id, created_at);
CREATE INDEX sends_pending ON sends(device_id, status);
