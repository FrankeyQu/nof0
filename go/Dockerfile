# Build stage
FROM golang:1.22-alpine AS builder

WORKDIR /build

# Install build dependencies
RUN apk add --no-cache git

# Copy go mod files
COPY go.mod go.sum ./
RUN go mod download

# Copy source code
COPY . .

# Build the application and the preflight gate
RUN CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo -o nof0-api nof0.go && \
    CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo -o nof0-preflight ./cmd/preflight

# Runtime stage
FROM alpine:latest

WORKDIR /app

# Install ca-certificates for HTTPS
RUN apk --no-cache add ca-certificates

# Copy binary from builder
COPY --from=builder /build/nof0-api .
COPY --from=builder /build/nof0-preflight .
COPY --from=builder /build/etc ./etc
COPY --from=builder /build/schemas ./schemas
COPY --from=builder /build/start.sh ./start.sh

# Create non-root user
RUN addgroup -g 1000 nof0 && \
    adduser -D -u 1000 -G nof0 nof0 && \
    chown -R nof0:nof0 /app && \
    chmod +x /app/start.sh

USER nof0

EXPOSE 8888

ENV NOF0_API_BIN=./nof0-api \
    NOF0_PREFLIGHT_BIN=./nof0-preflight

ENTRYPOINT ["./start.sh"]
