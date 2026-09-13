FROM rust:1.96-bookworm AS agent-build
WORKDIR /src
COPY Cargo.toml Cargo.lock rust-toolchain.toml ./
COPY agent ./agent
RUN cargo build --release --locked -p scout-agent

FROM node:24-bookworm-slim AS deps
WORKDIR /app
COPY package.json package-lock.json ./
RUN npm ci

FROM node:24-bookworm-slim AS build
WORKDIR /app
COPY --from=deps /app/node_modules ./node_modules
COPY . .
RUN npm run build

FROM node:24-bookworm-slim AS runtime
ENV NODE_ENV=production
ENV PORT=8080
WORKDIR /app
RUN groupadd --system --gid 1001 scout && useradd --system --uid 1001 --gid scout scout
COPY --from=build --chown=scout:scout /app/.next/standalone ./
COPY --from=build --chown=scout:scout /app/.next/static ./.next/static
COPY --from=build --chown=scout:scout /app/public ./public
COPY --from=build --chown=scout:scout /app/agent-artifacts ./agent-artifacts
COPY --from=agent-build --chown=scout:scout /src/target/release/scout-agent ./agent-artifacts/scout-agent-linux-x86_64
RUN mkdir -p /data && chown scout:scout /data
USER scout
EXPOSE 8080
CMD ["node", "server.js"]
