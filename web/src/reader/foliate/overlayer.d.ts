// Types for foliate-js's overlayer.js.
/* eslint-disable @typescript-eslint/no-explicit-any */
type Draw = (rects: DOMRectList | DOMRect[], options?: Record<string, unknown>) => SVGElement
export class Overlayer {
  static highlight: Draw
  static underline: Draw
  static outline: Draw
  static squiggly: Draw
  static strikethrough: Draw
  add(key: string, range: Range, draw: Draw, options?: Record<string, unknown>): void
  remove(key: string): void
  hitTest(e: { x: number; y: number }): [string, Range] | []
}
