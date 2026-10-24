# API

Everything the web app does goes through this JSON API at `/api/v1`. Sign in with the session cookie, or send a
personal token (Settings → Apps & API) as `Authorization: Bearer plb_…`. Pocket Core device and person tokens
work too when the app is connected to Core. Shapes are in `web/src/lib/types.ts`.

Everything a person does (status, place, reads, reading time, highlights, shelves, devices, sends) is theirs
alone: another person's ids answer 404, admins included. Books are shared through libraries: a person sees the
libraries open to everyone plus the ones they were given.

## Catalogue

| | |
|---|---|
| `GET /libraries` · `POST` · `PUT /libraries/{id}` · `DELETE` | Libraries (changes: admins; folders shown to admins only) |
| `GET /books?q&library&author&series&tag&format&language&publisher&status&favorite&rating_min&kind&added_since&missing&sort&order&offset&limit&seed` | A page of books: `{items, total, offset, limit}`. `status` = `want`, `reading`, `read`, `dnf`, `unread`; `sort` = `title`, `author`, `added`, `published`, `series`, `rating`, `last_read`, `random`, `pages` |
| `GET /books/{id}` | A book with its files, series, more by the author, reads, shelves and, with Pocket Core, your Pocket Bookshelf rating |
| `PUT /books/{id}` | Edit metadata (editors). Every field sent is locked, so scans keep it; send `locked` to set the list |
| `PUT /books/{id}/state` | `{status, rating (1–10 half stars), favorite, hidden, day}`; finishing records a read on `day` |
| `POST /books/bulk` | `{ids, status, favorite, hidden, add_tags, remove_tags, series, language, shelf_add, shelf_remove}` |
| `GET /books/{id}/reads` · `GET /reads?from&to` · `DELETE /reads/{id}` | Finished (and given-up) reads |
| `GET /authors` · `GET /authors/{id}` · `PUT /authors/{id}` | Authors (renaming to an existing name merges) |
| `GET /series` · `GET /series/{id}` · `PUT /series/{id}` | Series |
| `GET /tags` · `GET /facets` | Tags; languages, formats and publishers for filters |
| `GET /shelves` · `POST` · `PUT /shelves/{id}` · `DELETE` · `PUT /shelves/order` | Shelves: `manual` (optionally `ordered`, a reading list) or `smart` (`filter` = a saved book query) |
| `GET /shelves/{id}` · `POST /shelves/{id}/books` · `POST /shelves/{id}/books/remove` · `PUT /shelves/{id}/books/order` | A shelf's books |
| `GET /home` · `GET /search?q` | Home (continue, on deck, want, recently added, discover, stats) and the ⌘K search |

## Files, covers, metadata (editors change them)

| | |
|---|---|
| `GET /books/{id}/cover?w=` · `POST /books/{id}/cover` · `DELETE` | Cover (any width 120–800; upload a file or `{url}`) |
| `GET /authors/{id}/photo?w=` · `POST /authors/{id}/enrich` | Author photo; look the author up again |
| `GET /books/{id}/metadata/search?q&isbn` · `POST /books/{id}/metadata/apply` | Open Library and Google Books; `{candidate, fields}` |
| `POST /metadata/fetch-missing` | Fill empty descriptions, covers and ISBNs where a match is certain (admins) |
| `POST /upload` | Multipart `files` (+ `library_id`): filed with the library's template |
| `DELETE /books/{id}?files=1` | Delete a book with its files (editors) |
| `GET /duplicates` | Same file, same ISBN, same title and author |
| `GET /scan` · `POST /scan` · `POST /libraries/{id}/scan` · `POST /books/{id}/rescan` | Scanning (`?full=1` reads every file again) |
| `GET /books/{id}/download?file&format` · `GET /files/{id}/download` | Download, as is or converted (`epub`, `kepub`, `azw3`, `mobi`, `pdf`); EPUBs carry the metadata edited here |

## Reading

| | |
|---|---|
| `GET /books/{id}/reader?file` | What the web reader opens: the file, its engine, your place and highlights |
| `GET /files/{id}/content` · `GET /files/{id}/pages` · `GET /files/{id}/pages/{n}?w=` | File bytes (ranges); comic pages |
| `POST /books/{id}/progress` | `{file_id, percentage, locator, seconds, pages}`: your place and reading time |
| `GET /books/{id}/annotations` · `POST` · `PUT /annotations/{id}` · `DELETE` | Highlights, notes, bookmarks |
| `GET /annotations?q&kind&book` · `GET /annotations/export?book&format=md\|json\|csv` | All of them; export |
| `GET /sessions?book&from&to` · `DELETE /sessions/{id}` | Reading sessions (`estimated` when an e-reader only sent a position) |
| `GET /stats?range=7d\|30d\|90d\|year\|all\|YYYY` | Time, pages, days, finished books, streaks, pace, by day/month/format/device |
| `GET /day/2026-10-24` | One day of reading in a sentence and a list (Pocket Day) |
| `GET /dashboard` | Books, reading now, minutes today and this week, the current book: for a Homepage widget |
| `GET /reading/current` · `GET /reading/feed?since=RFC3339` | What you are reading; what changed since a moment (Pocket Bookshelf follows this) |

## Devices and sending

| | |
|---|---|
| `GET /devices` · `POST` · `PUT /devices/{id}` · `DELETE` | Kindle (mail), e-mail devices, Xteink X4 with CrossPoint, KOReader. E-readers that sign in get an app password, shown once |
| `GET /devices/{id}/setup` · `POST /devices/{id}/password` · `POST /devices/{id}/default` · `POST /devices/{id}/test` · `POST /devices/{id}/provision` | What to type into the device; new password; test; put the catalogue on an X4 |
| `POST /books/{id}/send` · `POST /send` | `{device_id, file_id, format}` / `{book_ids, device_id}` |
| `GET /sends` · `DELETE /sends/{id}` · `POST /sends/{id}/retry` | What was sent where |

## Imports

| | |
|---|---|
| `GET /import/sources` | Which old apps are mounted |
| `POST /import/calibre-web?dry=1` · `POST /import/kavita?dry=1` | Kindle addresses, the mail account, shelves, read status / progress, ratings, collections, reading lists (admins) |
| `POST /import/kindle-clippings` | `My Clippings.txt` → highlights (everyone) |
| `GET /export.json` · `POST /import` | Your own reading as JSON |

## E-readers (outside `/api/v1`)

| | |
|---|---|
| `/opds` | OPDS 1.2 catalogue (HTTP Basic: your email + an app password or token). Continue reading, want to read, recently added, *To send to this device*, shelves, libraries, authors, series, tags, search |
| `/kosync` | KOReader sync server (KOReader, CrossPoint). Books are matched by KOReader's file hash or by file name; your place moves between devices, formats and the web reader |
| `/kosync/api/v1/clippings/{hash}` | CrossPoint 2.0 highlight sync |
| `/koreader/plugin.zip` · `/koreader/v1/…` | The KOReader plugin and its API (queue, highlights, reading statistics) |

## Pocket Core

Pocket Library registers as `library`, publishes `library.started`, `library.finished`,
`library.highlighted`, `library.sent` (each with `user_email`), and offers two scopes to other Pocket apps:
`reading:read` (feed, current, sessions, reads, annotations, stats, day, dashboard) and `library:read` (books,
covers). It uses Pocket Bookshelf's `books:read` to show your rating there on a book's page.
