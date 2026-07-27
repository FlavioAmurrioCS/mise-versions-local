# /// script
# requires-python = ">=3.14"
# dependencies = [
#     "fastapi>=0.139.0",
#     "httpx>=0.28.1",
#     "uvicorn>=0.51.0",
# ]
# ///

import json
import os
import sys

import anyio
import httpx
from fastapi import FastAPI
from fastapi import Request
from fastapi import Response

app = FastAPI()

client = httpx.AsyncClient(base_url="https://mise-versions.jdx.dev")

PATH = "/{file_path:path}"


@app.post(PATH)
@app.put(PATH)
@app.delete(PATH)
async def read_rest_of_path1(file_path: str, request: Request) -> Response:
    response = await client.request(
        request.method,
        file_path,
        headers=[(k, v) for k, v in request.headers.raw if k != b"host"],
        content=await request.body(),
    )
    return Response(
        content=response.content,
        status_code=response.status_code,
        headers=dict(response.headers),
    )


@app.get(PATH)
async def read_rest_of_path(file_path: str, request: Request) -> Response:
    path = file_path.rstrip("/")
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
            (
                b"if-none-match",
                old_headers.get("etag").encode() if old_headers and "etag" in old_headers else b"",
            )
        ],
        content=await request.body(),
    )

    content = response.content
    status_code = response.status_code
    response_headers = dict(response.headers)
    response_headers.pop("date", None)
    response_headers.pop("cf-ray", None)

    if response.is_error:
        if await anyio.Path(path).exists():
            print(f"There was an error for {path} but using cached version", file=sys.stderr)
            status_code = 200
            response_headers = old_headers
            async with await anyio.open_file(path, "rb") as f:
                content = await f.read()
        return Response(
            content=content,
            status_code=status_code,
            headers=response_headers,
        )

    # with open(meta_file, "w") as f:
    async with await anyio.open_file(meta_file, "w") as f:
        json.dump(response_headers, f, indent=4, sort_keys=True)
    if response.status_code == 304:  # noqa: PLR2004
        print(f"Cache hit for {path}", file=sys.stderr)
        status_code = 200
        async with await anyio.open_file(path, "rb") as f:
            content = await f.read()
    else:
        async with await anyio.open_file(path, "wb") as f:
            await f.write(content)

    return Response(
        content=content,
        status_code=status_code,
        headers=response_headers,
    )


if __name__ == "__main__":
    import uvicorn

    uvicorn.run(app, host="127.0.0.1", port=8000)
