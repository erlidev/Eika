# The default workspace image. Agents run every command in a container built
# from this image, as the non-root user `eika`, with the repository checked out
# at /workspace.
#
# eikad is baked in here, but the harness also copies its own build of the
# binary into every container before starting it, so a custom image that does
# not carry eikad works just as well. The build context is the repository root:
# `docker build -f sandbox/Dockerfile .`, which `make sandbox` does.
FROM golang:1.26-bookworm AS eikad
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd/ cmd/
COPY internal/ internal/
# Static, so the binary runs in any sandbox image.
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o /out/eikad ./cmd/eikad

FROM ubuntu:24.04

ARG NODE_VERSION=22.23.1
ARG GO_VERSION=1.26.8
ARG TARGETARCH=amd64

ENV DEBIAN_FRONTEND=noninteractive
ENV LANG=C.UTF-8

RUN apt-get update && \
    apt-get install -y --no-install-recommends \
        build-essential \
        ca-certificates \
        curl \
        git \
        jq \
        less \
        openssh-client \
        python3 \
        python3-pip \
        python3-venv \
        ripgrep \
        tini \
        unzip \
        xz-utils && \
    rm -rf /var/lib/apt/lists/*

# Node, from the official tarball so the version is pinned.
RUN case "${TARGETARCH}" in \
        amd64) node_arch=x64 ;; \
        arm64) node_arch=arm64 ;; \
        *) echo "unsupported architecture: ${TARGETARCH}" >&2; exit 1 ;; \
    esac && \
    curl -fsSL "https://nodejs.org/dist/v${NODE_VERSION}/node-v${NODE_VERSION}-linux-${node_arch}.tar.xz" \
        -o /tmp/node.tar.xz && \
    tar -xJf /tmp/node.tar.xz -C /usr/local --strip-components=1 \
        --exclude CHANGELOG.md --exclude LICENSE --exclude README.md && \
    rm /tmp/node.tar.xz && \
    node --version && npm --version

# Go, from the official tarball.
RUN curl -fsSL "https://go.dev/dl/go${GO_VERSION}.linux-${TARGETARCH}.tar.gz" -o /tmp/go.tar.gz && \
    tar -xzf /tmp/go.tar.gz -C /usr/local && \
    rm /tmp/go.tar.gz
ENV PATH=/usr/local/go/bin:/home/eika/go/bin:${PATH}

# Ubuntu 24.04 ships an `ubuntu` user on uid 1000; rename it to eika so the
# workspace volume is owned by a predictable, non-root user.
RUN usermod -l eika -d /home/eika -m ubuntu && \
    groupmod -n eika ubuntu && \
    mkdir -p /workspace /home/eika/go && \
    chown -R eika:eika /workspace /home/eika

COPY --from=eikad /out/eikad /usr/local/bin/eikad

USER eika
WORKDIR /workspace
ENV GOPATH=/home/eika/go
EXPOSE 7000

# The harness overrides this entrypoint with the same command plus its flags;
# it is spelled out here so the image is usable on its own.
ENTRYPOINT ["/usr/bin/tini", "--", "/usr/local/bin/eikad"]
CMD ["-listen", ":7000", "-root", "/workspace"]
