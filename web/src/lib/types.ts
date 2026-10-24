// Shapes of the JSON API (internal/store/models.go and the server handlers).
// Endpoints are under /api/v1 unless noted.

export interface User { id: number; email: string; name: string; role: 'admin' | 'member'; can_edit: boolean; created_at: string }

export interface APIToken { id: number; user_id: number; name: string; kind: 'token' | 'device'; prefix: string; last_used_at: string | null; created_at: string }

/** GET /settings — box-wide fields only change for admins; secrets come back masked. */
export interface Settings {
  title: string
  setup_complete: boolean
  base_url: string
  core_configured: boolean
  core_registered: boolean
  is_admin: boolean
  can_edit: boolean
  // box-wide (admins)
  smtp_host: string; smtp_port: number; smtp_tls: 'starttls' | 'tls' | 'none'; smtp_user: string; smtp_password: string /* masked, '' = none */; smtp_from: string; smtp_from_name: string
  smtp_configured: boolean
  kindle_fix_epub: boolean
  embed_metadata: boolean
  google_books_key: string // masked
  opds_enabled: boolean
  kosync_enabled: boolean
  ingest_enabled: boolean
  ingest_dir: string // from INGEST_DIR, '' = none
  convert_available: boolean // calibre's ebook-convert is installed
  // personal
  date_format: string
  time_format: string
  reader_prefs: string // JSON owned by the reader
  comic_prefs: string
  hidden_libraries: string
  default_device: number
  library_view: 'grid' | 'list'
  card_size: 's' | 'm' | 'l'
  daily_goal: number
  yearly_goal: number
  notify_url: string; notify_format: 'apprise' | 'ntfy' | 'json'; notify_finished: boolean; notify_sent: boolean; notify_added: boolean; notify_weekly: boolean; notify_configured: boolean
}

// ---------- catalogue ----------

export type LibraryKind = 'books' | 'comics' | 'manga'

/** GET /libraries (the ones the person may open; admins get all with paths). */
export interface Library {
  id: number; name: string; kind: LibraryKind; paths: string[]; access: 'all' | 'people'; people: number[]
  uploads: boolean; upload_template: string; scan_every: number; position: number
  last_scan_at: string; last_scan_ms: number; last_scan_error: string; created_at: string
  books: number
  scanning?: boolean
}

export interface AuthorRef { id: number; name: string }

export type ReadStatus = '' | 'want' | 'reading' | 'read' | 'dnf'

export interface UserBook {
  status: ReadStatus; rating: number /* 1..10 half stars */; favorite: boolean; hidden: boolean
  percentage: number; locator: string; file_id: number
  last_read_at: string; last_device: string; started_at: string; finished_at: string; times_read: number; updated_at: string
}

export interface BookFile {
  id: number; book_id: number; library_id: number; name: string; format: string; size: number; mtime: number
  partial_md5: string; pages: number; missing: boolean; added_at: string
}

export interface Book {
  id: number; library_id: number
  title: string; sort_title: string; subtitle: string; authors: AuthorRef[]
  description?: string; language: string; publisher: string; published: string; isbn: string; identifiers: Record<string, string>
  series_id: number; series: string; series_index: number; tags: string[]
  pages: number; words: number; kind: 'book' | 'comic' | 'manga'; reading_direction: 'ltr' | 'rtl'; age_rating: string
  cover: string /* URL, '' = none; add ?w=200|400|800 */; cover_color: string
  locked: string[]; meta_source: string; missing: boolean; added_at: string; updated_at: string
  formats: string[]
  files?: BookFile[] // detail only
  state?: UserBook
}

/** GET /books/{id} adds context for the book page. */
export interface BookDetail extends Book {
  files: BookFile[]
  library: { id: number; name: string; kind: LibraryKind }
  series_books: Book[] // the other books of its series, in order
  more_by_author: Book[]
  reads: Read[]
  shelves: number[] // ids of the person's shelves it is on
  annotations: number // count
  readable: boolean // the web reader can open one of its files
  sendable: boolean // there is a format some device takes
  bookshelf?: { available: boolean; url?: string; rating?: number; status?: string } // Pocket Bookshelf via Core
}

/** GET /books?… — BookQuery as query parameters (also a smart shelf's filter). */
export interface BookQuery {
  q?: string
  library?: number
  author?: number
  series?: number
  tag?: string
  format?: string
  language?: string
  publisher?: string
  status?: ReadStatus | 'unread' | 'any'
  favorite?: boolean
  rating_min?: number
  kind?: 'book' | 'comic' | 'manga'
  added_since?: string // YYYY-MM-DD
  missing?: 'cover' | 'description' | 'isbn' | 'series' | 'files'
  sort?: 'title' | 'author' | 'added' | 'published' | 'series' | 'rating' | 'last_read' | 'random' | 'pages'
  order?: 'asc' | 'desc'
  offset?: number
  limit?: number // default 60, max 500
}
export interface Page<T> { items: T[]; total: number; offset: number; limit: number }

