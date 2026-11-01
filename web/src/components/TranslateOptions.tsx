// Translate into Serbian: the choices (script, model, notes), Pocket AI's
// state, and the dialog a book page opens.
import { useEffect, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { api, V1 } from '../lib/api'
import type { AIModels, Book, TranslateStatus, Translation } from '../lib/types'
import { Field, Modal, errMsg, useToast } from './ui'
import { Select } from './Select'
import { Icon } from './icons'

export type TranslateChoice = { script: 'latin' | 'cyrillic'; model: string; notes: string }

const KEY = 'plb-translate'
export function savedChoice(): TranslateChoice {
  try { const v = JSON.parse(localStorage.getItem(KEY) || '{}'); return { script: v.script === 'cyrillic' ? 'cyrillic' : 'latin', model: v.model || '', notes: '' } }
  catch { return { script: 'latin', model: '', notes: '' } }
}
export function saveChoice(c: TranslateChoice) { localStorage.setItem(KEY, JSON.stringify({ script: c.script, model: c.model })) }

/** Pocket AI's state and models, loaded once per mount. */
export function usePocketAI() {
  const [status, setStatus] = useState<TranslateStatus | null>(null)
  const [models, setModels] = useState<AIModels | null>(null)
  useEffect(() => {
    let live = true
    api.get<TranslateStatus>(`${V1}/translate/status`).then(s => {
      if (!live) return
      setStatus(s)
      if (s.ready) api.get<AIModels>(`${V1}/translate/models`).then(m => { if (live) setModels(m) }).catch(() => {})
    }).catch(e => { if (live) setStatus({ ready: false, detail: errMsg(e), core: false, convert: false, parallel: 0 }) })
    return () => { live = false }
  }, [])
  return { status, models }
}

export function AIState({ status, models }: { status: TranslateStatus | null; models: AIModels | null }) {
  if (!status) return <div className="callout"><span className="spinner" />Checking Pocket AI…</div>
  if (!status.ready) return (
    <div className="callout warn"><Icon name="alert" size={15} />
      <span><b>Pocket AI isn't connected.</b> {status.detail}. In Pocket Core, open Pocket Library's links and switch on <i>Pocket AI · Generate text</i>.</span>
    </div>
  )
  return (
    <div className="callout ok"><Icon name="sparkles" size={15} />
      <span>Pocket AI is ready{models ? <> · default model <b>{models.default_name || models.default_model}</b></> : ''} · {status.parallel} parts at a time. Finished books are filed into the library.</span>
    </div>
  )
}

export function TranslateFields({ value, onChange, models }: { value: TranslateChoice; onChange: (v: TranslateChoice) => void; models: AIModels | null }) {
  const modelOptions = [{ value: '', label: `Pocket AI default${models ? ` (${models.default_name || models.default_model})` : ''}` },
    ...(models?.models || []).map(m => ({ value: m.id, label: m.name, hint: m.description }))]
  return (
    <div className="tr-fields">
      <Field label="Script" group>
        <Select ariaLabel="Script" value={value.script} onChange={v => onChange({ ...value, script: v as TranslateChoice['script'] })}
          options={[{ value: 'latin', label: 'Latinica (Latin)' }, { value: 'cyrillic', label: 'Ćirilica (Cyrillic)' }]} />
      </Field>
      <Field label="Model" group>
        <Select ariaLabel="Model" value={value.model} onChange={v => onChange({ ...value, model: v })} options={modelOptions} />
      </Field>
      <Field label="Notes for the translator" className="full" help={'Optional, e.g. "keep character names in English", "formal register", glossary terms.'}>
        <textarea rows={3} value={value.notes} onChange={e => onChange({ ...value, notes: e.target.value })} placeholder="Optional instructions…" />
      </Field>
    </div>
  )
}

/** The book page's "Translate to Serbian" dialog. */
export function TranslateBookDialog({ book, onClose, onStarted }: { book: Book; onClose: () => void; onStarted?: (t: Translation) => void }) {
  const { toast } = useToast()
  const navigate = useNavigate()
  const { status, models } = usePocketAI()
  const [choice, setChoice] = useState<TranslateChoice>(savedChoice)
  const [busy, setBusy] = useState(false)
  const go = async () => {
    setBusy(true)
    try {
      saveChoice(choice)
      const t = await api.post<Translation>(`${V1}/translations`, { book_id: book.id, ...choice })
      onStarted?.(t)
      toast('Translation started — it takes a while, you can leave this page')
      onClose()
    } catch (e) { toast(errMsg(e), 'err') } finally { setBusy(false) }
  }
  return (
    <Modal title="Translate to Serbian" onClose={onClose}
      footer={<><button type="button" className="btn ghost" onClick={() => { onClose(); navigate('/translate') }}>All translations</button><span style={{ flex: 1 }} />
        <button type="button" className="btn" onClick={onClose}>Cancel</button>
        <button type="button" className="btn primary" disabled={busy || !status?.ready} onClick={go}><Icon name="languages" size={15} />Translate</button></>}>
      <div className="stack">
        <p className="muted small"><b>{book.title}</b>{book.authors[0] ? ` by ${book.authors[0].name}` : ''} is translated part by part with Pocket AI; the Serbian book lands in the library next to this one. A novel takes about half an hour.</p>
        <AIState status={status} models={models} />
        <TranslateFields value={choice} onChange={setChoice} models={models} />
      </div>
    </Modal>
  )
}
