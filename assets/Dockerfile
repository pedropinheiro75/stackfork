ARG BUN_VERSION=1.3.9
FROM oven/bun:${BUN_VERSION} AS bun
FROM node:22-bookworm-slim
COPY --from=bun /usr/local/bin/bun /usr/local/bin/bun
RUN ln -s /usr/local/bin/bun /usr/local/bin/bunx
USER root
RUN apt-get update && apt-get install -y --no-install-recommends curl \
    && rm -rf /var/lib/apt/lists/*
WORKDIR /app
COPY .agent-dev/entrypoint.sh /usr/local/bin/idv-dev-entrypoint
