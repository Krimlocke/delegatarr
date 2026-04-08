# --- Build Stage ---
FROM golang:1.22-alpine AS builder

RUN apk add --no-cache gcc musl-dev

WORKDIR /src
COPY . .
RUN go mod tidy && \
    echo "=== BUILD START ===" && \
    CGO_ENABLED=0 go build -v -gcflags=-e -ldflags="-s -w" -o /delegatarr ./cmd/delegatarr 2>&1; \
    echo "=== EXIT CODE: $? ==="

# --- Runtime Stage ---
FROM alpine:3.19

RUN apk add --no-cache tzdata ca-certificates

WORKDIR /app

COPY --from=builder /delegatarr .
COPY --from=builder /src/templates/ templates/
COPY --from=builder /src/static/ static/
COPY --from=builder /src/logo.png .

VOLUME /config
EXPOSE 5555

CMD ["/app/delegatarr"]
