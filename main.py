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
from fastapi import FastAPI, Request
import httpx
from fastapi import Response

app = FastAPI()

client = httpx.AsyncClient(base_url="https://mise-versions.jdx.dev")

PATH = "/{file_path:path}"

@app.post(PATH)
@app.put(PATH)
@app.delete(PATH)
async def read_rest_of_path(file_path: str, request: Request):
    response = await client.request(
            request.method,
            file_path,
            headers=[(k,v) for k,v in request.headers.raw if k not in (b"host",)],
            content=await request.body()
    )
    return Response(
        content=response.content,
        status_code=response.status_code,
        headers=dict(response.headers),
    )

@app.get(PATH)
async def read_rest_of_path(file_path: str, request: Request):
    path = file_path.rstrip("/")
    os.makedirs(os.path.dirname(path), exist_ok=True)
    meta_file = path + ".meta"
    old_headers: dict[str,str] = {}
    if os.path.exists(meta_file):
        with open(meta_file) as f:
            old_headers = json.load(f)
    response = await client.request(
        request.method,
        file_path,
        headers=[(k,v) for k,v in request.headers.raw if k not in (b"host",b"accept-encoding")] + [(b"if-none-match", old_headers.get("etag").encode() if old_headers and "etag" in old_headers else b"")],
        content=await request.body()
    )

    content = response.content
    status_code = response.status_code
    response_headers = dict(response.headers)
    response_headers.pop("date", None)
    response_headers.pop("cf-ray", None)

    if response.is_error:
        if os.path.exists(path):
            print(f"There was an error for {path} but using cached version", file=sys.stderr)
            status_code = 200
            response_headers = old_headers
            with open(path, "rb") as f:
                content = f.read()
        return Response(
                content=content,
                status_code=status_code,
                headers=response_headers,
            )

    with open(meta_file, "w") as f:
        json.dump(response_headers, f, indent=4, sort_keys=True)
    if response.status_code == 304:
        print(f"Cache hit for {path}", file=sys.stderr)
        status_code = 200
        with open(path, "rb") as f:
            content = f.read()
    else:
        with open(path, "wb") as f:
            f.write(content)

    return Response(
        content=content,
        status_code=status_code,
        headers=response_headers,
    )

if __name__ == "__main__":
    import uvicorn
    uvicorn.run(app, host="0.0.0.0", port=8000)
