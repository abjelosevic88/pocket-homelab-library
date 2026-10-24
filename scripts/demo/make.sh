#!/usr/bin/env bash
# A demo library of public-domain books (Project Gutenberg EPUBs with their covers) for screenshots
# and trying Pocket Library out. Never point screenshots at a real library: the repository is public.
#   scripts/demo/make.sh [dir]     (default ~/.cache/pocket-library-demo)
# Then run the server with LIBRARIES="Books=<dir>/books" and DATA_DIR=<dir>/data.
set -euo pipefail
here=$(cd "$(dirname "$0")" && pwd)
dir=${1:-$HOME/.cache/pocket-library-demo}
mkdir -p "$dir/books"
while IFS='|' read -r id author title; do
  [[ -z "$id" || "$id" == \#* ]] && continue
  out="$dir/books/$author/$author - $title.epub"
  [[ -s "$out" ]] && continue
  mkdir -p "$dir/books/$author"
  echo "fetching $title"
  curl -fsSL --retry 2 -A "PocketLibrary demo (+https://github.com/abjelosevic88/pocket-homelab-library)" \
    "https://www.gutenberg.org/ebooks/$id.epub3.images" -o "$out.part" && mv "$out.part" "$out" || rm -f "$out.part"
  sleep 1
done < "$here/titles.txt"
echo "demo books in $dir/books"
