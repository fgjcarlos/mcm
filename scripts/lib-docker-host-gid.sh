#!/usr/bin/env bash
# lib-docker-host-gid.sh — shared helper for the scripts that start the
# Compose stack (issue #306).
#
# The MCM container reaches the Docker daemon through the host's socket.
# The socket is owned by a group whose GID is host-specific: the
# conventional 999 on Ubuntu-derived hosts, something else elsewhere, and
# on a CI runner the group of the runner that BUILT the image has nothing
# to do with the group of the runner that RUNS it.
#
# The dev image joins the conventional GID at build time (the
# DOCKER_HOST_GID build argument), which cannot satisfy a host with
# another GID. The Compose file therefore takes the host's socket GID as
# a runtime `group_add`, and this helper resolves it once for every
# script that starts the stack.
#
# Sourced by the e2e scripts and the quickstart smoke.

# Avoid double-sourcing.
if [ -n "${LIB_DOCKER_HOST_GID_LOADED:-}" ]; then
    return 0
fi
LIB_DOCKER_HOST_GID_LOADED=1

# resolve_docker_host_gid prints the GID of the Docker socket, or 999 when
# the socket is not where it is expected (the conventional default the
# dev image already joins).
resolve_docker_host_gid() {
    stat -c '%g' /var/run/docker.sock 2>/dev/null || echo 999
}

# export_docker_host_gid exports DOCKER_HOST_GID for the Compose
# interpolation. An explicit value from the environment always wins.
export_docker_host_gid() {
    export DOCKER_HOST_GID="${DOCKER_HOST_GID:-$(resolve_docker_host_gid)}"
}
