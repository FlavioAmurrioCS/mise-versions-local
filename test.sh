#!/usr/bin/env bash

docker compose down
./create-dirs.sh
rm -rf api.github.com/*

docker compose up -d recorder
sleep 5


docker run -it --rm --entrypoint= --network=mise-versions-local_default jdxcode/mise \
    env MISE_URL_REPLACEMENTS='{ "https://mise-versions.jdx.dev" : "http://recorder:8000/mise-versions.jdx.dev", "https://api.github.com" : "http://recorder:8000/api.github.com" }' \
    mise use -v uv github:docker/buildx 2>&1 | tee mise.log

docker compose logs recorder > recorder.log 2>&1
