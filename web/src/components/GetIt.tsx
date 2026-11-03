import { useState } from 'react'
import { api, ApiError, V1 } from '../lib/api'
import { useApi } from '../lib/use'
import { Img } from './cards'
import { Icon } from './icons'
import { MenuButton, Spinner, type MenuItem, useToast } from './ui'

// "Get it": ask Pocket Requests (through Pocket Core) for a book's audiobook,
// the book in another language the box reads, or the author's other books —
// and see whether they are here or on their way. Without the link the
// buttons stay, greyed out, with the reason.

type Kind = 'ebook' | 'audiobook'
interface Ask { id: number; kind: Kind; language: string; status: string; state: string; progress: number; url?: string }
interface Have { url: string }
interface CanGet { ok: boolean; reason?: string }
export interface GetItInfo {
  available: boolean; reason?: string; found: boolean; book_key?: string; book_url?: string; app_url?: string; can_get?: CanGet
  audiobook: { have: Have | null; request: Ask | null; unknown?: string }
  languages: { code: string; name: string; request: Ask | null }[]
  language: string
}
interface MoreBook { book_key: string; title: string; year: number; cover: string; url: string; ebook: Have | null; audiobook: Have | null; asked: Ask[]; in_library: boolean; local_id?: number }
interface AuthorMore { available: boolean; reason?: string; app_url?: string; can_get?: CanGet; items: MoreBook[] }

const STATE_LABEL: Record<string, string> = {
  waiting: 'Waiting for approval', sent: 'Asked for', unreleased: 'Not out yet', searching: 'Looking for a copy',
  downloading: 'Downloading', importing: 'Arriving', attention: 'Needs a look', partial: 'Partly here',
  available: 'Here', removed: 'Removed', failed: 'Couldn’t be asked for',
}

function stateText(r: Ask): string {
  if (r.status === 'pending') return 'Waiting for approval'
  let s = STATE_LABEL[r.state] || r.state
  if (r.state === 'downloading' && r.progress > 0 && r.progress < 1) s += ` · ${Math.round(r.progress * 100)}%`
  return s
}

type Said = { ok: boolean; msg: string; url?: string }

async function post(url: string, body: Record<string, unknown>, what: string): Promise<Said> {
  try {
    const r = await api.post<Ask>(url, body)
    return { ok: true, msg: `${what} asked for in Pocket Requests`, url: r.url }
  } catch (e) {
    const d = e instanceof ApiError ? e.data : {}
    return { ok: false, msg: (e as Error).message, url: ((d.url || d.open_url) as string) || undefined }
  }
}

const openAction = (url?: string) => url ? { label: 'Open', run: () => { window.open(url, '_blank', 'noopener') } } : undefined

function AskState({ a }: { a: Ask }) {
  return a.url
    ? <a className="get-it-state" href={a.url} target="_blank" rel="noreferrer">{stateText(a)}<Icon name="external" size={11} /></a>
    : <span className="get-it-state">{stateText(a)}</span>
}

/** The "Get it" card on a book's page: its audiobook and the other languages. */
export function GetItCard({ bookId }: { bookId: number }) {
  const { toast } = useToast()
  const { data, loading, reload, setData } = useApi<GetItInfo>(`${V1}/books/${bookId}/requests`, [bookId])
  const [busy, setBusy] = useState('')
  const go = async (kind: Kind, language = '', label = '') => {
    setBusy(kind + language)
    const r = await post(`${V1}/books/${bookId}/requests`, { kind, language }, label || (kind === 'audiobook' ? 'The audiobook' : 'The ebook'))
    setBusy('')
    toast(r.msg, r.ok ? 'ok' : 'err', openAction(r.url))
    try { setData(await api.get<GetItInfo>(`${V1}/books/${bookId}/requests?fresh=1`)) } catch { reload() }
  }
  const off = !!data && !data.available
  const cannot = !!data?.can_get && !data.can_get.ok
  const disabled = !data || off || cannot || busy !== ''
  const why = off ? data?.reason : cannot ? data?.can_get?.reason : undefined
  const ab = data?.audiobook
  return (
    <section className="card side-card get-it" aria-busy={loading}>
      <h3><Icon name="download" size={14} />Get it
        {loading && !data && <Spinner size={12} />}
        {data?.book_url && <a className="muted small icon-text get-it-open" href={data.book_url} target="_blank" rel="noreferrer">Open in Requests<Icon name="external" size={11} /></a>}
      </h3>
      <div className="get-it-row">
        <span className="get-it-kind"><Icon name="headphones" size={14} />Audiobook</span>
        {ab?.have ? <a className="btn sm" href={ab.have.url} target="_blank" rel="noreferrer"><Icon name="check" size={13} />Here · listen</a>
          : ab?.request ? <AskState a={ab.request} />
          : <button type="button" className="btn sm" disabled={disabled} title={why || 'Ask Pocket Requests for the audiobook'} onClick={() => go('audiobook')}>
              {busy === 'audiobook' ? <Spinner size={12} /> : <Icon name="download" size={13} />}Get the audiobook
            </button>}
      </div>
      {(data?.languages || []).map(l => (
        <div key={l.code} className="get-it-row">
          <span className="get-it-kind"><Icon name="languages" size={14} />{l.name}</span>
          {l.request ? <AskState a={l.request} />
            : <button type="button" className="btn sm" disabled={disabled} title={why || `Ask Pocket Requests for the ebook in ${l.name}`} onClick={() => go('ebook', l.code, `The ${l.name} ebook`)}>
                {busy === 'ebook' + l.code ? <Spinner size={12} /> : <Icon name="download" size={13} />}Get it in {l.name}
              </button>}
        </div>
      ))}
      {why && <p className="core-hint small muted">{why}</p>}
      {!why && ab?.unknown && <p className="small muted get-it-note">{ab.unknown}</p>}
    </section>
  )
}

