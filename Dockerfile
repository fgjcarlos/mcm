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

# Build info injected via -ldflags so `mcm --version` reports the actual
# commit and timestamp instead of the dev defaults baked into main.go.
# Plain `docker buildx build` (no --build-arg) produces the dev banner.
ARG VERSION=dev
ARG COMMIT=none
ARG BUILD_DATE=unknown

COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=frontend /app/frontend/dist ./frontend/dist
RUN CGO_ENABLED=0 go build -trimpath \
    -ldflags="-s -w -X main.version=${VERSION} -X main.commit=${COMMIT} -X main.buildDate=${BUILD_DATE}" \
    -o /mcm ./cmd/mcm

# Stage 3: Production image (default target)
#
# The production image is intentionally minimal: no docker-cli, no
# fixed GID 997, no socket-mounting helpers. It runs the mcm binary
# as a non-root user and exposes /livez. Production deployments that
# want the deploy service to drive Mosquitto via `docker exec` must
# either:
#   - mount the host docker socket AND the docker CLI at the
#     orchestrator level (Compose `group_add: <host-gid>` +
#     `volumes: - /var/run/docker.sock:/var/run/docker.sock`); or
#   - use the `dev` build target below (Dockerfile --target=dev).
#
# The two main tree paths to the production-ready image are:
#   docker build -t mcm:dev .                  # → dev target
#   docker build --target prod -t mcm:latest . # → production target
FROM alpine:3.21 AS prod
RUN apk add --no-cache ca-certificates tzdata wget
RUN addgroup -S mcm && adduser -S mcm -G mcm
COPY --from=backend /mcm /usr/local/bin/mcm
RUN mkdir -p /var/lib/mcm && chown mcm:mcm /var/lib/mcm
USER mcm
EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=10s --start-period=15s --retries=3 \
    CMD wget -qO- http://127.0.0.1:8080/livez || exit 1
ENTRYPOINT ["mcm"]

# Stage 4: Dev image (Dockerfile.dev target)
#
# Layers docker-cli on top of the production image so the MCM deploy
# service can run `docker exec … kill -HUP 1` against the bundled
# Mosquitto. The `docker` group is created at the host GID
# (DOCKER_HOST_GID, default 999 — the conventional host socket GID
# on the most common Linux distros). If the base image already has a
# group at that GID (e.g. alpine's `ping` at 999), we install the
# `shadow` package to get `groupmod` and rename the existing group to
# `docker` so the mcm user can join it. Operators whose host docker
# socket has a different GID override at build time:
#
#   docker build --target dev --build-arg DOCKER_HOST_GID=997 \
#     -t mcm:dev .
#
# The host's /var/run/docker.sock must be bind-mounted into the
# container for the deploy service to actually talk to the daemon.
# Operators that don't need docker-cli (e.g. production behind an
# orchestrator) should build the `prod` target instead — no docker-cli,
# no fixed GID, smaller surface.
FROM prod AS dev
USER root
ARG DOCKER_HOST_GID=999
RUN apk add --no-cache docker-cli shadow \
    && existing=$(awk -F: -v gid="${DOCKER_HOST_GID}" \
        '$3 == gid {print $1; exit}' /etc/group) \
    && if [ -n "${existing}" ] && [ "${existing}" != "docker" ]; then \
         echo "docker-cli: reusing existing group '${existing}' at GID ${DOCKER_HOST_GID}"; \
         groupmod -n docker "${existing}"; \
       elif [ -z "${existing}" ]; then \
         addgroup -S -g "${DOCKER_HOST_GID}" docker; \
       fi \
    && addgroup mcm docker \
    && apk del shadow
USER mcm
