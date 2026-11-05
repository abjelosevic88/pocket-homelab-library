-- Who kept a book off e-readers (or on): '' nobody, 'auto' Library (a book
-- with only a PDF; back on when another format arrives), 'manual' a person,
-- whose choice the automatic rule never overrides. Books already kept off:
-- the PDF-only ones count as automatic (Improve kept them off), others as a
-- person's choice.
ALTER TABLE books ADD COLUMN ereader_set TEXT NOT NULL DEFAULT '';
UPDATE books SET ereader_set = CASE
    WHEN EXISTS (SELECT 1 FROM files f WHERE f.book_id = books.id AND f.missing = 0)
     AND NOT EXISTS (SELECT 1 FROM files f WHERE f.book_id = books.id AND f.missing = 0 AND f.format != 'pdf') THEN 'auto'
    ELSE 'manual' END
  WHERE ereader_off = 1;