export interface Author { id: number; name: string; sort_name: string; bio?: string; photo?: string; links?: Record<string, string>; books: number }
export interface Series { id: number; name: string; sort_name: string; description?: string; books: number; read: number; covers: string[] }
export interface Tag { name: string; books: number }

/** GET /home */
export interface Home {
  continue: Book[] // reading, newest first
  on_deck: Book[] // next unread book of a series the person is reading or finished
  recently_added: Book[]
  want: Book[] // want to read
  discover: Book[] // random from authors/tags the person likes, else random
  stats: { reading: number; finished_year: number; yearly_goal: number; minutes_today: number; daily_goal: number; streak: number }
  libraries: Library[]
  pending_sends: number
}

/** GET /search?q= — the command palette. */
export interface SearchResult { books: Book[]; authors: Author[]; series: Series[]; tags: Tag[]; shelves: Shelf[] }

// ---------- per person ----------

export interface Read { id: number; book_id: number; status: 'read' | 'dnf'; started_at: string; finished_at: string; day: string }

export interface Shelf {
  id: number; name: string; description: string; icon: string; kind: 'manual' | 'smart'; filter: string /* JSON BookQuery */
  ordered: boolean; opds: boolean; position: number; books: number; covers: string[]; created_at: string; updated_at: string
}

export interface Annotation {
  id: number; book_id: number; file_id: number; kind: 'highlight' | 'note' | 'bookmark'
  text: string; note: string; color: string; locator: string; chapter: string; percentage: number; page: number
  source: 'web' | 'koreader' | 'kindle' | 'crosspoint' | 'import'; external_id?: string; created_at: string; updated_at: string
  book_title?: string; book_cover?: string
}

export interface ReadingSession {
  id: number; book_id: number; file_id: number; device: string; source: string; day: string
  started_at: string; ended_at: string; seconds: number; start_pct: number; end_pct: number; pages: number; estimated: boolean
  book_title?: string; book_cover?: string
}

/** GET /stats?range=7d|30d|year|all|YYYY */
export interface Stats {
  range: string; from: string; to: string
  seconds: number; sessions: number; pages: number; days_read: number
  finished: number; books_started: number
  streak: number; best_streak: number
  per_day: { day: string; seconds: number; pages: number }[]
  per_month: { month: string; finished: number; seconds: number }[]
  by_format: { format: string; seconds: number }[]
  by_device: { device: string; seconds: number }[]
  top_authors: { id: number; name: string; books: number }[]
  top_tags: { name: string; books: number }[]
  finished_books: Book[]
  pace_pages_per_hour: number
}

// ---------- devices & sending ----------

export type DeviceKind = 'kindle' | 'crosspoint' | 'koreader' | 'kobo' | 'email'

export interface Device {
  id: number; kind: DeviceKind; name: string; email: string; address: string; format: string
  token_id: number; settings: Record<string, unknown>; is_default: boolean; last_seen_at: string; created_at: string
  pending: number
}

/** POST /devices returns the device and, for e-readers that sign in, its app password once. */
export interface DeviceCreated { device: Device; password?: string; username?: string; setup?: DeviceSetup }

/** GET /devices/{id}/setup — what to type into the device. */
export interface DeviceSetup {
  opds_url: string; kosync_url: string; username: string; has_password: boolean
  plugin_url?: string // KOReader plugin zip
  steps: string[]
}

export interface Send {
  id: number; device_id: number; device_name: string; book_id: number; file_id: number; title: string
  format: string; method: 'email' | 'push' | 'pull'; status: 'queued' | 'sending' | 'sent' | 'failed' | 'cancelled'
  error: string; bytes: number; created_at: string; sent_at: string; cover?: string
}

// ---------- admin ----------

export interface ScanStatus { running: boolean; library_id: number; library: string; phase: string; done: number; total: number; added: number; updated: number; removed: number; errors: string[]; started_at: string }

export interface MetadataCandidate {
  source: 'openlibrary' | 'google'; id: string; title: string; subtitle: string; authors: string[]
  description: string; publisher: string; published: string; isbn: string; language: string; pages: number
  series: string; series_index: number; tags: string[]; cover: string /* remote URL */; identifiers: Record<string, string>
}

export interface DuplicateGroup { reason: 'same file' | 'same isbn' | 'same title'; books: Book[] }
