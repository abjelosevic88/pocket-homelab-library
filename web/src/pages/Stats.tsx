// What you read: time, days, pages, books finished, streaks, and where and
// in what you read them — from the web reader and every synced e-reader.
import { useMemo, useState } from 'react'
import { Link } from 'react-router-dom'
import { api, V1 } from '../lib/api'
import { useApp } from '../lib/app-context'
import type { ReadingSession, Stats as StatsT } from '../lib/types'
import { fmtDate, fmtDateTime, fmtPct, fmtSeconds } from '../lib/format'
import { Card, Empty, Loading, PageHeader, Segmented, useAsync } from '../components/ui'
import { Select } from '../components/Select'
import { BarList, Columns } from '../components/charts'
import { Icon } from '../components/icons'
import { MiniCover } from '../components/DevCover'
import '../styles/admin.css'

const hrs = (s: number) => `${+(s / 3600).toFixed(s >= 36000 ? 0 : 1)} h`
const MONTHS = ['Jan', 'Feb', 'Mar', 'Apr', 'May', 'Jun', 'Jul', 'Aug', 'Sep', 'Oct', 'Nov', 'Dec']

export default function Stats() {
  const { settings } = useApp()
  const year = new Date().getFullYear()
  const [range, setRange] = useState('30d')
  const { data: s, error, loading } = useAsync(() => api.get<StatsT>(`${V1}/stats?range=${range}`), [range])
  const { data: sessions } = useAsync(() => api.get<ReadingSession[]>(`${V1}/sessions?limit=20${s?.from ? `&from=${s.from}&to=${s.to}` : ''}`), [s?.from, s?.to])
  const past = useMemo(() => Array.from({ length: 6 }, (_, i) => String(year - 1 - i)), [year])
  const goal = settings?.yearly_goal || 0
  const showGoal = goal > 0 && (range === 'year' || range === String(year))

  const perDay = useMemo(() => (s?.per_day || []).map(p => ({ key: p.day, label: p.day.slice(5).replace('-', '/'), value: p.seconds / 3600, detail: `${fmtDate(p.day)} · ${fmtSeconds(p.seconds)}${p.pages ? ` · ${p.pages} pages` : ''}` })), [s])
  const perMonth = useMemo(() => (s?.per_month || []).map(p => {
    const [y, m] = p.month.split('-')
    return { key: p.month, label: `${MONTHS[Number(m) - 1] || m}${(s?.per_month.length || 0) > 12 ? ` ${y.slice(2)}` : ''}`, value: p.finished, detail: `${p.finished} finished · ${fmtSeconds(p.seconds)}` }
  }), [s])

  return (
    <div className="stats">
      <PageHeader title="Stats" sub="What you read here and on your synced e-readers."
        actions={<div className="st-range">
          <Segmented size="sm" value={past.includes(range) ? null : range} onChange={setRange} options={[{ value: '7d', label: 'Week' }, { value: '30d', label: 'Month' }, { value: '90d', label: '3 months' }, { value: 'year', label: String(year) }, { value: 'all', label: 'All time' }]} />
          <div style={{ width: 120 }}><Select ariaLabel="Earlier years" placeholder="Earlier…" value={past.includes(range) ? range : ''} onChange={v => v && setRange(v)} options={past.map(y => ({ value: y, label: y }))} /></div>
        </div>} />
      {error && <div className="callout danger"><Icon name="alert" size={15} /><span>{error}</span></div>}
      {!s ? (!error && <Loading />) : s.sessions === 0 && s.finished === 0 ? (
        <Empty title="Nothing read in this period" icon="bar">Open a book in the reader, or sync an e-reader, and your reading shows up here.</Empty>
      ) : <div style={{ opacity: loading ? 0.6 : 1, transition: 'opacity .2s' }}>
        <div className="stat-tiles">
          <div className="stat-tile"><span className="stat-label">Time read</span><span className="stat-value">{fmtSeconds(s.seconds)}</span><span className="stat-delta">{s.sessions} {s.sessions === 1 ? 'session' : 'sessions'}</span></div>
          <div className="stat-tile"><span className="stat-label">Days read</span><span className="stat-value">{s.days_read}</span><span className="stat-delta">{s.days_read ? `${fmtSeconds(Math.round(s.seconds / s.days_read))} a day` : '—'}</span></div>
          <div className="stat-tile"><span className="stat-label">Pages</span><span className="stat-value">{s.pages.toLocaleString()}</span><span className="stat-delta">{s.pace_pages_per_hour ? `${Math.round(s.pace_pages_per_hour)} pages an hour` : 'about'}</span></div>
          <div className="stat-tile"><span className="stat-label">Finished</span><span className="stat-value">{s.finished}{showGoal ? <span className="muted" style={{ fontSize: 15, fontWeight: 550 }}> / {goal}</span> : null}</span>
            {showGoal ? <><span className="stat-delta">{fmtPct(Math.min(1, s.finished / goal))} of this year's goal</span><div className="st-goal"><span style={{ width: fmtPct(Math.min(1, s.finished / goal)) }} /></div></>
              : <span className="stat-delta">{s.books_started} started</span>}
          </div>
          <div className="stat-tile"><span className="stat-label">Streak</span><span className="stat-value">{s.streak} {s.streak === 1 ? 'day' : 'days'}</span><span className="stat-delta">longest {s.best_streak}{settings?.daily_goal ? ` · goal ${settings.daily_goal} min a day` : ''}</span></div>
        </div>

        {perDay.length > 0 && <Card title="Time read" className="chart-card">
          <Columns ariaLabel="Hours read per day" data={perDay} format={n => `${+n.toFixed(1)} h`} labelEvery={perDay.length > 16 ? Math.ceil(perDay.length / 12) : 1} />
        </Card>}
        {perMonth.length > 1 && <Card title="Books finished per month" className="chart-card">
          <Columns ariaLabel="Books finished per month" height={150} data={perMonth} format={n => String(Math.round(n))} />
        </Card>}

        {s.finished_books.length > 0 && (
          <Card title={range === 'all' ? 'Finished' : 'Finished in this period'} className="chart-card">
            <div className="st-finished">
              {s.finished_books.map(b => (
                <Link key={b.id} to={`/book/${b.id}`} title={b.title}>
                  <MiniCover src={b.cover} title={b.title} width={96} w={200} />
                  <span className="ellipsis small" style={{ fontWeight: 600 }}>{b.title}</span>
                  {b.state?.finished_at && <span className="muted xsmall ellipsis">{fmtDate(b.state.finished_at.slice(0, 10))}</span>}
                </Link>
              ))}
            </div>
          </Card>
        )}

        <div className="two-col">
          <Card title="Authors">{s.top_authors.length ? <BarList data={s.top_authors.map(a => ({ key: String(a.id), label: a.name, value: a.books, detail: `${a.books} ${a.books === 1 ? 'book' : 'books'}` }))} format={n => String(Math.round(n))} /> : <p className="muted small">No authors yet.</p>}</Card>
          <Card title="Tags">{s.top_tags.length ? <BarList data={s.top_tags.map(t => ({ key: t.name, label: t.name, value: t.books, detail: `${t.books} ${t.books === 1 ? 'book' : 'books'}` }))} format={n => String(Math.round(n))} /> : <p className="muted small">The books you read have no tags.</p>}</Card>
        </div>
        <div className="two-col mt">
          <Card title="Where you read">{s.by_device.length ? <BarList data={s.by_device.map(d => ({ key: d.device, label: d.device || 'Unknown', value: d.seconds / 3600, detail: fmtSeconds(d.seconds) }))} format={hrs} /> : <p className="muted small">—</p>}</Card>
          <Card title="Formats">{s.by_format.length ? <BarList data={s.by_format.map(d => ({ key: d.format, label: d.format.toUpperCase() || '—', value: d.seconds / 3600, detail: fmtSeconds(d.seconds) }))} format={hrs} /> : <p className="muted small">—</p>}</Card>
        </div>

        {sessions && sessions.length > 0 && (
          <Card title="Recent reading" className="mt" sub="A ~ marks time guessed from pages turned on an e-reader that only sends its place.">
            <div className="st-sessions">
              {sessions.map(x => (
                <div key={x.id} className="st-session">
                  <Link to={`/book/${x.book_id}`}><MiniCover src={x.book_cover} title={x.book_title || ''} width={30} /></Link>
                  <div className="grow">
                    <Link to={`/book/${x.book_id}`} className="ellipsis" style={{ display: 'block', fontWeight: 600, color: 'var(--text)' }}>{x.book_title || 'A book'}</Link>
                    <div className="muted small ellipsis">{fmtDateTime(x.started_at)} · {x.device || x.source}{x.pages ? ` · ${x.pages} pages` : ''} · {fmtPct(x.start_pct)} → {fmtPct(x.end_pct)}</div>
                  </div>
                  <span className="nowrap small">{x.estimated && <span className="st-est" title="Estimated">~</span>}{fmtSeconds(x.seconds)}</span>
                </div>
              ))}
            </div>
          </Card>
        )}
      </div>}
    </div>
  )
}
