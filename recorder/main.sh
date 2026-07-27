#!/usr/bin/env bash

export UV_PROJECT_ENVIRONMENT="/tmp/foo"
uv sync --python 3.13

exec "${UV_PROJECT_ENVIRONMENT}/bin/python" ./recorder/recorder_server.py "$@"
