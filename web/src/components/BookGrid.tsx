// A page of books as covers or rows, with multi-select.
import React, { useCallback, useEffect, useState } from 'react'
import type { Book } from '../lib/types'
import { BookCard, BookRow, SkeletonGrid } from './cards'
import { modalOpen } from './ui'

/** Which books are selected; shift-click selects a range in `order`. */
export function useSelection(order: Book[]) {
  const [sel, setSel] = useState<Set<number>>(new Set())
  const [anchor, setAnchor] = useState<number | null>(null)
  const toggle = useCallback((b: Book, e?: React.MouseEvent | React.PointerEvent) => {
    setSel(prev => {
      const n = new Set(prev)
      if (e && 'shiftKey' in e && e.shiftKey && anchor !== null) {
        const a = order.findIndex(x => x.id === anchor), z = order.findIndex(x => x.id === b.id)
        if (a >= 0 && z >= 0) { for (let i = Math.min(a, z); i <= Math.max(a, z); i++) n.add(order[i].id); return n }
      }
      if (n.has(b.id)) n.delete(b.id); else n.add(b.id)
      return n
    })
    setAnchor(b.id)
  }, [anchor, order])
  const clear = useCallback(() => { setSel(new Set()); setAnchor(null) }, [])
  const all = useCallback(() => setSel(new Set(order.map(b => b.id))), [order])
  // esc stops selecting, ⌘A selects everything shown
  useEffect(() => {
    const h = (e: KeyboardEvent) => {
      const t = e.target as HTMLElement
      if (t.closest('input, textarea, [contenteditable="true"]') || modalOpen()) return
      if (e.key === 'Escape' && sel.size) { e.preventDefault(); clear() }
      if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === 'a' && order.length) { e.preventDefault(); all() }
    }
    window.addEventListener('keydown', h)
    return () => window.removeEventListener('keydown', h)
  }, [sel.size, clear, all, order.length])
  return { sel, toggle, clear, all, selecting: sel.size > 0 }
}

export function BookGrid({ books, view = 'grid', size = 'm', loading, sel, onToggle, showSeries, numbered, rowActions, sub }: {
  books: Book[]; view?: 'grid' | 'list'; size?: 's' | 'm' | 'l'; loading?: boolean
  sel?: Set<number>; onToggle?: (b: Book, e: React.MouseEvent | React.PointerEvent) => void
  showSeries?: boolean; numbered?: boolean; rowActions?: (b: Book) => React.ReactNode; sub?: (b: Book) => React.ReactNode
}) {
  const selecting = !!sel && sel.size > 0
  if (loading && books.length === 0) return <SkeletonGrid />
  if (view === 'list') {
    return (
      <ul className={`book-list ${selecting ? 'selecting' : ''}`}>
        {books.map((b, i) => <BookRow key={b.id} b={b} n={numbered ? i + 1 : undefined} selecting={selecting} selected={sel?.has(b.id)} onSelect={onToggle} actions={rowActions?.(b)} />)}
      </ul>
    )
  }
  return (
    <div className={`grid-books size-${size} ${selecting ? 'selecting' : ''}`}>
      {books.map(b => <BookCard key={b.id} b={b} selecting={selecting} selected={sel?.has(b.id)} onSelect={onToggle} showSeries={showSeries} sub={sub?.(b)} />)}
    </div>
  )
}
