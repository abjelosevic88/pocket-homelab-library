// Reading preferences: what the Aa sheet changes, saved per person in
// settings.reader_prefs / settings.comic_prefs (JSON the reader owns).

export type ReaderTheme = 'app' | 'light' | 'sepia' | 'dark' | 'black'
export type FontChoice = 'publisher' | 'serif' | 'sans' | 'readable'

export interface ReaderPrefs {
  theme: ReaderTheme
  font: FontChoice
  size: number // px
  lineHeight: number
  margin: number // side gap, percent of the page
  maxWidth: number // px of one column
  columns: 'auto' | 1 | 2
  justify: boolean
  hyphenate: boolean
  flow: 'paginated' | 'scrolled'
  publisherColors: boolean // keep the book's own text colours in light themes
}

export const DEFAULT_PREFS: ReaderPrefs = {
  theme: 'app', font: 'publisher', size: 18, lineHeight: 1.5, margin: 6, maxWidth: 720, columns: 'auto',
  // a phone's narrow column justifies into wide gaps: ragged right there unless chosen
  justify: typeof window === 'undefined' || window.innerWidth >= 600, hyphenate: true, flow: 'paginated', publisherColors: true,
}

export type ComicMode = 'single' | 'double' | 'scroll'
export type ComicFit = 'width' | 'height' | 'original'

export interface ComicPrefs {
  mode: ComicMode
  fit: ComicFit
  direction: 'auto' | 'ltr' | 'rtl' // auto = the book's (manga read right to left)
  theme: ReaderTheme
}

export const DEFAULT_COMIC: ComicPrefs = { mode: 'single', fit: 'height', direction: 'auto', theme: 'black' }

export function parsePrefs<T extends object>(raw: string | undefined, def: T): T {
  if (!raw) return { ...def }
  try { return { ...def, ...(JSON.parse(raw) as Partial<T>) } } catch { return { ...def } }
}

export const THEMES: Record<Exclude<ReaderTheme, 'app'>, { bg: string; fg: string; link: string; label: string; dark: boolean }> = {
  light: { bg: '#ffffff', fg: '#1d1d1f', link: '#be123c', label: 'Light', dark: false },
  sepia: { bg: '#f4ecd8', fg: '#4b3b2a', link: '#9f1239', label: 'Sepia', dark: false },
  dark: { bg: '#1c1c1e', fg: '#e3e3e6', link: '#fda4af', label: 'Dark', dark: true },
  black: { bg: '#000000', fg: '#c9c9cc', link: '#fb7185', label: 'Black', dark: true },
}

/** The theme to draw: "app" follows the app's light/dark. */
export function resolveTheme(t: ReaderTheme): Exclude<ReaderTheme, 'app'> {
  if (t !== 'app') return t
  return document.documentElement.dataset.theme === 'dark' ? 'dark' : 'light'
}

export const FONTS: Record<FontChoice, { label: string; stack: string }> = {
  publisher: { label: 'Original', stack: '' },
  serif: { label: 'Serif', stack: 'Literata, "Iowan Old Style", Charter, Georgia, "Times New Roman", serif' },
  sans: { label: 'Sans', stack: 'Inter, -apple-system, "Segoe UI", Roboto, "Helvetica Neue", Arial, sans-serif' },
  readable: { label: 'Readable', stack: '"Atkinson Hyperlegible", "OpenDyslexic", Verdana, Tahoma, "Trebuchet MS", sans-serif' },
}

/** CSS injected into every section of a reflowable book. */
export function bookCSS(p: ReaderPrefs): string {
  const t = THEMES[resolveTheme(p.theme)]
  const force = t.dark || p.theme === 'sepia' || !p.publisherColors
  const font = FONTS[p.font].stack
  return `
    @namespace epub "http://www.idpf.org/2007/ops";
    html { color-scheme: ${t.dark ? 'dark' : 'light'}; font-size: ${p.size}px !important; }
    html, body { background: ${t.bg} !important; color: ${t.fg} !important; }
    body { font-size: 1rem !important; }
    ${font ? `body, p, div, span, li, blockquote, dd, dt, td, th, a, em, i, b, strong, small, cite, h1, h2, h3, h4, h5, h6 { font-family: ${font} !important; }` : ''}
    ${force ? `body *:not(img):not(svg):not(svg *) { color: inherit !important; background-color: transparent !important; border-color: currentColor !important; }` : ''}
    a:link, a:visited { color: ${t.link} !important; }
    p, li, blockquote, dd {
      line-height: ${p.lineHeight} !important;
      text-align: ${p.justify ? 'justify' : 'start'};
      -webkit-hyphens: ${p.hyphenate ? 'auto' : 'manual'};
      hyphens: ${p.hyphenate ? 'auto' : 'manual'};
      -webkit-hyphenate-limit-before: 3;
      -webkit-hyphenate-limit-after: 2;
      -webkit-hyphenate-limit-lines: 2;
      hanging-punctuation: allow-end last;
      widows: 2;
    }
    [align="left"] { text-align: left; }
    [align="right"] { text-align: right; }
    [align="center"] { text-align: center; }
    [align="justify"] { text-align: justify; }
    pre { white-space: pre-wrap !important; }
    img, svg { max-width: 100%; }
    ${t.dark ? 'img { filter: brightness(.88); }' : ''}
    aside[epub|type~="endnote"], aside[epub|type~="footnote"], aside[epub|type~="note"], aside[epub|type~="rearnote"] { display: none; }
    ::selection { background: rgba(190, 18, 60, .28); }
  `
}

export const HIGHLIGHT_COLORS: { id: string; label: string; color: string }[] = [
  { id: 'yellow', label: 'Yellow', color: '#facc15' },
  { id: 'green', label: 'Green', color: '#4ade80' },
  { id: 'blue', label: 'Blue', color: '#60a5fa' },
  { id: 'pink', label: 'Pink', color: '#f472b6' },
  { id: 'purple', label: 'Purple', color: '#c084fc' },
  { id: 'underline', label: 'Underline', color: '#be123c' },
]

export function colorOf(id: string): string {
  return HIGHLIGHT_COLORS.find(c => c.id === id)?.color || '#facc15'
}
