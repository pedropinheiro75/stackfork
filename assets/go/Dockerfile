ARG GO_VERSION=1.24
FROM golang:${GO_VERSION}-bookworm
RUN apt-get update && apt-get install -y --no-install-recommends curl \
    && rm -rf /var/lib/apt/lists/*
WORKDIR /app
COPY .agent-dev/entrypoint.sh /usr/local/bin/stackfork-dev-entrypoint
