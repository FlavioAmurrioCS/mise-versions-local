# syntax=docker/dockerfile:1

# Fetch the pre-warmed bundle (docs/ + cache/ + MANIFEST.json) that the
# warm-cache workflow publishes to the rolling `cache-latest` release. This
# replaces downloading the raw mise-versions docs at a pinned commit — the
# bundle already contains the docs plus a warmed /api/github cache.
#
# `cache-latest` is a rolling tag, so build with `--no-cache` (or bump
# BUNDLE_REF) to pick up a newer bundle.
FROM debian:bookworm-slim AS bundle
ARG BUNDLE_REPO=FlavioAmurrioCS/mise-versions-local
ARG BUNDLE_REF=cache-latest
ADD https://github.com/${BUNDLE_REPO}/releases/download/${BUNDLE_REF}/mise-versions-bundle.tar.zst /bundle.tar.zst
RUN \
    : \
    && apt-get update \
    && apt-get install -y --no-install-recommends zstd \
    && mkdir -p /app \
    && tar --zstd -xf /bundle.tar.zst -C /app \
    && rm /bundle.tar.zst \
    && :


FROM golang:1.26-bookworm AS builder

ENV CGO_ENABLED=0

RUN \
    --mount=type=bind,source=main.go,target=/tmp/main.go \
    : \
    && go build -o /mise-versions-local /tmp/main.go \
    && :


# Minimal runtime: static binary + baked docs and warmed cache. distroless/static
# ships CA certs, so the /api/github proxy can still reach the upstream on a miss.
FROM gcr.io/distroless/static-debian12

COPY --from=builder /mise-versions-local /mise-versions-local
COPY --from=bundle /app/docs /app/docs
COPY --from=bundle /app/cache /app/cache
COPY --from=bundle /app/MANIFEST.json /app/MANIFEST.json

ENV DOCS_DIR=/app/docs \
    CACHE_DIR=/app/cache \
    ADDR=0.0.0.0:8080

EXPOSE 8080
ENTRYPOINT ["/mise-versions-local"]
