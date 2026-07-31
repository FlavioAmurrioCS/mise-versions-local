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

CLIENTS = {
    "mise-versions.jdx.dev": httpx.AsyncClient(base_url="https://mise-versions.jdx.dev"),
    "api.github.com": httpx.AsyncClient(
        base_url="https://api.github.com",
        auth=httpx.BasicAuth("", os.environ.get("MISE_GITHUB_TOKEN", "")),
    ),
}


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


# @app.post(PATH)
# @app.put(PATH)
# @app.delete(PATH)
# async def read_rest_of_path1(file_path: str, request: Request) -> Response:
#     response = await CLIENTS["mise-versions.jdx.dev"].request(
#         request.method,
#         file_path,
#         headers=[(k, v) for k, v in request.headers.raw if k not in (b"host", b"accept-encoding")]
#         + [(b"accept-encoding", b"identity")],
#         content=await request.body(),
#     )
#     return Response(
#         content=response.content,
#         status_code=response.status_code,
#         headers=clean_headers(response.headers),
#     )


async def mirror(request: Request) -> Response:
    cache_location = anyio.Path(request.url.path.strip("/"))
    try:
        await cache_location.parent.mkdir(parents=True, exist_ok=True)
    except (FileExistsError, NotADirectoryError):
        # A previously recorded URL is now a path prefix: /repos/x/releases was stored as a
        # file, and now /repos/x/releases/tags/v1 needs it to be a directory. Convert the
        # file into <name>/index.html, which is where the read path below looks anyway.
        # One level up raises FileExistsError; a deeper ancestor raises NotADirectoryError.
        walker = cache_location.parent
        while walker != walker.parent and not await walker.is_file():
            walker = walker.parent
        old_meta = anyio.Path(str(walker) + ".meta")
        new_file = walker.with_name(walker.name + ".tmp")
        await walker.rename(new_file)
        await cache_location.parent.mkdir(parents=True, exist_ok=True)
        await new_file.rename(walker / "index.html")
        # Moved, not deleted, so the stored etag still drives If-None-Match next time.
        if await old_meta.exists():
            await old_meta.rename(walker / "index.html.meta")

    if await cache_location.is_dir():
        cache_location = cache_location / "index.html"
    meta_file = anyio.Path(str(cache_location) + ".meta")
    old_headers: dict[str, str] = {}
    if await meta_file.exists():
        async with await anyio.open_file(meta_file) as f:
            old_headers = json.loads(await f.read())

    _, domain, url_path = request.url.path.split("/", 2)
    # path = os.path.join(domain, file_path.strip("/"))
    # os.makedirs(os.path.dirname(path), exist_ok=True)
    # meta_file = path + ".meta"
    # old_headers: dict[str, str] = {}
    # if await anyio.Path(meta_file).exists():
    #     async with await anyio.open_file(meta_file, "r") as f:
    #         old_headers = json.loads(await f.read())
    headers = [(k, v) for k, v in request.headers.raw if k not in (b"host", b"accept-encoding")] + [
        # We store decoded bytes, so ask for decoded bytes. Besides removing any need
        # for httpx to decompress, this keeps the etag stable: Cloudflare returns a weak
        # etag (W/"...") for a gzipped representation and a strong one otherwise, which
        # would otherwise flip-flop the .meta file on every re-record.
        (b"accept-encoding", b"identity"),
        (
            b"if-none-match",
            (old_headers.get("etag") or "").encode()
            if old_headers and "etag" in old_headers
            else b"",
        ),
    ]
    response = await CLIENTS[domain].request(
        request.method,
        url=url_path,
        params=request.query_params,
        headers=headers,
        content=await request.body(),
    )

    content = response.content
    status_code = response.status_code
    response_headers = clean_headers(response.headers)

    if response.is_error:
        if await cache_location.exists():
            print(
                f"There was an error for {cache_location} but using cached version", file=sys.stderr
            )
            status_code = 200
            # Cleaned on the way out too, so .meta files recorded before the allowlist
            # existed cannot resurrect a stale content-encoding.
            response_headers = clean_headers(old_headers)
            async with await cache_location.open("rb") as f:
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
        print(f"Cache hit for {url_path}", file=sys.stderr)
        status_code = 200
        async with await cache_location.open("rb") as f:
            content = await f.read()
    else:
        async with await cache_location.open("wb") as f:
            await f.write(content)

    async with await anyio.open_file(meta_file, "w") as f:
        await f.write(json.dumps(response_headers, indent=4, sort_keys=True))

    return Response(
        content=content,
        status_code=status_code,
        headers=response_headers,
    )


# @app.get("/repos/{file_path:path}")
# async def api_github_com(file_path: str, request: Request) -> Response:
#     return await mirror("api.github.com", request)

# @app.get(PATH)
# async def mise_versions_jdx_dev(file_path: str, request: Request) -> Response:
#     return await mirror("mise-versions.jdx.dev", request)


@app.get("/api.github.com/{file_path:path}")
@app.get("/mise-versions.jdx.dev/{file_path:path}")
async def api_github_com(file_path: str, request: Request) -> Response:
    return await mirror(request)


if __name__ == "__main__":
    import uvicorn

    uvicorn.run(app, host="0.0.0.0", port=8000)
