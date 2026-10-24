import { useCallback, useEffect, useRef, useState } from 'react'
import { api } from './api'

/** GET a URL (null = nothing yet) and keep the answer; reload() asks again. */
export function useApi<T>(url: string | null, deps: unknown[] = []) {
  const [data, setData] = useState<T | null>(null)
  const [error, setError] = useState('')
  const [loading, setLoading] = useState(true)
  const live = useRef(0)
  const reload = useCallback(() => {
    if (!url) { setLoading(false); return Promise.resolve() }
    const n = ++live.current
    setLoading(true)
    return api.get<T>(url).then(d => { if (n === live.current) { setData(d); setError('') } })
      .catch(e => { if (n === live.current) setError((e as Error).message) })
      .finally(() => { if (n === live.current) setLoading(false) })
  }, [url, ...deps]) // eslint-disable-line react-hooks/exhaustive-deps
  useEffect(() => { reload() }, [reload])
  return { data, setData, error, loading, reload }
}

/** Pages of a list, the next page loaded when the end comes into view. */
export function usePaged<T>(url: (offset: number) => string | null, key: string, pick?: (r: unknown) => { items: T[]; total: number }, limit = 60) {
  const [items, setItems] = useState<T[]>([])
  const [total, setTotal] = useState(-1)
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  const gen = useRef(0)
  const offset = useRef(0)
  const urlRef = useRef(url)
  urlRef.current = url
  const pickRef = useRef(pick)
  pickRef.current = pick
  const load = useCallback(async (reset: boolean) => {
    const g = reset ? ++gen.current : gen.current
    const off = reset ? 0 : offset.current
    const u = urlRef.current(off)
    if (!u) return
    setBusy(true)
    try {
      const raw = await api.get<unknown>(u)
      const r = pickRef.current ? pickRef.current(raw) : raw as { items: T[]; total: number }
      if (g !== gen.current) return
      offset.current = off + r.items.length
      setItems(prev => reset ? r.items : [...prev, ...r.items])
      setTotal(r.total)
      setError('')
    } catch (e) {
      if (g === gen.current) setError((e as Error).message)
    } finally {
      if (g === gen.current) setBusy(false)
    }
  }, [])
  useEffect(() => { setItems([]); setTotal(-1); offset.current = 0; load(true) }, [key, load])
  const more = items.length < total
  const sentinel = useRef<HTMLDivElement | null>(null)
  useEffect(() => {
    const el = sentinel.current
    if (!el || !more) return
    const io = new IntersectionObserver(es => { if (es.some(e => e.isIntersecting) && !busy) load(false) }, { rootMargin: '600px' })
    io.observe(el)
    return () => io.disconnect()
  }, [more, busy, load, items.length])
  return { items, setItems, total, error, busy, more, sentinel, reload: () => load(true), limit }
}
