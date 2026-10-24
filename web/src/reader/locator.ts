// Positions shared with KOReader and CrossPoint (internal/xpointer): a spine
// item and how far into its text a point is. The fraction counts the code
// points of the <body>'s text nodes in document order, every run of
// whitespace counted as one space, before the point, over the total.

export type Locator =
  | { type: 'epub'; cfi: string; spine: number; fraction: number; chapter?: string }
  | { type: 'pdf'; page: number }
  | { type: 'comic'; page: number }
  | { type: 'kosync'; xpointer?: string; spine?: number; fraction?: number; page?: number; device?: string }

export function parseLocator(raw: string | undefined | null): Partial<Locator> & { type?: string; cfi?: string; spine?: number; fraction?: number; page?: number; pos0?: string } {
  if (!raw) return {}
  try { const v = JSON.parse(raw); return v && typeof v === 'object' ? v : {} } catch { return {} }
}

const WS = /\s/

/** Code points of a string with whitespace runs counted once; ws says whether
 *  the text before ended in whitespace (runs continue across text nodes). */
function countCollapsed(s: string, ws: boolean): [number, boolean] {
  let n = 0
  for (const ch of s) {
    if (WS.test(ch)) {
      if (!ws) { n++; ws = true }
    } else { n++; ws = false }
  }
  return [n, ws]
}

function textNodes(doc: Document): Text[] {
  const body = doc.body
  if (!body) return []
  const out: Text[] = []
  const w = doc.createTreeWalker(body, NodeFilter.SHOW_TEXT)
  for (let n = w.nextNode(); n; n = w.nextNode()) out.push(n as Text)
  return out
}

/** The fraction of a section's text before a point (container + offset). */
export function fractionAt(doc: Document, container: Node, offset: number): number {
  const nodes = textNodes(doc)
  if (!nodes.length) return 0
  // a point between elements: the first text node at or after it
  let target: Text | null = null
  let targetOffset = 0
  if (container.nodeType === Node.TEXT_NODE) {
    target = container as Text
    targetOffset = offset
  } else {
    const r = doc.createRange()
    r.setStart(container, Math.min(offset, container.childNodes.length))
    target = nodes.find(t => r.comparePoint(t, 0) >= 0) || null
    targetOffset = 0
  }
  let before = 0, total = 0, ws = false, reached = false
  for (const t of nodes) {
    if (t === target) {
      const head = t.data.slice(0, targetOffset)
      const [a, wsA] = countCollapsed(head, ws)
      before = total + a
      const [b, wsB] = countCollapsed(t.data.slice(targetOffset), wsA)
      total += a + b
      ws = wsB
      reached = true
      continue
    }
    const [n, w2] = countCollapsed(t.data, ws)
    total += n
    ws = w2
  }
  if (!reached) before = target ? 0 : total
  return total ? Math.min(1, before / total) : 0
}

/** A collapsed range at a fraction of a section's text. */
export function rangeAtFraction(doc: Document, fraction: number): Range | null {
  const nodes = textNodes(doc)
  if (!nodes.length) return null
  let total = 0, ws = false
  for (const t of nodes) { const [n, w] = countCollapsed(t.data, ws); total += n; ws = w }
  const want = Math.max(0, Math.min(1, fraction)) * total
  let seen = 0
  ws = false
  for (const t of nodes) {
    const [n, w] = countCollapsed(t.data, ws)
    if (seen + n >= want && n > 0) {
      // walk the node to the code point
      let k = 0, unit = 0, w2 = ws
      for (const ch of t.data) {
        if (seen + k >= want) break
        if (WS.test(ch)) { if (!w2) { k++; w2 = true } } else { k++; w2 = false }
        unit += ch.length
      }
      const r = doc.createRange()
      r.setStart(t, Math.min(unit, t.data.length))
      r.collapse(true)
      return r
    }
    seen += n
    ws = w
  }
  const last = nodes[nodes.length - 1]
  const r = doc.createRange()
  r.setStart(last, last.data.length)
  r.collapse(true)
  return r
}
