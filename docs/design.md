# How it works

## Your folders are the library

Pocket Library reads your book folders as they are and never rearranges them. A scan walks every library's
folders, groups the files of one book (an EPUB, a MOBI and a PDF with the same name in the same folder are one
book with three formats; a Calibre-style folder with a `metadata.opf` is one book), and reads its metadata in this
order: a sidecar `.opf` next to the file (what Calibre and Calibre-Web write), then the file itself (EPUB, AZW3,
MOBI, FB2, PDF, ComicInfo.xml in comics), then the file name. Rescans only open files whose size or date changed,
so a library of ten thousand books rescans in seconds.

Anything you edit by hand is **locked**: a scan never overwrites it. The files on disk are never changed either;
when you download or send an EPUB, the metadata edited here is written into a copy.

Comics and manga libraries treat every archive (CBZ, CBR, CB7, CBT) as one issue or chapter, with the folder as
the series and the number from ComicInfo or the file name. Manga reads right to left.

## Calibre-Web and Kavita, in one app

| Calibre-Web (Automated) | Kavita | Pocket Library |
|---|---|---|
| Calibre library, metadata editing, fetch metadata | Folder libraries, series, reading lists | Folder libraries with sidecar-OPF metadata, editing with locks, Open Library + Google Books, bulk edits |
| Send to Kindle, conversions | — | Send to Kindle with Amazon's EPUB fixes and edited metadata, to the Xteink X4 (push or catalogue), to KOReader (plugin); conversions with calibre |
| Ingest folder, duplicates | — | Ingest folder and uploads filed by a template, duplicates by file, ISBN and title |
| OPDS, Kobo sync, KOReader sync | OPDS, KOReader sync | OPDS for KOReader and CrossPoint, KOReader sync that also moves your place between the web reader, devices and formats |
| Basic EPUB reader | EPUB, PDF, comic and manga reader | One reader for EPUB, MOBI, AZW3, FB2, PDF, comics and manga, with highlights, notes, bookmarks, search and reading time |
| Shelves, read status | Collections, want to read, ratings, stats | Shelves (hand-picked, reading lists, smart), status with finish dates, half-star ratings, stats, streaks, goals |

Settings → Import brings over what lived in them: Kindle addresses and the mail account from Calibre-Web, shelves
and read status, and from Kavita everyone's progress, ratings, collections and reading lists.

## Your place, everywhere

Each person has one place per book. The web reader reports it as you read; KOReader and CrossPoint report it
through the KOReader sync protocol at `/kosync`. A device knows a book by a hash of its file (or of its file
name, CrossPoint's default); Pocket Library knows every file's hash, so a position lands on the right book even
when the device's copy came from somewhere else. When the book was read further somewhere else, a device asking
for its position gets that place translated for its own file: an XPointer for an EPUB (KOReader's way of pointing
into a chapter), a page for a PDF or a comic. So you can read on the X4 in bed, carry on in the browser at lunch,
and finish on a Kindle with KOReader.

Reading time comes from the web reader (time with the book open and in use), from KOReader's own statistics
(the plugin), or is estimated from pages turned when a device only sends a position; estimated time is marked.

## Per person, always

Everyone signs in with their own account (or Sign in with Pocket Core). The books are the household's, through
libraries; what each person does with them (place, reads, ratings, highlights, shelves, devices, sends) is
theirs alone, and admins can't see it. Members can be allowed to edit metadata and upload.

## Pocket apps

With Pocket Core, Pocket Library joins the other apps: one sign-in, events on the bus (`library.started`,
`library.finished`, `library.highlighted`, `library.sent`), and links:

- **Pocket Bookshelf** follows your reading here: a finished ebook becomes a read on your shelves, a book you are
  reading moves its page, highlights become quotes. Its rating and status show on the book page here.
- **Pocket Day** and other apps can read one day of reading in a sentence (`/api/v1/day/<date>`, scope `reading:read`).
- **Pocket Hub** and Homepage show it with its icon; `/api/v1/dashboard` feeds a Homepage widget.
