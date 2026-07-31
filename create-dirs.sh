#!/usr/bin/env bash

directories=(
    ./dir_cache/arm64/cache
    ./dir_cache/arm64/config
    ./dir_cache/arm64/data
    ./dir_cache/arm64/state
    ./dir_cache/amd64/cache
    ./dir_cache/amd64/config
    ./dir_cache/amd64/data
    ./dir_cache/amd64/state
)

rm -rf "${directories[@]}"




mkdir -p "${directories[@]}"
