FROM ghcr.io/astral-sh/uv:debian

RUN : \
    && mkdir -p ~/.local/bin \
    && mkdir -p ~/.cache/mise \
    && mkdir -p ~/.config/mise \
    && mkdir -p ~/.local/share/mise \
    && mkdir -p ~/.local/share/mise/shims \
    && mkdir -p ~/.local/state/mise \
    && :
