-- A fingerprint of each book's cover (64-bit difference hash, hex) and the
-- cover file it was taken from, to find copies of a book whose details differ.
ALTER TABLE books ADD COLUMN cover_hash TEXT NOT NULL DEFAULT '';
ALTER TABLE books ADD COLUMN cover_hash_of TEXT NOT NULL DEFAULT '';
