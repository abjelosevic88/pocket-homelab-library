-- Book translations into Serbian with Pocket AI: one row per job, owned by
-- the person who started it. Files (source, cached replies, result) live in
-- DATA_DIR/translations/<id>; the finished book is filed into the library.
CREATE TABLE translations (
  id               INTEGER PRIMARY KEY AUTOINCREMENT,
  user_id          INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  source_book_id   INTEGER REFERENCES books(id) ON DELETE SET NULL,
  source_name      TEXT NOT NULL,
  source_size      INTEGER NOT NULL DEFAULT 0,
  title            TEXT NOT NULL DEFAULT '',
  author           TEXT NOT NULL DEFAULT '',
  title_translated TEXT NOT NULL DEFAULT '',
  script           TEXT NOT NULL DEFAULT 'latin',
  model            TEXT NOT NULL DEFAULT '',
  notes            TEXT NOT NULL DEFAULT '',
  status           TEXT NOT NULL DEFAULT 'queued', -- queued, running, done, failed, cancelled
  step             TEXT NOT NULL DEFAULT '',
  units            INTEGER NOT NULL DEFAULT 0,
  chars            INTEGER NOT NULL DEFAULT 0,
  batches          INTEGER NOT NULL DEFAULT 0,
  done_batches     INTEGER NOT NULL DEFAULT 0,
  cached_batches   INTEGER NOT NULL DEFAULT 0,
  warnings         TEXT NOT NULL DEFAULT '[]',
  error            TEXT NOT NULL DEFAULT '',
  result_book_id   INTEGER REFERENCES books(id) ON DELETE SET NULL,
  created_at       TEXT NOT NULL,
  started_at       TEXT NOT NULL DEFAULT '',
  finished_at      TEXT NOT NULL DEFAULT ''
);
CREATE INDEX translations_user ON translations(user_id, id);
CREATE INDEX translations_status ON translations(status, id);
CREATE INDEX translations_source ON translations(source_book_id);
