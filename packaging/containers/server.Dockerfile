FROM golang:1.26-alpine AS build

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags='-s -w' -o /out/scout-server ./apps/server

FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=build /out/scout-server /usr/local/bin/scout-server
VOLUME ["/var/lib/scout"]
USER nonroot:nonroot
ENTRYPOINT ["/usr/local/bin/scout-server"]
