FROM golang:1.26-alpine AS build

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags='-s -w' -o /out/scout-agent ./apps/agent

FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=build /out/scout-agent /usr/local/bin/scout-agent
VOLUME ["/var/lib/scout/agent"]
USER nonroot:nonroot
ENTRYPOINT ["/usr/local/bin/scout-agent"]
