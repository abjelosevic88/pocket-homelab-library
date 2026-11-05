-- Pocket AI sorting new books: where each one should live (the main library,
-- a tech library or the review library) and why, for an admin to apply.
CREATE TABLE book_sort (
  book_id      INTEGER PRIMARY KEY REFERENCES books(id) ON DELETE CASCADE,
  status       TEXT NOT NULL DEFAULT 'pending', -- pending | suggested | applied | kept | failed
  target       TEXT NOT NULL DEFAULT '',        -- books | tech | review
  reason       TEXT NOT NULL DEFAULT '',
  confidence   REAL NOT NULL DEFAULT 0,
  source       TEXT NOT NULL DEFAULT '',        -- upload | ingest | scan | asked
  requested_by INTEGER REFERENCES users(id) ON DELETE SET NULL,
  error        TEXT NOT NULL DEFAULT '',
  created_at   TEXT NOT NULL,
  updated_at   TEXT NOT NULL
);
CREATE INDEX book_sort_status ON book_sort(status, updated_at);
