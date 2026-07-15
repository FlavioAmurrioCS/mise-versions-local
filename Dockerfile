# syntax=docker/dockerfile:1

# Self-contained, offline-capable mirror: a static binary plus the warmed tree
# (data/ + tools/ + api/) that lives in the repo. Build after the tree is
# populated (run the local warm loop or let the warm-cache workflow commit it).

FROM golang:1.26-bookworm AS builder

ENV CGO_ENABLED=0
WORKDIR /src

# Build the whole module (not a single bind-mounted file), so adding a source
# file or dependency can't silently break the image.
RUN \
    --mount=type=bind,target=/src \
    --mount=type=cache,target=/root/.cache/go-build \
    : \
    && go build -o /mise-versions-local . \
    && :


# Minimal runtime. distroless/static ships CA certs, so the /api/github proxy can
# still reach the upstream on a miss.
FROM gcr.io/distroless/static-debian12

COPY --from=builder /mise-versions-local /mise-versions-local
# The warmed mirror tree, straight from the build context (the repo).
COPY data /app/data
COPY tools /app/tools
COPY api /app/api

# DOCS_DIR serves /data + /tools from the synced tomls; CACHE_DIR is the tree
# root so /api/github/* maps to /app/api/github/*.
ENV DOCS_DIR=/app/data \
    CACHE_DIR=/app \
    ADDR=0.0.0.0:8080

EXPOSE 8080
ENTRYPOINT ["/mise-versions-local"]
