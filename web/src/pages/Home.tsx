// Home: what you're reading, what's next in your series, what you want to
// read, what's new, something to discover — and how your reading goes.
import { useState } from 'react'
import { Link } from 'react-router-dom'
import { api, V1 } from '../lib/api'
import { useApp } from '../lib/app-context'
import { useApi } from '../lib/use'
import type { Book, Home as HomeT } from '../lib/types'
import { authorsText, coverURL, hm, hue, pct, seriesLine, stateOf } from '../lib/books'
import { Icon } from '../components/icons'
import { BookCard, ContinueCard, Cover, Progress, Rail, SkeletonGrid } from '../components/cards'
import { KIND_ICON } from '../components/Sidebar'

function greeting() {
  const h = new Date().getHours()
  return h < 5 ? 'Good night' : h < 12 ? 'Good morning' : h < 18 ? 'Good afternoon' : 'Good evening'
}

export default function Home() {
  const { user, settings } = useApp()
  const { data: h, setData, error } = useApi<HomeT>(`${V1}/home`)
  const [shuffling, setShuffling] = useState(false)
  if (error) return <div className="callout danger"><Icon name="alert" size={15} />{error}</div>
  if (!h) return <div className="home"><div className="home-hero"><h1>{greeting()}</h1></div><SkeletonGrid n={8} /></div>
  const first = (user?.name || '').split(' ')[0]
  const feature = h.continue[0]
  const editor = !!(settings?.can_edit || user?.role === 'admin')
  const totalBooks = h.libraries.reduce((a, l) => a + (l.books || 0), 0)
  const st = h.stats
  const reshuffle = async () => {
    setShuffling(true)
    try { const r = await api.get<HomeT>(`${V1}/home`); setData({ ...h, discover: r.discover }) } catch { /* keep */ } finally { setShuffling(false) }
  }

  if (h.libraries.length === 0 || totalBooks === 0) {
    return (
      <div className="home">
        <header className="home-hero"><div className="grow"><h1>{greeting()}{first ? `, ${first}` : ''}</h1></div></header>
        <div className="empty big">
          <span className="empty-ico"><Icon name="library" size={22} /></span>
          <h3>{h.libraries.length === 0 ? 'No library yet' : 'Your library is empty'}</h3>
          <p className="muted" style={{ maxWidth: 480 }}>
            {h.libraries.length === 0
              ? 'Pocket Library reads the folders where your ebooks, comics and manga live. Add a library with those folders and it files every book: covers, authors, series and the formats you have.'
              : 'The library folders have no books yet, or the first scan is still running. New books show up here as soon as they are found.'}
          </p>
          {editor ? <Link to="/admin" className="btn primary"><Icon name="plus" size={15} />{h.libraries.length === 0 ? 'Add a library' : 'Library admin'}</Link>
            : <p className="muted small">Ask whoever runs this box to add a library.</p>}
        </div>
      </div>
    )
  }

  return (
    <div className="home">
      <header className="home-hero">
        <div className="grow">
          <h1>{greeting()}{first ? `, ${first}` : ''}</h1>
          <p className="muted">
            {feature ? <>Pick up “{feature.title}” where you left off</> : <>{totalBooks.toLocaleString()} books on your shelves. Pick something new.</>}
            {st.streak > 1 && <> · <span className="streak"><Icon name="zap" size={13} />{st.streak}-day streak</span></>}
          </p>
        </div>
        <Link to="/library?status=unread&sort=random" className="btn"><Icon name="shuffle" size={15} />Surprise me</Link>
      </header>

      <div className="home-stats">
        <Stat icon="book-open" value={String(st.reading)} label={st.reading === 1 ? 'book on the go' : 'books on the go'} to="/reading" />
        <Stat icon="check-circle" value={st.yearly_goal ? `${st.finished_year} / ${st.yearly_goal}` : String(st.finished_year)} label={`read in ${new Date().getFullYear()}`} goal={st.yearly_goal ? st.finished_year / st.yearly_goal : undefined} to="/reading/read" />
        <Stat icon="clock" value={st.minutes_today ? hm(st.minutes_today) : '0 min'} label={st.daily_goal ? `today of ${st.daily_goal} min` : 'read today'} goal={st.daily_goal ? st.minutes_today / st.daily_goal : undefined} to="/stats" />
        {h.pending_sends > 0 && <Stat icon="share" value={String(h.pending_sends)} label="on the way to a device" to="/devices" />}
      </div>

      {feature && <Feature b={feature} />}
      {h.continue.length > 1 && <Rail title="Continue reading" to="/reading" wide>{h.continue.slice(1).map(b => <ContinueCard key={b.id} b={b} />)}</Rail>}
      {h.on_deck.length > 0 && <Rail title="Next in your series" sub="The next book of series you are reading">{h.on_deck.map(b => <BookCard key={b.id} b={b} sub={seriesLine(b) || authorsText(b)} />)}</Rail>}
      {h.want.length > 0 && <Rail title="Want to read" to="/reading/want">{h.want.map(b => <BookCard key={b.id} b={b} />)}</Rail>}
      {h.recently_added.length > 0 && <Rail title="Recently added" to="/library?sort=added&order=desc">{h.recently_added.map(b => <BookCard key={b.id} b={b} />)}</Rail>}
      {h.discover.length > 0 && (
        <Rail title="Discover" sub="From your shelves, not started yet" to="/library?status=unread&sort=random"
          actions={<button type="button" className="btn ghost sm" onClick={reshuffle} disabled={shuffling}><Icon name="shuffle" size={14} />Shuffle</button>}>
          {h.discover.map(b => <BookCard key={b.id} b={b} />)}
        </Rail>
      )}
      {h.libraries.length > 1 && (
        <section className="shelf">
          <div className="shelf-head"><h2 className="grow">Libraries</h2></div>
          <div className="lib-tiles">
            {h.libraries.map(l => (
              <Link key={l.id} to={`/library?library=${l.id}`} className="lib-tile" style={{ '--h': hue(l.name) } as React.CSSProperties}>
                <span className="ico"><Icon name={(KIND_ICON[l.kind] || 'library') as never} size={18} /></span>
                <span className="grow"><b className="ellipsis">{l.name}</b><span>{(l.books || 0).toLocaleString()} {l.kind === 'books' ? 'books' : l.kind === 'manga' ? 'chapters & volumes' : 'issues'}</span></span>
              </Link>
            ))}
          </div>
        </section>
      )}
    </div>
  )
}

