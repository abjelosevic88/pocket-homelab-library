// Types for foliate-js's footnotes.js.
/* eslint-disable @typescript-eslint/no-explicit-any */
export class FootnoteHandler extends EventTarget {
  detectFootnotes: boolean
  handle(book: any, e: Event): Promise<void> | undefined
}
