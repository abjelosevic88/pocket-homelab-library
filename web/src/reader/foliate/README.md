# foliate-js (vendored)

The EPUB, MOBI/KF8 (AZW3), FB2 renderer of the web reader, from
https://github.com/johnfactotum/foliate-js at commit
`78914aef4466eb960965702401634c2cb348e9b1` (2026-05-01), MIT licensed (see
LICENSE). Only the modules the reader needs are copied; `vendor/` holds its
bundled zip.js and fflate.

One local change, in `view.js` `makeBook`: PDFs are refused (Pocket Library
opens them in its own pdf.js reader), so foliate's `pdf.js` and its vendored
pdf.js build are not shipped.

The `.d.ts` files beside the modules are Pocket Library's and type only what
the reader uses.

To update: copy the same files from a newer commit, re-apply the `makeBook`
change, update the commit above and try the reader with an EPUB, an AZW3 and
an FB2.
