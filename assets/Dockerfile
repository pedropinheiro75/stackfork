ARG BUN_VERSION=1.3.9
FROM oven/bun:${BUN_VERSION}
USER root
RUN apt-get update && apt-get install -y --no-install-recommends curl \
    && rm -rf /var/lib/apt/lists/*
WORKDIR /app
COPY .agent-dev/entrypoint.sh /usr/local/bin/idv-dev-entrypoint
