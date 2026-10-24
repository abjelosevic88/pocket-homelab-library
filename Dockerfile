# syntax=docker/dockerfile:1.7
# ---------- 1. build the web UI ----------
FROM --platform=$BUILDPLATFORM node:22-alpine AS web
WORKDIR /src/web
COPY web/package.json web/package-lock.json* ./
RUN npm ci --no-audit --no-fund
COPY web/ ./
RUN npm run build

# ---------- 2. build the Go binary ----------
FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS build
ARG TARGETOS TARGETARCH VERSION=dev
WORKDIR /src
COPY go.mod go.sum ./
COPY third_party/pocket-core ./third_party/pocket-core
RUN cd third_party/pocket-core && sha256sum -c SHA256SUMS
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY . .
COPY --from=web /src/web/dist ./web/dist
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /out/pocket-library ./cmd/pocket-library

# ---------- 3. runtime ----------
# Debian, so calibre's own build runs when it is wanted (WITH_CALIBRE=true:
# conversions such as MOBI → EPUB for Send to Kindle; amd64 and arm64 only).
# poppler-utils gives PDF covers and page counts.
FROM debian:bookworm-slim
ARG WITH_CALIBRE=false
RUN apt-get update \
 && apt-get install -y --no-install-recommends ca-certificates tzdata curl poppler-utils \
 && if [ "$WITH_CALIBRE" = "true" ]; then \
      apt-get install -y --no-install-recommends xz-utils python3 libegl1 libopengl0 libglx0 libxcb-cursor0 libxkbcommon0 libnss3 libfontconfig1 libfreetype6 libxdamage1 libxrandr2 libxcomposite1 libxtst6 libxkbfile1 libasound2 \
      && curl -fsSL https://download.calibre-ebook.com/linux-installer.sh | sh /dev/stdin install_dir=/opt isolated=y \
      && ln -s /opt/calibre/ebook-convert /usr/local/bin/ebook-convert \
      && apt-get purge -y xz-utils && apt-get autoremove -y; \
    fi \
 && rm -rf /var/lib/apt/lists/* \
 && groupadd -g 1000 app && useradd -u 1000 -g app -d /data -M app
COPY --from=build /out/pocket-library /usr/local/bin/pocket-library
ENV DATA_DIR=/data PORT=8080 HOST=0.0.0.0 QT_QPA_PLATFORM=offscreen
VOLUME ["/data"]
EXPOSE 8080
USER app
WORKDIR /data
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 CMD curl -fsS http://localhost:8080/healthz || exit 1
LABEL org.opencontainers.image.title="Pocket Library" \
      org.opencontainers.image.description="Every ebook, comic and manga in one place: a library that files and fixes your books, reads EPUB, PDF, MOBI, comics and manga in the browser, keeps your place with KOReader and CrossPoint, and sends books to your Kindle and e-readers." \
      org.opencontainers.image.source="https://github.com/abjelosevic88/pocket-homelab-library" \
      org.opencontainers.image.licenses="MIT"
ENTRYPOINT ["pocket-library"]
