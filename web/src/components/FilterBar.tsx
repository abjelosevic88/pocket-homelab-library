// Search, filters and sort for a list of books. Controlled: the value is the
// query (as strings); Browse keeps it in the URL, smart shelves in their filter.
import { useEffect, useState } from 'react'
import { api, V1 } from '../lib/api'
import { useApp } from '../lib/app-context'
import type { Author, Library, Series, Tag } from '../lib/types'
import { formatLabel } from '../lib/books'
import { Icon } from './icons'
import { Select } from './Select'
import { MenuButton, Segmented, useDebounce } from './ui'

export type Filters = Record<string, string>

export const SORTS = [
  { value: 'title', label: 'Title' }, { value: 'author', label: 'Author' }, { value: 'added', label: 'Recently added' },
  { value: 'last_read', label: 'Last read' }, { value: 'published', label: 'Published' }, { value: 'series', label: 'Series' },
  { value: 'rating', label: 'My rating' }, { value: 'pages', label: 'Length' }, { value: 'random', label: 'Shuffle' },
]
/** Sorts that read best newest/biggest first. */
const DESC_FIRST = new Set(['added', 'last_read', 'published', 'rating', 'pages'])

const STATUSES = [
  { value: '', label: 'Any status' }, { value: 'unread', label: 'Not started' }, { value: 'want', label: 'Want to read' },
  { value: 'reading', label: 'Reading' }, { value: 'read', label: 'Read' }, { value: 'dnf', label: 'Didn’t finish' },
]
const KINDS = [{ value: '', label: 'Books & comics' }, { value: 'book', label: 'Books' }, { value: 'comic', label: 'Comics' }, { value: 'manga', label: 'Manga' }]
const MISSING = [{ value: '', label: 'Complete or not' }, { value: 'cover', label: 'No cover' }, { value: 'description', label: 'No description' }, { value: 'isbn', label: 'No ISBN' }, { value: 'series', label: 'No series' }, { value: 'files', label: 'Files gone' }]

/** The query keys a filter can carry, in a stable order. */
export const FILTER_KEYS = ['q', 'library', 'kind', 'status', 'favorite', 'rating_min', 'format', 'language', 'publisher', 'tag', 'author', 'series', 'added_since', 'missing', 'sort', 'order', 'seed']

export function filtersToQuery(f: Filters, extra: Record<string, string | number> = {}): string {
  const p = new URLSearchParams()
  for (const k of FILTER_KEYS) if (f[k]) p.set(k, f[k])
  for (const [k, v] of Object.entries(extra)) p.set(k, String(v))
  return p.toString()
}

let facetsCache: Promise<{ languages: Tag[]; formats: Tag[]; publishers: Tag[] }> | null = null
let libsCache: Promise<Library[]> | null = null

