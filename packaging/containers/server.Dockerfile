FROM node:24-bookworm-slim AS deps
WORKDIR /app
COPY package.json package-lock.json ./
RUN npm ci

FROM rust:1.96-bookworm AS agent-build
WORKDIR /src
COPY Cargo.toml Cargo.lock ./
COPY agent ./agent
RUN cargo build --release --locked -p scout-agent

FROM node:24-bookworm-slim AS build
WORKDIR /app
COPY --from=deps /app/node_modules ./node_modules
ARG SCOUT_USE_PREBUILT_ARTIFACTS=0
ENV SCOUT_USE_PREBUILT_ARTIFACTS=$SCOUT_USE_PREBUILT_ARTIFACTS
COPY . .
COPY --from=agent-build /src/target/release/scout-agent /tmp/scout-agent
RUN SCOUT_AGENT_BINARY=/tmp/scout-agent npm run build

FROM node:24-bookworm-slim AS runtime
ENV NODE_ENV=production
ENV PORT=8080
ENV SCOUT_AGENT_ARTIFACT_DIR=/app/agent-artifacts
WORKDIR /app
RUN groupadd --system --gid 1001 scout && useradd --system --uid 1001 --gid scout scout
COPY --from=build --chown=scout:scout /app/.next/standalone ./
COPY --from=build --chown=scout:scout /app/.next/static ./.next/static
COPY --from=build --chown=scout:scout /app/public ./public
COPY --from=build --chown=scout:scout /app/agent-artifacts ./agent-artifacts
COPY --chown=scout:scout packaging/containers/scout-ops.mjs /usr/local/bin/scout-ops.mjs
RUN mkdir -p /data && chown scout:scout /data
USER 1001
EXPOSE 8080
CMD ["node", "server.js"]
