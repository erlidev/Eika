#!/bin/sh
# Entry point of the harness image. It starts as root only to give the eika
# user the group that owns the Docker socket, whatever id that group has on
# this host, and then runs the harness as eika. That is what lets the same
# image work on every host without a build argument.
set -eu

socket="${EIKA_DOCKER_SOCKET:-/var/run/docker.sock}"

if [ "$(id -u)" != "0" ]; then
	exec "$@"
fi

if [ -S "$socket" ]; then
	gid="$(stat -c %g "$socket")"
	group="$(getent group "$gid" | cut -d: -f1 || true)"
	if [ -z "$group" ]; then
		group=docker-host
		groupadd --gid "$gid" "$group"
	fi
	usermod --append --groups "$group" eika
fi

# The data volume holds the hub and the key that seals stored credentials.
chown eika:eika /var/lib/eika

export HOME=/var/lib/eika USER=eika
exec setpriv --reuid=eika --regid=eika --init-groups "$@"
