# Stage 1: Build frontend
FROM node:22-alpine AS frontend
WORKDIR /app/frontend
COPY frontend/package.json frontend/package-lock.json ./
RUN npm ci --ignore-scripts
COPY frontend/ ./
RUN npm run build

# Stage 2: Build Go backend
FROM golang:1.24-alpine AS backend
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=frontend /app/frontend/dist ./frontend/dist
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /mcm ./cmd/mcm

# Stage 3: Production image
FROM alpine:3.21
RUN apk add --no-cache ca-certificates tzdata wget \
    && addgroup -S mcm && adduser -S mcm -G mcm
COPY --from=backend /mcm /usr/local/bin/mcm
RUN mkdir -p /var/lib/mcm && chown mcm:mcm /var/lib/mcm
USER mcm
EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=10s --start-period=15s --retries=3 \
    CMD wget -qO- http://127.0.0.1:8080/healthz || exit 1
ENTRYPOINT ["mcm"]
