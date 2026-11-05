-- KOReader's document hash (partial MD5) of each copy handed out that is not
-- the file itself: an EPUB with the catalogue's metadata written in or fixed
-- for Kindle, or a conversion. A reader syncing that copy reports this hash,
-- so it is matched back to the file.
CREATE TABLE served_hashes (
  hash       TEXT PRIMARY KEY,
  file_id    INTEGER NOT NULL REFERENCES files(id) ON DELETE CASCADE,
  created_at TEXT NOT NULL
);
CREATE INDEX served_hashes_file ON served_hashes(file_id);
