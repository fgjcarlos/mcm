# Stage 1: Build frontend
FROM node:22-alpine AS frontend
WORKDIR /app/frontend
COPY frontend/package.json frontend/package-lock.json ./
RUN npm ci --ignore-scripts
COPY frontend/ ./
RUN npm run build

# Stage 2: Build Go backend
FROM golang:1.24-alpine AS backend
RUN apk add --no-cache git
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=frontend /app/frontend/dist ./frontend/dist
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /mcm ./cmd/mcm

# Stage 3: Production image
FROM alpine:3.21
RUN apk add --no-cache ca-certificates tzdata \
    && addgroup -S mcm && adduser -S mcm -G mcm
COPY --from=backend /mcm /usr/local/bin/mcm
RUN mkdir -p /var/lib/mcm && chown mcm:mcm /var/lib/mcm
USER mcm
EXPOSE 8080
ENTRYPOINT ["mcm"]
CMD ["server", "--config", "/etc/mcm/config.yaml"]