export function FilterBar({ value, onChange, fixed = [], total, view, onView, size, onSize, defaultSort = 'title', extraSorts = [] }: {
  value: Filters; onChange: (f: Filters) => void; fixed?: string[]; total?: number; defaultSort?: string; extraSorts?: { value: string; label: string }[]
  view?: 'grid' | 'list'; onView?: (v: 'grid' | 'list') => void; size?: 's' | 'm' | 'l'; onSize?: (s: 's' | 'm' | 'l') => void
}) {
  const { settings, user } = useApp()
  const editor = !!(settings?.can_edit || user?.role === 'admin')
  const [q, setQ] = useState(value.q || '')
  const dq = useDebounce(q, 250)
  const [facets, setFacets] = useState<{ languages: Tag[]; formats: Tag[]; publishers: Tag[] } | null>(null)
  const [libs, setLibs] = useState<Library[]>([])
  const [names, setNames] = useState<{ author?: string; series?: string }>({})
  const [more, setMore] = useState(() => !!(value.format || value.language || value.missing || value.publisher || value.rating_min || value.added_since))
  useEffect(() => { setQ(value.q || '') }, [value.q])
  useEffect(() => { if ((dq || '') !== (value.q || '')) onChange({ ...value, q: dq }) }, [dq]) // eslint-disable-line react-hooks/exhaustive-deps
  useEffect(() => {
    facetsCache ||= api.get(`${V1}/facets`)
    libsCache ||= api.get<Library[]>(`${V1}/libraries`)
    facetsCache.then(setFacets).catch(() => { facetsCache = null })
    libsCache.then(setLibs).catch(() => { libsCache = null })
  }, [])
  useEffect(() => {
    if (value.author) api.get<{ author: Author }>(`${V1}/authors/${value.author}`).then(r => setNames(n => ({ ...n, author: r.author.name }))).catch(() => {})
    if (value.series) api.get<{ series: Series }>(`${V1}/series/${value.series}`).then(r => setNames(n => ({ ...n, series: r.series.name }))).catch(() => {})
  }, [value.author, value.series])

  const set = (k: string, v: string) => {
    const n = { ...value, [k]: v }
    if (k === 'sort') { n.order = DESC_FIRST.has(v) ? 'desc' : ''; n.seed = v === 'random' ? String(Math.floor(Math.random() * 1e6)) : '' }
    onChange(n)
  }
  const show = (k: string) => !fixed.includes(k)
  const chips: { k: string; label: string }[] = []
  if (value.author && show('author')) chips.push({ k: 'author', label: `By ${names.author || '…'}` })
  if (value.series && show('series')) chips.push({ k: 'series', label: `Series: ${names.series || '…'}` })
  if (value.tag && show('tag')) chips.push({ k: 'tag', label: `#${value.tag}` })
  if (value.publisher && show('publisher')) chips.push({ k: 'publisher', label: value.publisher })
  if (value.favorite && show('favorite')) chips.push({ k: 'favorite', label: 'Favourites' })
  const sort = value.sort || defaultSort
  const hasMore = ['format', 'language', 'publisher', 'rating_min', 'added_since', 'favorite', 'missing'].some(k => !fixed.includes(k))
  const desc = value.order === 'desc'
  return (
    <div className="filter-bar">
      {(show('q') || show('status') || hasMore) && <div className="filter-row">
        {show('q') && <label className="search-box"><Icon name="search" size={15} /><input value={q} onChange={e => setQ(e.target.value)} placeholder="Title, author, series, tag, ISBN…" aria-label="Search books" />{q && <button type="button" className="btn ghost icon-only xs" aria-label="Clear" onClick={() => setQ('')}><Icon name="x" size={13} /></button>}</label>}
        {show('library') && libs.length > 1 && <Select ariaLabel="Library" value={value.library || ''} onChange={v => set('library', v)} options={[{ value: '', label: 'All libraries' }, ...libs.map(l => ({ value: String(l.id), label: l.name }))]} />}
        {show('status') && <Select ariaLabel="Status" value={value.status || ''} onChange={v => set('status', v)} options={STATUSES} />}
        {show('kind') && libs.some(l => l.kind !== 'books') && <Select ariaLabel="Kind" value={value.kind || ''} onChange={v => set('kind', v)} options={KINDS} />}
        {hasMore && <button type="button" className={`btn sm ${more ? 'on' : ''}`} onClick={() => setMore(m => !m)}><Icon name="plus" size={14} />More filters</button>}
      </div>}
      {more && hasMore && (
        <div className="filter-row">
          {show('format') && <Select ariaLabel="Format" value={value.format || ''} onChange={v => set('format', v)} options={[{ value: '', label: 'Any format' }, ...(facets?.formats || []).map(f => ({ value: f.name, label: `${formatLabel(f.name)}`, hint: f.books.toLocaleString() }))]} />}
          {show('language') && <Select ariaLabel="Language" value={value.language || ''} onChange={v => set('language', v)} options={[{ value: '', label: 'Any language' }, ...(facets?.languages || []).map(f => ({ value: f.name, label: langName(f.name), hint: f.books.toLocaleString() }))]} />}
          {show('publisher') && (facets?.publishers.length || 0) > 0 && <Select ariaLabel="Publisher" value={value.publisher || ''} onChange={v => set('publisher', v)} options={[{ value: '', label: 'Any publisher' }, ...(facets?.publishers || []).map(f => ({ value: f.name, label: f.name, hint: f.books.toLocaleString() }))]} />}
          {show('rating_min') && <Select ariaLabel="My rating" value={value.rating_min || ''} onChange={v => set('rating_min', v)} options={[{ value: '', label: 'Any rating' }, { value: '10', label: '★★★★★' }, { value: '8', label: '★★★★ and up' }, { value: '6', label: '★★★ and up' }, { value: '2', label: 'Rated' }]} />}
          {show('added_since') && <Select ariaLabel="Added" value={value.added_since || ''} onChange={v => set('added_since', v)} options={[{ value: '', label: 'Added any time' }, { value: daysAgo(7), label: 'Added this week' }, { value: daysAgo(30), label: 'Added this month' }, { value: daysAgo(365), label: 'Added this year' }].concat(value.added_since && ![7, 30, 365].map(daysAgo).includes(value.added_since) ? [{ value: value.added_since, label: `Since ${value.added_since}` }] : [])} />}
          {show('favorite') && <button type="button" className={`btn sm ${value.favorite ? 'on' : ''}`} onClick={() => set('favorite', value.favorite ? '' : '1')}><Icon name="heart" size={14} />Favourites</button>}
          {editor && show('missing') && <Select ariaLabel="Missing" value={value.missing || ''} onChange={v => set('missing', v)} options={MISSING} />}
        </div>
      )}
      <div className="filter-row filter-meta">
        {total !== undefined && total >= 0 && <span className="result-count tnum">{total.toLocaleString()} {total === 1 ? 'book' : 'books'}</span>}
        {chips.map(c => <span key={c.k} className="fchip">{c.label}<button type="button" aria-label={`Remove ${c.label}`} onClick={() => set(c.k, '')}><Icon name="x" size={12} /></button></span>)}
        {chips.length > 1 && <button type="button" className="link-btn small" onClick={() => { const n = { ...value }; chips.forEach(c => { n[c.k] = '' }); onChange(n) }}>Clear all</button>}
        <span className="spacer" />
        {show('sort') && <Select ariaLabel="Sort" value={sort} onChange={v => set('sort', v === defaultSort && extraSorts.some(x => x.value === v) ? '' : v)} options={[...extraSorts, ...SORTS]} />}
        {show('sort') && sort !== 'random' && <button type="button" className="btn ghost icon-only sm" title={desc ? 'Descending' : 'Ascending'} aria-label="Sort order" onClick={() => set('order', desc ? '' : 'desc')}><Icon name={desc ? 'arrow-down' : 'arrow-up'} size={15} /></button>}
        {show('sort') && sort === 'random' && <button type="button" className="btn ghost icon-only sm" title="Shuffle again" aria-label="Shuffle again" onClick={() => onChange({ ...value, seed: String(Math.floor(Math.random() * 1e6)) })}><Icon name="shuffle" size={15} /></button>}
        {onView && <span className="view-toggle"><Segmented size="sm" value={view || 'grid'} onChange={onView} label="View" options={[{ value: 'grid', label: <Icon name="grid" size={14} />, title: 'Covers' }, { value: 'list', label: <Icon name="list" size={14} />, title: 'List' }]} /></span>}
        {onSize && view !== 'list' && <MenuButton label="Cover size" className="btn ghost icon-only sm hide-sm" align="right" width={170} items={[{ heading: 'Cover size' }, ...(['s', 'm', 'l'] as const).map(k => ({ label: k === 's' ? 'Small' : k === 'm' ? 'Medium' : 'Large', checked: (size || 'm') === k, onSelect: () => onSize(k) }))]}><Icon name="sliders" size={15} /></MenuButton>}
      </div>
    </div>
  )
}

function daysAgo(n: number): string {
  const d = new Date(Date.now() - n * 864e5)
  return d.toISOString().slice(0, 10)
}

const LANGS: Record<string, string> = { en: 'English', sr: 'Serbian', hr: 'Croatian', bs: 'Bosnian', sh: 'Serbo-Croatian', de: 'German', fr: 'French', it: 'Italian', es: 'Spanish', ru: 'Russian', pt: 'Portuguese', nl: 'Dutch', pl: 'Polish', sl: 'Slovenian', mk: 'Macedonian', ja: 'Japanese', zh: 'Chinese', ko: 'Korean', hu: 'Hungarian', cs: 'Czech', sv: 'Swedish', no: 'Norwegian', da: 'Danish', fi: 'Finnish', tr: 'Turkish', el: 'Greek', ro: 'Romanian', bg: 'Bulgarian', uk: 'Ukrainian', ar: 'Arabic', he: 'Hebrew', la: 'Latin' }
export function langName(code: string): string {
  if (!code) return ''
  if (LANGS[code]) return LANGS[code]
  try { return new Intl.DisplayNames(['en'], { type: 'language' }).of(code) || code } catch { return code }
}
