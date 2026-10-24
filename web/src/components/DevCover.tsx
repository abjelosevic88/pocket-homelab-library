// A small book cover for lists (stats, sends, duplicates), with a typographic
// fallback coloured from the title when the book has no cover.
import { useState } from 'react'
import '../styles/admin.css'

function hueOf(s: string): number {
  return [...s].reduce((a, c) => (a * 31 + c.charCodeAt(0)) % 360, 11)
}

/** src is a book's cover URL (it already carries ?v=…); w is the width asked of the server. */
export function MiniCover({ src, title, width = 40, w = 120 }: { src?: string; title: string; width?: number; w?: number }) {
  const [broken, setBroken] = useState(false)
  const url = src ? `${src}${src.includes('?') ? '&' : '?'}w=${w}` : ''
  const hue = hueOf(title || '?')
  return (
    <span className="mini-cover" style={{ width, background: url && !broken ? undefined : `linear-gradient(160deg, hsl(${hue} 45% 46%), hsl(${(hue + 30) % 360} 50% 30%))` }}>
      {url && !broken ? <img src={url} alt="" loading="lazy" onError={() => setBroken(true)} /> : width >= 60 && <span className="mini-cover-t">{title}</span>}
    </span>
  )
}
