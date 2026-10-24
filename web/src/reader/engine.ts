// What the reader shell needs from each engine (foliate for reflowable
// books, pdf.js for PDFs, the page reader for comics).
import type { Annotation, Book, BookFile, UserBook } from '../lib/types'
import type { ComicPrefs, ReaderPrefs } from './prefs'

/** GET /api/v1/books/{id}/reader */
export interface ReaderData {
  book: Book & { files: BookFile[] }
  file: BookFile | null
  engine: 'foliate' | 'pdf' | 'comic' | 'none'
  locator: string
  percentage: number
  annotations: Annotation[]
  state: UserBook | null
}

export interface TocItem { label: string; href: string | number; subitems?: TocItem[] }

export interface RelocateInfo {
  percentage: number // 0..1 of the whole book
  locator: Record<string, unknown>
  chapter: string
  chapterHref?: string | number
  page?: string // "Page 12", "12 / 300"
  minutesLeft?: number // in this chapter, at the book's own pace estimate
  atEnd: boolean
  turned: boolean // a page was turned to get here (not the first show)
}

export interface SearchHit { label: string; pre: string; match: string; post: string; target: string | number }

export interface SelectionInfo {
  text: string
  rect: DOMRect // in the page's coordinates
  locator: Record<string, unknown>
  chapter: string
  percentage: number
  page: number
  cfi: string
}

export interface EngineApi {
  next(): void
  prev(): void
  left(): void
  right(): void
  goToFraction(f: number): void
  goTo(target: string | number): void
  goToAnnotation(a: Annotation): void
  toc(): TocItem[]
  sectionFractions(): number[]
  search?(q: string, onHit: (h: SearchHit) => void, onDone: () => void): () => void
  clearSearch?(): void
  drawAnnotation?(a: Annotation): void
  removeAnnotation?(a: Annotation): void
  clearSelection?(): void
  /** The bookmark on the page shown, if any. */
  bookmarkHere(list: Annotation[]): Annotation | undefined
}

export interface EngineProps {
  data: ReaderData
  file: BookFile
  prefs: ReaderPrefs
  comicPrefs: ComicPrefs
  annotations: Annotation[]
  onReady(api: EngineApi): void
  onRelocate(info: RelocateInfo): void
  onSelect(sel: SelectionInfo | null): void
  onAnnotationClick(a: Annotation, rect: DOMRect): void
  onTap(zone: 'left' | 'right' | 'center'): void
  onActivity(): void
  onKey(e: KeyboardEvent): void
  onError(message: string): void
}

/** An EngineApi that always calls the latest one an engine rendered (engines
 *  that are React state machines rebuild their api on every render). */
export function delegate(ref: { current: EngineApi }): EngineApi {
  const r = () => ref.current
  return {
    next: () => r().next(), prev: () => r().prev(), left: () => r().left(), right: () => r().right(),
    goToFraction: f => r().goToFraction(f), goTo: t => r().goTo(t), goToAnnotation: a => r().goToAnnotation(a),
    toc: () => r().toc(), sectionFractions: () => r().sectionFractions(),
    search: (q, h, d) => r().search?.(q, h, d) ?? (() => {}),
    clearSearch: () => r().clearSearch?.(), drawAnnotation: a => r().drawAnnotation?.(a), removeAnnotation: a => r().removeAnnotation?.(a),
    clearSelection: () => r().clearSelection?.(), bookmarkHere: l => r().bookmarkHere(l),
  }
}
