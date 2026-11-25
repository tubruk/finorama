# Stage 1: Build the React frontend
FROM oven/bun:1-alpine AS frontend-builder
WORKDIR /app/web

# Copy web dependency manifests
COPY web/package.json web/bun.lock* ./

# Install frontend dependencies
RUN bun install --frozen-lockfile || bun install

# Copy web source code
COPY web/ ./

# Build frontend SPA
RUN bun run build

# Stage 2: Build the Go backend
FROM golang:1.27-alpine AS backend-builder
WORKDIR /app

# Copy Go dependency manifests
COPY go.mod go.sum ./
RUN go mod download

# Copy backend source code and embedded assets
COPY cmd/ ./cmd/
COPY internal/ ./internal/
COPY main.go ./main.go
COPY web/embed.go ./web/embed.go

# Copy built frontend assets from frontend-builder
COPY --from=frontend-builder /app/web/dist ./web/dist

# Build Go binary
ARG VERSION=dev
ARG COMMIT=unknown
ARG BUILD_TIME=unknown

RUN CGO_ENABLED=0 go build \
    -ldflags "-s -w -X main.version=${VERSION} -X main.commit=${COMMIT} -X main.buildTime=${BUILD_TIME}" \
    -o /app/bin/finorama \
    .

# Stage 3: Runner
FROM alpine:3.21 AS runner
RUN apk add --no-cache ca-certificates tzdata curl

WORKDIR /app

# Copy binary from builder
COPY --from=backend-builder /app/bin/finorama /usr/local/bin/finorama

ENV FINORAMA_DATA_DIR=/data
ENV FINORAMA_PORT=8080

RUN mkdir -p /data

EXPOSE 8080

# Health check verifies API responsiveness
HEALTHCHECK --interval=30s --timeout=5s --retries=3 CMD curl -f http://localhost:${FINORAMA_PORT}/api/v1/vault/status || exit 1

ENTRYPOINT ["/usr/local/bin/finorama", "serve", "-host", "0.0.0.0", "-data-dir", "/data", "-port", "8080"]
