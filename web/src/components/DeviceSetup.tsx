// What to type into an e-reader: the catalogue and sync addresses, the user
// name, the one-time app password and the steps for its kind.
import { CopyField } from './ui'
import { Icon } from './icons'
import type { Device, DeviceSetup as Setup } from '../lib/types'
import '../styles/admin.css'

export const KIND_LABEL: Record<string, string> = {
  kindle: 'Kindle', crosspoint: 'Xteink X4 (CrossPoint)', koreader: 'KOReader', email: 'E-mail device', kobo: 'Kobo',
}
export const KIND_ICON: Record<string, string> = {
  kindle: 'book-open', crosspoint: 'smartphone', koreader: 'book', email: 'mail', kobo: 'book-open',
}

export function DevIcon({ kind, size = 20 }: { kind: string; size?: number }) {
  return <span className={`dev-ico ${kind}`}><Icon name={KIND_ICON[kind] || 'book'} size={size} /></span>
}

export function DeviceSetup({ device, setup, password, username }: { device: Device; setup: Setup | null; password?: string; username?: string }) {
  const signsIn = device.kind === 'crosspoint' || device.kind === 'koreader'
  return (
    <div className="stack loose">
      {password && (
        <div className="stack">
          <div className="callout"><Icon name="key" size={15} /><span>This is the device's own password. Type it into the {KIND_LABEL[device.kind] || 'device'} now: it is shown only once. If you lose it, make a new one from the device's card.</span></div>
          <div className="dev-password" aria-label="App password">{password}</div>
          <CopyField value={password} label="App password" />
        </div>
      )}
      {setup && signsIn && (
        <dl className="dev-kv">
          <div><dt>Catalogue (OPDS)</dt><dd><CopyField value={setup.opds_url} label="OPDS address" /></dd></div>
          <div><dt>Progress sync</dt><dd><CopyField value={setup.kosync_url} label="Sync address" /></dd></div>
          <div><dt>User name</dt><dd><CopyField value={username || setup.username} label="User name" /></dd></div>
          <div><dt>Password</dt><dd className="muted small">{password ? 'the app password above' : setup.has_password ? 'the app password you were shown when you added it (or make a new one)' : 'make an app password for it'}</dd></div>
        </dl>
      )}
      {setup?.plugin_url && (
        <div className="row wrap">
          <a className="btn primary" href={setup.plugin_url} download><Icon name="download" size={15} />Download the KOReader plugin</a>
          <span className="muted small">Unzip it and copy the <code>pocketlibrary.koplugin</code> folder into <code>koreader/plugins</code> on your Kindle or Kobo, then restart KOReader.</span>
        </div>
      )}
      {setup && setup.steps.length > 0 && (
        <div className="stack">
          <h4 className="small" style={{ margin: 0 }}>How to set it up</h4>
          <ol className="dev-steps">{setup.steps.map((s, i) => <li key={i}>{s}</li>)}</ol>
        </div>
      )}
      {device.kind === 'kindle' && (
        <p className="muted small">Amazon only takes books from addresses you approved. Open <a href="https://www.amazon.com/myk" target="_blank" rel="noreferrer">Manage Your Content and Devices</a> → Preferences → Personal Document Settings and add the sender address from the steps above.</p>
      )}
    </div>
  )
}
