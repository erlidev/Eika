# Builds the Eika harness image: the eika and eikad binaries plus the frontend.
FROM node:22-bookworm-slim AS web
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web/ ./
RUN npm run build

FROM golang:1.26-bookworm AS go
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd/ cmd/
COPY internal/ internal/
# eikad is injected into sandboxes as a static binary, so it is built static.
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o /out/eika ./cmd/eika && \
    CGO_ENABLED=0 go build -ldflags="-s -w" -o /out/eikad ./cmd/eikad

FROM debian:bookworm-slim

# Group id of /var/run/docker.sock on the host. It varies by distribution, so
# it is a build arg: `stat -c %g /var/run/docker.sock` on the host, passed as
# DOCKER_GID. Membership in that group is root-equivalent on the host; the
# harness needs it to run sandboxes as sibling containers.
ARG DOCKER_GID=999

RUN apt-get update && \
    apt-get install -y --no-install-recommends ca-certificates curl git && \
    rm -rf /var/lib/apt/lists/*
RUN groupadd --system --gid 1000 eika && \
    useradd --system --uid 1000 --gid eika --home-dir /var/lib/eika --create-home eika && \
    (getent group "${DOCKER_GID}" || groupadd --gid "${DOCKER_GID}" docker) && \
    usermod --append --groups "$(getent group "${DOCKER_GID}" | cut -d: -f1)" eika
WORKDIR /app
COPY --from=go /out/eika /usr/local/bin/eika
COPY --from=go /out/eikad /usr/local/share/eika/eikad
COPY --from=web /src/web/dist /app/web
COPY deploy/eika.yaml /app/eika.yaml
USER eika
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/eika"]
CMD ["-config", "/app/eika.yaml", "-web", "/app/web"]
