# Build
FROM golang:1.24-bookworm AS build
WORKDIR /src
ENV GOTOOLCHAIN=auto
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o /out/stresstool ./cmd/stresstool

# Runtime: CA bundle for HTTPS targets (e.g. httpbin) from nodes/controller
FROM alpine:3.20
RUN apk add --no-cache ca-certificates
COPY --from=build /out/stresstool /usr/local/bin/stresstool
ENTRYPOINT ["/usr/local/bin/stresstool"]
