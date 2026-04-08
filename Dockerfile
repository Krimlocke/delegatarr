# --- Build Stage ---
FROM golang:1.22-alpine AS builder

RUN apk add --no-cache gcc musl-dev

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o /delegatarr ./cmd/delegatarr

# --- Runtime Stage ---
FROM alpine:3.19

RUN apk add --no-cache tzdata ca-certificates

WORKDIR /app

COPY --from=builder /delegatarr .
COPY templates/ templates/
COPY static/ static/
COPY logo.png .

VOLUME /config
EXPOSE 5555

CMD ["/app/delegatarr"]
