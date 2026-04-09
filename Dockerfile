# --- Build Stage ---
FROM golang:1.22-alpine AS builder

WORKDIR /src

# Cache dependency downloads in their own layer
COPY go.mod go.sum* ./
RUN go mod download 2>/dev/null || true

COPY . .
RUN go mod tidy
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o /delegatarr ./cmd/delegatarr

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
