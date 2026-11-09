-- Linked file imports are personal, unlike the pre-existing shared catalogue.
ALTER TABLE libraries ADD COLUMN owner_user_id INTEGER REFERENCES users(id) ON DELETE CASCADE;
CREATE UNIQUE INDEX libraries_owner ON libraries(owner_user_id) WHERE owner_user_id IS NOT NULL;

CREATE TABLE linked_book_imports (
  user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  sha256 TEXT NOT NULL,
  book_id INTEGER NOT NULL REFERENCES books(id) ON DELETE CASCADE,
  file_id INTEGER NOT NULL REFERENCES files(id) ON DELETE CASCADE,
  shelf_id INTEGER REFERENCES shelves(id) ON DELETE SET NULL,
  source_app TEXT NOT NULL,
  source_url TEXT NOT NULL,
  original_name TEXT NOT NULL,
  idempotency_key TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL,
  PRIMARY KEY (user_id, sha256)
);
