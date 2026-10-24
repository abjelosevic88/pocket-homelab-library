import { Modal } from './ui'

const GROUPS: { title: string; keys: [string[], string][] }[] = [
  { title: 'Go to', keys: [[['h'], 'Home'], [['l'], 'Books'], [['s'], 'Series'], [['a'], 'Authors'], [['r'], 'Reading'], [['b'], 'Shelves'], [['n'], 'Highlights'], [['t'], 'Stats'], [['d'], 'Devices'], [[','], 'Settings']] },
  { title: 'Find', keys: [[['/'], 'Search & commands'], [['⌘', 'K'], 'Search & commands'], [['['], 'Show or hide the sidebar'], [['?'], 'This sheet']] },
  { title: 'Books', keys: [[['⇧', 'click'], 'Select several books'], [['esc'], 'Stop selecting'], [['⌘', 'A'], 'Select every book shown']] },
  { title: 'Reading', keys: [[['←', '→'], 'Previous / next page'], [['space'], 'Next page'], [['t'], 'Contents'], [['b'], 'Bookmark this page'], [['f'], 'Full screen'], [['esc'], 'Close the panel, then the book']] },
]

export function ShortcutsSheet({ onClose }: { onClose: () => void }) {
  return (
    <Modal title="Keyboard shortcuts" onClose={onClose} size="lg">
      <div className="shortcuts">
        {GROUPS.map(g => (
          <section key={g.title}>
            <h4 className="sc-h">{g.title}</h4>
            {g.keys.map(([keys, label]) => (
              <div key={label + keys.join('')} className="sc-row"><span>{label}</span><span className="sc-keys">{keys.map(k => <kbd key={k}>{k}</kbd>)}</span></div>
            ))}
          </section>
        ))}
      </div>
      <p className="muted small mt">On a phone, press and hold a cover to start selecting. In search, <kbd>⇧</kbd><kbd>↵</kbd> opens a book in the reader straight away.</p>
    </Modal>
  )
}
