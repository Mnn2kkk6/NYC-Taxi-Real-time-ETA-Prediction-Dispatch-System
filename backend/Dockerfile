# Build the Go API as a static binary.
FROM golang:1.24-alpine AS build

WORKDIR /src
COPY go.mod ./
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/server ./cmd/server

# Small non-root runtime image.
FROM alpine:3.21

RUN apk add --no-cache ca-certificates tzdata \
    && addgroup -S appgroup \
    && adduser -S -D -H -u 10001 -G appgroup appuser

COPY --from=build /out/server /usr/local/bin/server

USER appuser
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/server"]
