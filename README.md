# Mise Versions Local

A **record-and-replay offline mirror** of `https://mise-versions.jdx.dev` and
`https://api.github.com`. A small FastAPI "recorder" proxies mise's requests to the real
hosts and writes every `GET` response to disk as a file tree mirroring the URL path
(`mise-versions.jdx.dev/…`, `api.github.com/…`). The resulting tree is committed and can be
served statically (e.g. from GitHub Pages).

## Why

mise resolves tool versions from `mise-versions.jdx.dev`, whose `/api/github/*` routes mirror
GitHub release metadata. Without a local mirror, mise falls back to `api.github.com` and
quickly hits the unauthenticated 60-requests/hour rate limit.

## Run it

```bash
./sync.sh          # seed mise-versions.jdx.dev/data/ from the jdx/mise-versions docs
./create-dirs.sh   # (re)create the dir_cache mount dirs — DESTRUCTIVE, wipes dir_cache/*
docker compose up  # recorder starts, then the invoker installs tools through it

docker compose down # Teardown
```

## Add tools to the cache

Edit the `tools=(...)` array (and `number_of_versions`) in `invoker/main.sh`. The last N
versions of each tool are installed so their checksums get cached too.
