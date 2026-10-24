// Types for the parts of foliate-js's view.js that Pocket Library uses.
/* eslint-disable @typescript-eslint/no-explicit-any */
export class ResponseError extends Error {}
export class NotFoundError extends Error {}
export class UnsupportedTypeError extends Error {}
export function makeBook(file: File | string): Promise<any>
