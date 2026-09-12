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

ARG TARGETOS
ARG TARGETARCH

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH:-amd64} go build -trimpath -ldflags='-s -w' -o /out/scout-server ./apps/server
RUN mkdir -p /out/agent \
    && CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags='-s -w' -o /out/agent/scout-agent-linux-amd64 ./apps/agent \
    && CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -ldflags='-s -w' -o /out/agent/scout-agent-linux-arm64 ./apps/agent

FROM alpine:3.22 AS permissions

RUN mkdir -p /var/lib/scout && chown 65532:65532 /var/lib/scout

FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=build /out/scout-server /usr/local/bin/scout-server
COPY --from=build /out/agent /usr/local/share/scout/agent
COPY scripts/install-agent.sh /usr/local/share/scout/agent/install-agent.sh
COPY packaging/linux/agent.service /usr/local/share/scout/agent/agent.service
COPY --from=web-build /src/apps/web/dist /usr/local/share/scout/web
COPY --from=permissions --chown=nonroot:nonroot /var/lib/scout /var/lib/scout
ENV SCOUT_WEB_DIR=/usr/local/share/scout/web
VOLUME ["/var/lib/scout"]
USER nonroot:nonroot
ENTRYPOINT ["/usr/local/bin/scout-server"]
