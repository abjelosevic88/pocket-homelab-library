# Backup

Pocket Library never owns your books: they stay in your folders, and the folders are what you back up first.
Everything the app itself keeps is in `DATA_DIR` (`/data` in the container):

| File | What it is |
|---|---|
| `pocket-library.db` (+ `-wal`, `-shm`) | The catalogue (rebuilt by a scan) and what only lives here: each person's place, reads, ratings, reading time, highlights, shelves, devices, sends, settings, accounts, metadata edited by hand |
| `covers/` | Covers and author photos. Covers from files are made again by a scan; covers you uploaded or picked online are not, so keep this folder |
| `cache/` | Converted files, prepared EPUBs, extracted comic pages. Safe to delete, never back up |
| `secret.key` | Seals the mail password and the Google Books key. Lose it and you type them again; nothing else is lost |
| `core.token`, `core.token.oidc.json` | The app's Pocket Core registration (made again by registering) |
| `backups/` | Snapshots made by `pocket-library backup` or Settings → Backup & data |

**Never copy the live database file.** Use one of:

```sh
docker exec pocket-library pocket-library backup            # writes data/backups/pocket-library-<time>.db
curl -H "Authorization: Bearer plb_…" https://library.example.com/api/v1/backup -o library.db   # admins
```

Each person can export their own reading (place, reads, sessions, highlights, shelves) as JSON under
Settings → Backup & data, and import it on another box; books are matched by ISBN, else title and author.

To restore: stop the app, replace `pocket-library.db` with a snapshot (delete stale `-wal`/`-shm`), start it.
