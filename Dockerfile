
FROM golang:1.26-bookworm AS downloader

ARG MISE_VERSIONS_COMMIT=574afbdd9b4397d4c28f4306d1fb83815ad6b448
# ADD https://github.com/jdx/mise-versions/archive/refs/heads/main.zip /main.zip
ADD https://github.com/jdx/mise-versions/archive/${MISE_VERSIONS_COMMIT}.zip /main.zip

RUN \
    : \
    && apt update \
    && apt install -y unzip \
    && mkdir -p /app \
    && cd /app \
    && unzip /main.zip \
    && mv /app/mise-versions-${MISE_VERSIONS_COMMIT} /app/mise-versions \
    && :


FROM golang:1.26-bookworm AS builder

RUN mkdir -p /app

WORKDIR /app

COPY --from=downloader /app/mise-versions/docs /app/docs

RUN \
    --mount=type=bind,source=main.go,target=/tmp/main.go \
    : \
    && go build -o /app/mise-versions-local /tmp/main.go \
    && :

ENTRYPOINT ["/app/mise-versions-local"]
