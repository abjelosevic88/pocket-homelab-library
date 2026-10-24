// Send a book (or several) to an e-reader: one tap for the default device, a menu for the others.
import { useEffect, useState } from 'react'
import { Link } from 'react-router-dom'
import { api, V1 } from '../lib/api'
import { useApp } from '../lib/app-context'
import type { Device, Send } from '../lib/types'
import { Icon } from './icons'
import { type Anchor, Popover, Spinner, errMsg, rectOf, useToast } from './ui'

let devicesCache: Promise<Device[]> | null = null
/** The person's devices, fetched once per page load (refresh after changes). */
export function loadDevices(fresh = false): Promise<Device[]> {
  if (!devicesCache || fresh) devicesCache = api.get<Device[]>(`${V1}/devices`).catch(e => { devicesCache = null; throw e })
  return devicesCache
}

const KIND_ICON: Record<string, string> = { kindle: 'mail', email: 'mail', crosspoint: 'smartphone', koreader: 'book-open', kobo: 'smartphone' }
export const deviceIcon = (d: Device) => (KIND_ICON[d.kind] || 'smartphone') as never

function sentText(s: Send, d: Device): string {
  if (s.status === 'failed') return `Couldn’t send to ${d.name}: ${s.error}`
  if (s.method === 'pull') return `Waiting for ${d.name} to fetch it${d.kind === 'koreader' ? ' (Pocket Library plugin or catalogue)' : ' (its catalogue → To send)'}`
  if (s.method === 'push') return `Sending to ${d.name}…`
  return `Sending to ${d.name}${d.kind === 'kindle' ? ' — it arrives in a minute or two' : ''}`
}

/** Send to a device: the book page's button (bookId) or the bulk bar's (bookIds). */
export function SendButton({ bookId, bookIds, fileId, className = 'btn lg', label = 'Send', onSent }: { bookId?: number; bookIds?: number[]; fileId?: number; className?: string; label?: string; onSent?: () => void }) {
  const { settings } = useApp()
  const { toast } = useToast()
  const [devices, setDevices] = useState<Device[] | null>(null)
  const [anchor, setAnchor] = useState<Anchor | null>(null)
  const [busy, setBusy] = useState(false)
  useEffect(() => { loadDevices().then(setDevices).catch(() => setDevices([])) }, [])
  const def = devices?.find(d => d.id === settings?.default_device) || devices?.find(d => d.is_default) || (devices?.length === 1 ? devices[0] : undefined)
  const send = async (d: Device) => {
    setAnchor(null)
    setBusy(true)
    try {
      if (bookIds && bookIds.length) {
        const r = await api.post<Send[] | { sends?: Send[] }>(`${V1}/send`, { book_ids: bookIds, device_id: d.id })
        const list = Array.isArray(r) ? r : r.sends || []
        const failed = list.filter(s => s.status === 'failed').length
        toast(failed ? `${list.length - failed} on the way to ${d.name}, ${failed} failed` : `${bookIds.length} ${bookIds.length === 1 ? 'book' : 'books'} on the way to ${d.name}`, failed ? 'err' : 'ok')
      } else if (bookId) {
        const s = await api.post<Send>(`${V1}/books/${bookId}/send`, { device_id: d.id, file_id: fileId || undefined })
        toast(sentText(s, d), s.status === 'failed' ? 'err' : 'ok')
      }
      onSent?.()
    } catch (e) { toast(errMsg(e), 'err') } finally { setBusy(false) }
  }
  const many = (devices?.length || 0) > 1
  return (
    <>
        <button type="button" className={className} disabled={busy || !devices} title={def ? `Send to ${def.name}` : 'Send to a device'}
          onClick={e => { if (devices?.length === 1) send(devices[0]); else setAnchor(anchor ? null : rectOf(e.currentTarget)) }}>
          {busy ? <Spinner /> : <Icon name="share" size={16} />}{def && !many ? `${label} to ${def.name}` : label}{many && <Icon name="chevron-down" size={14} />}
        </button>
      {anchor && (
        <Popover anchor={anchor} onClose={() => setAnchor(null)} prefer="below" width={290} label="Send to">
          <div className="pop-pad">
            <h4>Send to</h4>
            {devices && devices.length === 0 && <p className="muted small">No devices yet. Add your Kindle, Xteink X4 (CrossPoint) or KOReader device first.</p>}
            <div className="shelf-picker">
              {devices?.map(d => (
                <button key={d.id} type="button" className="menu-item" onClick={() => send(d)}>
                  <span className="menu-ico"><Icon name={deviceIcon(d)} size={15} /></span>
                  <span className="grow"><span className="dev-item"><b>{d.name}</b><span className="muted xsmall">{d.kind === 'kindle' ? 'Send to Kindle by e-mail' : d.kind === 'email' ? d.email : d.kind === 'crosspoint' ? 'Xteink · CrossPoint' : d.kind === 'koreader' ? 'KOReader' : d.kind}</span></span></span>
                  {d.id === def?.id && <span className="badge">default</span>}
                </button>
              ))}
            </div>
            <Link to="/devices" className="small" onClick={() => setAnchor(null)}><Icon name="settings" size={13} /> Manage devices</Link>
          </div>
        </Popover>
      )}
    </>
  )
}
