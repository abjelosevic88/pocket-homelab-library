-- CrossPoint's clippings (2.0) mirrored as the device sent them: the layout
-- hints it needs back on restore, a tombstone for each deleted id (a deleted
-- id is never revived), and seq as the monotonic sync cursor (a change
-- re-inserts the row, so it gets a new seq).
CREATE TABLE crosspoint_clippings (
  seq           INTEGER PRIMARY KEY AUTOINCREMENT,
  user_id       INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  document      TEXT NOT NULL,
  clip_id       TEXT NOT NULL,
  book_id       INTEGER REFERENCES books(id) ON DELETE SET NULL,
  annotation_id INTEGER REFERENCES annotations(id) ON DELETE SET NULL,
  item          TEXT NOT NULL DEFAULT '{}',
  deleted       INTEGER NOT NULL DEFAULT 0,
  updated_unix  INTEGER NOT NULL DEFAULT 0,
  UNIQUE (user_id, document, clip_id)
);
CREATE INDEX crosspoint_clippings_doc ON crosspoint_clippings(user_id, document, seq);