function Stat({ icon, value, label, goal, to }: { icon: string; value: string; label: string; goal?: number; to: string }) {
  return (
    <Link to={to} className="home-stat" style={{ textDecoration: 'none', color: 'inherit' }}>
      <span className="ico"><Icon name={icon as never} size={16} /></span>
      <span className="grow"><b>{value}</b><span>{label}</span>{goal !== undefined && <span className="goal-bar"><span style={{ width: `${Math.min(100, goal * 100)}%` }} /></span>}</span>
    </Link>
  )
}

/** The big card: the book you were last in. */
function Feature({ b }: { b: Book }) {
  const s = stateOf(b)
  return (
    <section className="feature book-feature" style={b.cover ? { backgroundImage: `url(${coverURL(b.cover, 600)})` } : { backgroundColor: b.cover_color || undefined }}>
      <div className="feature-shade" />
      <div className="feature-body">
        <Link to={`/book/${b.id}`} className="feature-cover book"><Cover b={b} w={300} /></Link>
        <div className="feature-text">
          <p className="eyebrow">Pick up where you left off{s.last_device ? ` · last on ${s.last_device}` : ''}</p>
          <h2 className="feature-title"><Link to={`/book/${b.id}`}>{b.title}</Link></h2>
          <p className="feature-meta">{authorsText(b)}{b.series ? ` · ${seriesLine(b)}` : ''}</p>
          <div className="row wrap">
            <Link to={`/read/${b.id}`} className="btn primary lg"><Icon name="book-open" size={16} />Continue · {pct(s.percentage)}%</Link>
            <Link to={`/book/${b.id}`} className="btn glass lg"><Icon name="info" size={16} />Details</Link>
          </div>
          <span className="feature-progress"><Progress value={s.percentage || 0.001} /><span className="tnum">{pct(s.percentage)}%</span></span>
        </div>
      </div>
    </section>
  )
}
