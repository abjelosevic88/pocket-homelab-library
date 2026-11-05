-- A book an admin keeps off e-readers: it stays in the library and the web
-- reader, but no device's catalogue (OPDS) lists it.
ALTER TABLE books ADD COLUMN ereader_off INTEGER NOT NULL DEFAULT 0;