/** "More by the author": their other books from Pocket Requests, with Get buttons. */
export function AuthorMoreSection({ authorId, name }: { authorId: number; name: string }) {
  const { toast } = useToast()
  const { data, loading, reload, setData } = useApi<AuthorMore>(`${V1}/authors/${authorId}/requests-more`, [authorId])
  const others = (data?.items || []).filter(b => !b.in_library)
  if (loading && !data) return <section className="section"><h2 className="section-h">More by {name}</h2><Spinner /></section>
  if (!data) return null
  const off = !data.available
  const cannot = !!data.can_get && !data.can_get.ok
  const why = off ? data.reason : cannot ? data.can_get?.reason : undefined
  if (!off && others.length === 0) return null
  const go = async (b: MoreBook, kind: Kind) => {
    const r = await post(`${V1}/authors/${authorId}/requests`, { book_key: b.book_key, title: b.title, kind }, `${b.title} (${kind})`)
    toast(r.msg, r.ok ? 'ok' : 'err', openAction(r.url))
    try { setData(await api.get<AuthorMore>(`${V1}/authors/${authorId}/requests-more?fresh=1`)) } catch { reload() }
  }
  return (
    <section className="section author-more">
      <h2 className="section-h">More by {name}{data.app_url && <a className="muted small icon-text" href={data.app_url} target="_blank" rel="noreferrer" style={{ marginLeft: 10, fontWeight: 400 }}>Pocket Requests<Icon name="external" size={11} /></a>}</h2>
      {why && <p className="core-hint small muted">{why}</p>}
      {others.length > 0 && <div className="grid-books">
        {others.map(b => {
          const asked = b.asked.filter(a => a.status !== 'declined' && a.status !== 'cancelled')
          const badge = b.ebook ? 'Ebook here' : b.audiobook ? 'Audiobook here' : asked.length ? stateText(asked[0]) : ''
          const items: MenuItem[] = [{ heading: 'Ask Pocket Requests for' },
            { label: 'The ebook', icon: 'book', disabled: !!b.ebook || asked.some(a => a.kind === 'ebook'), onSelect: () => go(b, 'ebook') },
            { label: 'The audiobook', icon: 'headphones', disabled: !!b.audiobook || asked.some(a => a.kind === 'audiobook'), onSelect: () => go(b, 'audiobook') }]
          return (
            <div key={b.book_key} className="tile book-tile more-tile">
              <a href={b.url} target="_blank" rel="noreferrer" className="tile-art book" title={`${b.title} in Pocket Requests`}>
                <Img src={b.cover} w={220} alt={b.title} />
                {badge && <span className="more-badge">{badge}</span>}
              </a>
              <span className="tile-title ellipsis2">{b.title}</span>
              <span className="tile-sub row" style={{ justifyContent: 'space-between', alignItems: 'center' }}>
                <span className="ellipsis">{b.year || '—'}</span>
                {off || cannot
                  ? <button type="button" className="btn ghost sm icon-only" disabled title={why}><Icon name="download" size={14} /></button>
                  : <MenuButton label={`Get ${b.title}`} className="btn ghost sm icon-only" align="right" items={items}><Icon name="download" size={14} /></MenuButton>}
              </span>
            </div>
          )
        })}
      </div>}
    </section>
  )
}

