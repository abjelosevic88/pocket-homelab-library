# Behind a reverse proxy

Pocket Library is one HTTP service on port 8080. Book files, comic pages and uploads stream through it, so the
proxy should not buffer them, and uploads can be large (`MAX_BOOK_UPLOAD_BYTES`, 2 GiB by default). Set
`SECURE_COOKIES=true` behind HTTPS and `BASE_URL` to the public address: OPDS feeds and device setup show it.

E-readers use the same host: `/opds` (the catalogue) and `/kosync` (KOReader sync), `/koreader/…` (the plugin).
Don't put a sign-in page in front of those paths: devices sign in with HTTP Basic or KOReader's headers.

## Caddy

```caddy
library.example.com {
	reverse_proxy pocket-library:8080 {
		flush_interval -1
	}
}
```

## nginx

```nginx
client_max_body_size 2g;
location / {
    proxy_pass http://pocket-library:8080;
    proxy_http_version 1.1;
    proxy_set_header Host $host;
    proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
    proxy_set_header X-Forwarded-Proto $scheme;
    proxy_buffering off;
    proxy_request_buffering off;
    proxy_read_timeout 1h;
}
```

## Traefik

Nothing special: Traefik streams by default.

## Forward auth (Authelia, Authentik, Tinyauth)

Exclude `/opds`, `/kosync`, `/koreader` and `/api/v1/` from the forward-auth check, or e-readers and the
Pocket Core links stop working; Pocket Library signs those requests in itself.
