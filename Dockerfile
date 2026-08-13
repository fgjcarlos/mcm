# Stage 1: Build frontend
FROM node:22-alpine AS frontend
WORKDIR /app/frontend
COPY frontend/package.json frontend/package-lock.json ./
RUN npm ci --ignore-scripts
COPY frontend/ ./
RUN npm run build

# Stage 2: Build Go backend
FROM golang:1.25-alpine AS backend
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=frontend /app/frontend/dist ./frontend/dist
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /mcm ./cmd/mcm

# Stage 3: Production image
FROM alpine:3.21
RUN apk add --no-cache ca-certificates tzdata wget docker-cli \
    # Create the `docker` group with GID 997 — matching the conventional
    # host socket GID on Ubuntu-derived systems. The actual host GID
    # can vary; if it differs the socket will be unreadable and the
    # deploy apply will fail. Adjust this GID or replace it with a
    # chmod/chown entrypoint wrapper if your host uses a different
    # one. Adding mcm to it lets the in-container mcm process talk to
    # the bind-mounted /var/run/docker.sock. This is dev/CI-only
    # convenience: production does NOT mount the docker socket into MCM.
    && addgroup -S -g 997 docker \
    && addgroup -S mcm && adduser -S mcm -G mcm \
    && addgroup mcm docker
COPY --from=backend /mcm /usr/local/bin/mcm
RUN mkdir -p /var/lib/mcm && chown mcm:mcm /var/lib/mcm
USER mcm
EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=10s --start-period=15s --retries=3 \
    CMD wget -qO- http://127.0.0.1:8080/livez || exit 1
ENTRYPOINT ["mcm"]
