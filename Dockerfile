# syntax=docker/dockerfile:1
#
# argocd-sops-cmp: Argo CD Config Management Plugin (CMP) sidecar image.
ARG TARGETOS=linux
ARG TARGETARCH=amd64

# ---- Stage 1: build the argocd-sops-cmp CMP binary ------------------------------
FROM --platform=${TARGETOS}/${TARGETARCH} golang:1.22-bookworm AS build
WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download
COPY *.go ./

RUN CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o /out/argocd-sops-cmp .

# ---- Stage 2: runtime -----------------------------------------------------
FROM --platform=${TARGETOS}/${TARGETARCH} debian:bookworm-slim

ARG HELM_VERSION=3.16.4
ARG SOPS_VERSION=3.10.2
ARG HELM_SHA256=fc307327959aa38ed8f9f7e66d45492bb022a66c3e5da6063958254b9767d179
ARG SOPS_SHA256=79b0f844237bd4b0446e4dc884dbc1765fc7dedc3968f743d5949c6f2e701739

# Argo CD runs its containers as uid/gid 999 ("argocd"). Match it so the
# sidecar is compatible with the repo-server pod securityContext.
ARG ARGOCD_UID=999
ARG ARGOCD_GID=999

SHELL ["/bin/bash", "-o", "pipefail", "-c"]

RUN apt-get update \
 && apt-get install -y --no-install-recommends \
      ca-certificates \
      curl \
      gnupg \
      gpg-agent \
 && rm -rf /var/lib/apt/lists/*

RUN curl -fsSL "https://get.helm.sh/helm-v${HELM_VERSION}-linux-amd64.tar.gz" -o /tmp/helm.tgz \
 && echo "${HELM_SHA256}  /tmp/helm.tgz" | sha256sum -c - \
 && tar -xzf /tmp/helm.tgz -C /tmp \
 && install -m 0755 /tmp/linux-amd64/helm /usr/local/bin/helm \
 && rm -rf /tmp/helm.tgz /tmp/linux-amd64

RUN curl -fsSL "https://github.com/getsops/sops/releases/download/v${SOPS_VERSION}/sops-v${SOPS_VERSION}.linux.amd64" -o /tmp/sops \
 && echo "${SOPS_SHA256}  /tmp/sops" | sha256sum -c - \
 && install -m 0755 /tmp/sops /usr/local/bin/sops \
 && rm -f /tmp/sops

COPY --from=build /out/argocd-sops-cmp /usr/local/bin/argocd-sops-cmp

RUN apt-get purge -y curl \
 && apt-get autoremove -y \
 && rm -rf /var/lib/apt/lists/*

# ---- Non-root argocd user -------------------------------------------------
RUN groupadd -g "${ARGOCD_GID}" argocd \
 && useradd -u "${ARGOCD_UID}" -g "${ARGOCD_GID}" -m -d /home/argocd -s /usr/sbin/nologin argocd \
 && mkdir -p /home/argocd/cmp-server/config /home/argocd/cmp-server/plugins \
 && chown -R "${ARGOCD_UID}:${ARGOCD_GID}" /home/argocd

ENV HOME=/home/argocd \
    HELM_CACHE_HOME=/tmp/helm/cache \
    HELM_CONFIG_HOME=/tmp/helm/config \
    HELM_DATA_HOME=/tmp/helm/data \
    XDG_CONFIG_HOME=/tmp/.config \
    XDG_CACHE_HOME=/tmp/.cache

USER ${ARGOCD_UID}
WORKDIR /home/argocd

# Sanity check that the tools resolve for the argocd user at build time.
# `argocd-sops-cmp discover` in an empty dir is a no-op that exits 0.
RUN helm version --short \
 && sops --version --disable-version-check \
 && gpg --version | head -n1 \
 && argocd-sops-cmp discover \
 && test -x /usr/lib/gnupg/gpg-preset-passphrase

