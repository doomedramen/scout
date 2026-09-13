FROM node:24-alpine AS web-build

WORKDIR /src
COPY package.json package-lock.json ./
COPY apps/agent/package.json apps/agent/package.json
COPY apps/server/package.json apps/server/package.json
COPY apps/web/package.json apps/web/package.json
RUN npm ci --ignore-scripts
COPY apps/web apps/web
RUN npm run build -w @scout/web

FROM golang:1.26-alpine AS build

ARG TARGETOS=linux
ARG TARGETARCH

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN if [ -n "${TARGETARCH}" ]; then \
      CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build -trimpath -ldflags='-s -w' -o /out/scout-server ./apps/server; \
    else \
      CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o /out/scout-server ./apps/server; \
    fi
RUN mkdir -p /out/agent \
    && CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags='-s -w' -o /out/agent/scout-agent-linux-amd64 ./apps/agent \
    && CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -ldflags='-s -w' -o /out/agent/scout-agent-linux-arm64 ./apps/agent

FROM alpine:3.22 AS permissions

RUN mkdir -p /var/lib/scout && chown 65532:65532 /var/lib/scout

FROM node:24-alpine

COPY --from=build /out/scout-server /usr/local/bin/scout-server
COPY --from=build /out/agent /usr/local/share/scout/agent
COPY scripts/install-agent.sh /usr/local/share/scout/agent/install-agent.sh
COPY packaging/linux/agent.service /usr/local/share/scout/agent/agent.service
COPY --from=web-build /src/apps/web/.next/standalone /app/web
COPY --from=web-build /src/apps/web/.next/static /app/web/apps/web/.next/static
COPY packaging/containers/start-server.sh /usr/local/bin/start-server
COPY --from=permissions --chown=node:node /var/lib/scout /var/lib/scout
RUN chmod 0755 /usr/local/bin/start-server
ENV PORT=8080
ENV HOSTNAME=0.0.0.0
ENV SCOUT_LISTEN=127.0.0.1:8081
ENV SCOUT_API_ORIGIN=http://127.0.0.1:8081
VOLUME ["/var/lib/scout"]
USER node:node
ENTRYPOINT ["/usr/local/bin/start-server"]
