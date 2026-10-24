// A vertical list reordered by dragging a handle (mouse, pen or touch).
import React, { useRef, useState } from 'react'
import { Icon } from './icons'

export function DragList<T>({ items, keyOf, render, onReorder, className = 'drag-list' }: { items: T[]; keyOf: (t: T) => number | string; render: (t: T, handle: React.ReactNode, i: number) => React.ReactNode; onReorder: (items: T[]) => void; className?: string }) {
  const [order, setOrder] = useState<T[] | null>(null)
  const [dragKey, setDragKey] = useState<number | string | null>(null)
  const list = useRef<HTMLUListElement>(null)
  const shown = order || items
  const start = (e: React.PointerEvent, t: T) => {
    e.preventDefault()
    ;(e.currentTarget as HTMLElement).setPointerCapture(e.pointerId)
    setOrder([...items])
    setDragKey(keyOf(t))
  }
  const move = (e: React.PointerEvent) => {
    if (dragKey === null || !order || !list.current) return
    const rows = [...list.current.children] as HTMLElement[]
    const from = order.findIndex(x => keyOf(x) === dragKey)
    let to = rows.findIndex(r => { const b = r.getBoundingClientRect(); return e.clientY < b.top + b.height / 2 })
    if (to === -1) to = rows.length - 1
    else if (to > from) to -= 1
    if (to !== from && to >= 0) { const n = [...order]; const [x] = n.splice(from, 1); n.splice(to, 0, x); setOrder(n) }
  }
  const end = () => {
    if (order && order.map(keyOf).join() !== items.map(keyOf).join()) onReorder(order)
    setOrder(null); setDragKey(null)
  }
  return (
    <ul ref={list} className={className} onPointerMove={move} onPointerUp={end} onPointerCancel={end}>
      {shown.map((t, i) => (
        <li key={keyOf(t)} className={dragKey === keyOf(t) ? 'dragging' : ''} style={{ listStyle: 'none' }}>
          {render(t, <button type="button" className="drag-handle" aria-label="Drag to reorder" onPointerDown={e => start(e, t)}><Icon name="move" size={15} /></button>, i)}
        </li>
      ))}
    </ul>
  )
}
