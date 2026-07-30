# /// script
# requires-python = ">=3.13"
# dependencies = [
#     "fastapi>=0.139.0",
#     "httpx>=0.28.1",
#     "uvicorn>=0.51.0",
# ]
# ///

import json
import os
import sys
from collections.abc import Mapping

import anyio
import httpx
from fastapi import FastAPI
from fastapi import Request
from fastapi import Response

app = FastAPI()

client = httpx.AsyncClient(base_url="https://mise-versions.jdx.dev")

PATH = "/{file_path:path}"

STORED_HEADERS = ("cache-control", "content-type", "etag", "last-modified")


def clean_headers(headers: Mapping[str, str]) -> dict[str, str]:
    """Keep only the headers that describe the bytes we store.

    content-encoding/content-length/transfer-encoding are dropped because they describe the
    upstream transfer, not what we wrote to disk; forwarding a stale content-encoding makes
    the client try to gunzip plaintext. We now request `identity` upstream so this is
    belt-and-braces, but it still matters for any hop that compresses anyway.

    Volatile CDN headers (age, cf-cache-status, cf-ray, date) are dropped so the .meta files
    stay byte-stable across re-records -- they are committed and served by GitHub Pages.
    """
    return {k: v for k in STORED_HEADERS if (v := headers.get(k)) is not None}


@app.post(PATH)
@app.put(PATH)
@app.delete(PATH)
async def read_rest_of_path1(file_path: str, request: Request) -> Response:
    response = await client.request(
        request.method,
        file_path,
        headers=[(k, v) for k, v in request.headers.raw if k not in (b"host", b"accept-encoding")]
        + [(b"accept-encoding", b"identity")],
        content=await request.body(),
    )
    return Response(
        content=response.content,
        status_code=response.status_code,
        headers=clean_headers(response.headers),
    )


@app.get(PATH)
async def read_rest_of_path(file_path: str, request: Request) -> Response:
    path = os.path.join("mise-versions.jdx.dev", file_path.rstrip("/"))
    os.makedirs(os.path.dirname(path), exist_ok=True)
    meta_file = path + ".meta"
    old_headers: dict[str, str] = {}
    if await anyio.Path(meta_file).exists():
        async with await anyio.open_file(meta_file, "r") as f:
            old_headers = json.loads(await f.read())
    response = await client.request(
        request.method,
        file_path,
        headers=[(k, v) for k, v in request.headers.raw if k not in (b"host", b"accept-encoding")]
        + [
            # We store decoded bytes, so ask for decoded bytes. Besides removing any need
            # for httpx to decompress, this keeps the etag stable: Cloudflare returns a weak
            # etag (W/"...") for a gzipped representation and a strong one otherwise, which
            # would otherwise flip-flop the .meta file on every re-record.
            (b"accept-encoding", b"identity"),
            (
                b"if-none-match",
                old_headers.get("etag").encode() if old_headers and "etag" in old_headers else b"",
            ),
        ],
        content=await request.body(),
    )

    content = response.content
    status_code = response.status_code
    response_headers = clean_headers(response.headers)

    if response.is_error:
        if await anyio.Path(path).exists():
            print(f"There was an error for {path} but using cached version", file=sys.stderr)
            status_code = 200
            # Cleaned on the way out too, so .meta files recorded before the allowlist
            # existed cannot resurrect a stale content-encoding.
            response_headers = clean_headers(old_headers)
            async with await anyio.open_file(path, "rb") as f:
                content = await f.read()
        return Response(
            content=content,
            status_code=status_code,
            headers=response_headers,
        )

    if response.status_code == 304:  # noqa: PLR2004
        # A 304 carries only validators -- no content-type -- so keep what the last 200
        # recorded rather than overwriting the meta file with a sparser set.
        response_headers = clean_headers(old_headers) | response_headers
        print(f"Cache hit for {path}", file=sys.stderr)
        status_code = 200
        async with await anyio.open_file(path, "rb") as f:
            content = await f.read()
    else:
        async with await anyio.open_file(path, "wb") as f:
            await f.write(content)

    async with await anyio.open_file(meta_file, "w") as f:
        await f.write(json.dumps(response_headers, indent=4, sort_keys=True))

    return Response(
        content=content,
        status_code=status_code,
        headers=response_headers,
    )


if __name__ == "__main__":
    import uvicorn

    uvicorn.run(app, host="0.0.0.0", port=8000)
