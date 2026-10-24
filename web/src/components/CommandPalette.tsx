// ⌘K / "/" — search the library (books, authors, series, tags, shelves), or run a command.
import React, { useEffect, useMemo, useRef, useState } from 'react'
import { createPortal } from 'react-dom'
import { useNavigate } from 'react-router-dom'
import { api, V1 } from '../lib/api'
import { useApp } from '../lib/app-context'
import type { SearchResult } from '../lib/types'
import { authorsText, pct, seriesLine } from '../lib/books'
import { Icon } from './icons'
import { Cover, Img } from './cards'
import { useDebounce } from './ui'

export function CommandPalette({ onClose, onShortcuts }: { onClose: () => void; onShortcuts: () => void }) {
  const navigate = useNavigate()
  const { user, settings } = useApp()
  const [q, setQ] = useState('')
  const dq = useDebounce(q, 140)
  const [hi, setHi] = useState(0)
  const [res, setRes] = useState<SearchResult | null>(null)
  const list = useRef<HTMLDivElement>(null)
  useEffect(() => {
    if (!dq.trim()) { setRes(null); return }
    let live = true
    api.get<SearchResult>(`${V1}/search?q=${encodeURIComponent(dq)}`).then(r => { if (live) setRes(r) }).catch(() => {})
    return () => { live = false }
  }, [dq])

  type Row = { key: string; group: string; icon: React.ReactNode; label: React.ReactNode; sub?: React.ReactNode; hint?: string; run: () => void; alt?: () => void }
  const ico = (n: string) => <Icon name={n as never} size={16} />
  const editor = !!(settings?.can_edit || user?.role === 'admin')
  const commands: Row[] = useMemo(() => [
    { key: 'random', group: 'Commands', icon: ico('shuffle'), label: 'Something I haven’t read', run: () => navigate('/library?status=unread&sort=random') },
    { key: 'recent', group: 'Commands', icon: ico('sparkles'), label: 'Recently added', run: () => navigate('/library?sort=added&order=desc') },
    { key: 'home', group: 'Go to', icon: ico('home'), label: 'Home', hint: 'H', run: () => navigate('/') },
    { key: 'books', group: 'Go to', icon: ico('book'), label: 'Books', hint: 'L', run: () => navigate('/library') },
    { key: 'series', group: 'Go to', icon: ico('layers'), label: 'Series', hint: 'S', run: () => navigate('/series') },
    { key: 'authors', group: 'Go to', icon: ico('feather'), label: 'Authors', hint: 'A', run: () => navigate('/authors') },
    { key: 'tags', group: 'Go to', icon: ico('tag'), label: 'Tags', run: () => navigate('/tags') },
    { key: 'reading', group: 'Go to', icon: ico('bookmark'), label: 'Reading now', hint: 'R', run: () => navigate('/reading') },
    { key: 'want', group: 'Go to', icon: ico('bookmark-plus'), label: 'Want to read', run: () => navigate('/reading/want') },
    { key: 'finished', group: 'Go to', icon: ico('check-circle'), label: 'Finished', run: () => navigate('/reading/read') },
    { key: 'shelves', group: 'Go to', icon: ico('library'), label: 'Shelves', hint: 'B', run: () => navigate('/shelves') },
    { key: 'hl', group: 'Go to', icon: ico('note'), label: 'Highlights', hint: 'N', run: () => navigate('/highlights') },
    { key: 'stats', group: 'Go to', icon: ico('bar'), label: 'Stats', hint: 'T', run: () => navigate('/stats') },
    { key: 'devices', group: 'Go to', icon: ico('smartphone'), label: 'Devices', hint: 'D', run: () => navigate('/devices') },
    { key: 'settings', group: 'Go to', icon: ico('settings'), label: 'Settings', hint: ',', run: () => navigate('/settings') },
    ...(editor ? [{ key: 'admin', group: 'Go to', icon: ico('wrench'), label: 'Library admin', run: () => navigate('/admin') }] : []),
    { key: 'keys', group: 'Go to', icon: ico('keyboard'), label: 'Keyboard shortcuts', hint: '?', run: onShortcuts },
  ], [navigate, onShortcuts, editor])

  const rows = useMemo<Row[]>(() => {
    const n = q.trim().toLowerCase()
    const words = n.split(/\s+/).filter(Boolean)
    const hit = (text: string) => words.every(w => text.toLowerCase().includes(w))
    const cmds = commands.filter(c => !n || hit(`${c.label} ${c.group}`))
    if (!n) return cmds
    const out: Row[] = []
    if (res) {
      out.push(...res.books.map<Row>(b => ({
        key: 'b' + b.id, group: 'Books', icon: <Cover b={b} w={48} />, label: b.title,
        sub: [authorsText(b, 2), seriesLine(b), b.state?.percentage ? `${pct(b.state.percentage)}%` : ''].filter(Boolean).join(' · '), hint: '↵',
        run: () => navigate(`/book/${b.id}`), alt: () => navigate(`/read/${b.id}`),
      })))
      out.push(...res.series.map<Row>(s => ({ key: 's' + s.id, group: 'Series', icon: ico('layers'), label: s.name, sub: `${s.books} ${s.books === 1 ? 'book' : 'books'}`, run: () => navigate(`/series/${s.id}`) })))
      out.push(...res.authors.map<Row>(a => ({ key: 'a' + a.id, group: 'Authors', icon: <Img src={a.photo} w={48} alt={a.name} className="pal-art" round />, label: a.name, sub: `${a.books} ${a.books === 1 ? 'book' : 'books'}`, run: () => navigate(`/author/${a.id}`) })))
      out.push(...res.tags.map<Row>(t => ({ key: 't' + t.name, group: 'Tags', icon: ico('tag'), label: t.name, sub: `${t.books} ${t.books === 1 ? 'book' : 'books'}`, run: () => navigate(`/library?tag=${encodeURIComponent(t.name)}`) })))
      out.push(...res.shelves.map<Row>(s => ({ key: 'sh' + s.id, group: 'Shelves', icon: ico(s.kind === 'smart' ? 'sparkles' : 'library'), label: s.name, sub: s.kind === 'smart' ? 'Smart shelf' : `${s.books} books`, run: () => navigate(`/shelf/${s.id}`) })))
      out.push({ key: 'all', group: 'Books', icon: ico('search'), label: <>All books matching “{q.trim()}”</>, run: () => navigate(`/library?q=${encodeURIComponent(q.trim())}`) })
    }
    out.push(...cmds.slice(0, 5))
    return out
  }, [q, res, commands, navigate])
  useEffect(() => setHi(0), [q, res])
  useEffect(() => { list.current?.querySelector(`[data-i="${hi}"]`)?.scrollIntoView({ block: 'nearest' }) }, [hi])

  const run = (r: Row | undefined, alt = false) => { if (!r) return; onClose(); (alt && r.alt ? r.alt : r.run)() }
  let last = ''
  return createPortal(
    <div className="palette-backdrop" onMouseDown={e => { if (e.target === e.currentTarget) onClose() }}>
      <div className="palette-box" role="dialog" aria-label="Search and commands">
        <div className="pal-input">
          <Icon name="search" size={18} />
          <input autoFocus value={q} onChange={e => setQ(e.target.value)} placeholder="Books, authors, series, tags, or a command…" aria-label="Search"
            onKeyDown={e => {
              if (e.key === 'ArrowDown') { e.preventDefault(); setHi(h => Math.min(rows.length - 1, h + 1)) }
              else if (e.key === 'ArrowUp') { e.preventDefault(); setHi(h => Math.max(0, h - 1)) }
              else if (e.key === 'Enter') { e.preventDefault(); run(rows[hi], e.shiftKey) }
              else if (e.key === 'Escape') { e.preventDefault(); e.stopPropagation(); onClose() }
            }} />
          <kbd>esc</kbd>
        </div>
        <div className="pal-list" ref={list} role="listbox">
          {rows.length === 0 && <div className="pal-empty">{q.trim() && !res ? 'Searching…' : 'Nothing matches.'}</div>}
          {rows.map((r, i) => {
            const head = r.group !== last ? <div className="pal-group" key={'g' + r.group}>{r.group}</div> : null
            last = r.group
            return (
              <React.Fragment key={r.key}>
                {head}
                <div data-i={i} role="option" aria-selected={i === hi} className={`pal-row ${i === hi ? 'hi' : ''}`} onMouseEnter={() => setHi(i)} onClick={() => run(r)}>
                  <span className="pal-ico">{r.icon}</span>
                  <span className="grow"><span className="pal-label ellipsis">{r.label}</span>{r.sub && <span className="pal-sub ellipsis">{r.sub}</span>}</span>
                  {r.hint && <kbd>{r.hint}</kbd>}
                  {i === hi && <Icon name="chevron-right" size={14} className="muted" />}
                </div>
              </React.Fragment>
            )
          })}
        </div>
        {q.trim() && <div className="pal-foot muted xsmall"><kbd>↵</kbd> open · <kbd>⇧</kbd><kbd>↵</kbd> start reading a book</div>}
      </div>
    </div>, document.body)
}
