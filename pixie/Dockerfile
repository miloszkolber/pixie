# syntax=docker/dockerfile:1.7

ARG BUN_IMAGE=oven/bun:1.4.0-debian@sha256:5bb0f9be3a1a36a03e27c9a9dd894a3b1ad26657155c7df4dda771e17bf872ef
ARG GO_IMAGE=golang:1.27.1-trixie@sha256:9baa6b4187bbb98d240372a8a235ac0bb6b5ddd52bba1431dc2f7c0705862728
ARG GIT_IMAGE=cgr.dev/chainguard/git:latest@sha256:d57114522a99da01911b9c6702ef6c4a18bd19f79ccd49a780e78303b1d36640
ARG DEBIAN_IMAGE=debian:trixie-slim@sha256:d7e12182ce18b85b93007c1dedf31f2d29e01ccf3182cc4017c709b6259bc132
ARG AGENT_BROWSER_VERSION=0.34.0
ARG VERSION=0.0.0-dev
ARG REVISION=unknown
ARG SOURCE_URL=https://github.com/miloszkolber/pixie
ARG CREATED=1970-01-01T00:00:00Z

FROM --platform=$BUILDPLATFORM ${BUN_IMAGE} AS web-build
WORKDIR /work
COPY pixie/package.json pixie/bun.lock pixie/bunfig.toml ./
COPY pixie/webui/package.json webui/package.json
COPY pixie/contracts/package.json contracts/package.json
COPY pixie/pi-host/package.json pi-host/package.json
COPY pixie/pi-mcp/package.json pi-mcp/package.json
RUN --mount=type=cache,id=bun-install-cache,target=/root/.bun/install/cache,sharing=locked \
    bun install --frozen-lockfile
COPY pixie/webui/ webui/
COPY pixie/contracts/ contracts/
COPY pixie/tsconfig.base.json ./
RUN bun run build:web \
    && mkdir -p /out/licenses/frontend \
    && find node_modules -type f \( -iname 'license*' -o -iname 'notice*' \) \
        -exec cp --parents '{}' /out/licenses/frontend/ \; \
    && mkdir -p /out/licenses/frontend/vendor/mewa-ui \
        /out/licenses/frontend/vendor/mewa-svelte \
        /out/licenses/frontend/vendor/mewa-icons \
    && cp webui/vendor/mewa-ui/LICENSE /out/licenses/frontend/vendor/mewa-ui/LICENSE \
    && cp webui/vendor/mewa-ui/licenses/GEIST-OFL.txt \
        /out/licenses/frontend/vendor/mewa-ui/GEIST-OFL.txt \
    && cp webui/vendor/mewa-svelte/LICENSE /out/licenses/frontend/vendor/mewa-svelte/LICENSE \
    && cp webui/vendor/mewa-icons/LICENSE /out/licenses/frontend/vendor/mewa-icons/LICENSE \
    && cp webui/vendor/mewa-icons/licenses/LUCIDE-LICENSE.txt \
        /out/licenses/frontend/vendor/mewa-icons/LUCIDE-LICENSE.txt \
    && cp webui/vendor/mewa.lock.json /out/licenses/frontend/vendor/mewa.lock.json

FROM --platform=$BUILDPLATFORM ${GO_IMAGE} AS go-source
WORKDIR /work
COPY pixie/go.mod pixie/go.sum ./
RUN --mount=type=cache,id=go-module-cache,target=/go/pkg/mod,sharing=locked \
    go mod download
COPY pixie/cmd/ cmd/
COPY pixie/internal/ internal/

FROM go-source AS go-build
ARG TARGETOS
ARG TARGETARCH
ARG VERSION
ARG REVISION
RUN --mount=type=cache,id=go-module-cache,target=/go/pkg/mod,sharing=locked \
    --mount=type=cache,id=go-build-cache,target=/root/.cache/go-build,sharing=locked \
    export CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH; \
    set -eu; \
    mkdir -p /out; \
    for entry in pixie:./cmd/pixie pixie-mcp:./cmd/pixie-mcp; do \
        binary=${entry%%:*}; package=${entry#*:}; \
        go build -trimpath -ldflags="-s -w -X main.version=$VERSION -X main.revision=$REVISION" -o "/out/$binary" "$package"; \
        mkdir -p "/out/licenses/$binary/go"; \
        cp /usr/local/go/LICENSE "/out/licenses/$binary/go/LICENSE"; \
        go list -deps -f '{{with .Module}}{{if .Version}}{{.Path}}@{{.Version}} {{.Dir}}{{end}}{{end}}' "$package" \
            > /tmp/modules.unsorted; \
        sort -u /tmp/modules.unsorted > /tmp/modules; \
        cut -d ' ' -f1 /tmp/modules > "/out/licenses/$binary/modules.txt"; \
        while read -r module directory; do \
            test -n "$module" || continue; \
            mkdir -p "/out/licenses/$binary/$module"; \
            find "$directory" -maxdepth 1 -type f \( -iname 'license*' -o -iname 'notice*' \) \
                -exec cp '{}' "/out/licenses/$binary/$module/" \;; \
        done < /tmp/modules; \
    done

FROM go-source AS ui-test-build
COPY pixie/tests/ui/fixture/ tests/ui/fixture/
ARG TARGETOS
ARG TARGETARCH
RUN --mount=type=cache,id=go-module-cache,target=/go/pkg/mod,sharing=locked \
    --mount=type=cache,id=go-build-cache,target=/root/.cache/go-build,sharing=locked \
    CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags='-s -w' -o /out/pixie-ui-fixture ./tests/ui/fixture

# Preserve Chainguard Git's dependency closure, but remove the shells and local
# server entry points that Pixie does not use.
FROM ${GIT_IMAGE} AS git-runtime-source

FROM ${DEBIAN_IMAGE} AS git-runtime-trim
COPY --from=git-runtime-source / /rootfs/
COPY pixie/tests/deployment/git-runtime.test.sh /usr/local/bin/verify-git-runtime
RUN rm -f \
        /rootfs/bin/sh /rootfs/usr/bin/sh \
        /rootfs/bin/dash /rootfs/usr/bin/dash \
        /rootfs/usr/bin/git-receive-pack \
        /rootfs/usr/bin/git-upload-archive \
        /rootfs/usr/bin/git-upload-pack \
        /rootfs/usr/libexec/git-core/git-receive-pack \
        /rootfs/usr/libexec/git-core/git-upload-archive \
        /rootfs/usr/libexec/git-core/git-upload-pack \
    && for executable in \
        /bin/sh /usr/bin/sh /bin/dash /usr/bin/dash \
        /bin/bash /usr/bin/bash /bin/ash /usr/bin/ash \
        /bin/busybox /usr/bin/busybox \
        /sbin/apk /usr/sbin/apk /usr/bin/apk \
        /usr/bin/apt /usr/bin/apt-get /usr/bin/dpkg \
        /usr/bin/dnf /usr/bin/microdnf /usr/bin/rpm /usr/bin/yum \
        /usr/bin/git-shell /usr/libexec/git-core/git-shell \
        /usr/bin/git-receive-pack /usr/libexec/git-core/git-receive-pack \
        /usr/bin/git-upload-archive /usr/libexec/git-core/git-upload-archive \
        /usr/bin/git-upload-pack /usr/libexec/git-core/git-upload-pack; do \
        test ! -e "/rootfs$executable"; \
    done \
    && install -d -m 0755 -o 1000 -g 1000 /rootfs/app /rootfs/home/pixie /rootfs/var/lib/pixie \
    && install -d -m 1777 -o 0 -g 0 /rootfs/tmp \
    && /usr/local/bin/verify-git-runtime /rootfs

FROM ${DEBIAN_IMAGE} AS agent-browser-fetch
ARG AGENT_BROWSER_VERSION
ARG TARGETARCH
RUN --mount=type=cache,id=browser-fetch-apt,target=/var/cache/apt,sharing=locked \
    --mount=type=cache,id=browser-fetch-apt-lists,target=/var/lib/apt/lists,sharing=locked \
    apt-get update \
    && DEBIAN_FRONTEND=noninteractive apt-get install --no-install-recommends -y ca-certificates curl \
    && case "${AGENT_BROWSER_VERSION}:${TARGETARCH}" in \
        0.34.0:amd64) agent_arch=x64; agent_sha256=69eadf5d8d6003a06a5cd2f914ebb261c7754fe1335a9190122c334e91909789 ;; \
        0.34.0:arm64) agent_arch=arm64; agent_sha256=ca70bf7c2d269a152b3824cbb65befb7b8258b8aa1cf34767c64ada2abc3d7c8 ;; \
        0.34.0:) echo "TARGETARCH must be set to amd64 or arm64" >&2; exit 1 ;; \
        *) echo "unsupported agent-browser version or architecture: ${AGENT_BROWSER_VERSION}:${TARGETARCH}" >&2; exit 1 ;; \
    esac \
    && install -d /out \
    && curl --fail --location --retry 3 \
        --output /out/agent-browser \
        "https://github.com/vercel-labs/agent-browser/releases/download/v${AGENT_BROWSER_VERSION}/agent-browser-linux-${agent_arch}" \
    && printf '%s  %s\n' "${agent_sha256}" /out/agent-browser | sha256sum --check --status - \
    && chmod 0755 /out/agent-browser \
    && curl --fail --location --retry 3 \
        --output /out/LICENSE \
        "https://raw.githubusercontent.com/vercel-labs/agent-browser/v${AGENT_BROWSER_VERSION}/LICENSE"

FROM ${DEBIAN_IMAGE} AS browser-packages
RUN --mount=type=cache,id=browser-runtime-apt,target=/var/cache/apt,sharing=locked \
    --mount=type=cache,id=browser-runtime-apt-lists,target=/var/lib/apt/lists,sharing=locked \
    apt-get update \
    && DEBIAN_FRONTEND=noninteractive apt-get install --no-install-recommends -y \
        ca-certificates \
        chromium \
        fonts-liberation \
        fonts-noto-cjk \
        fonts-noto-color-emoji \
        tini \
    && install -d /app/legal /home/pixie \
        /var/lib/pixie-browser/artifacts /var/lib/pixie-browser/state \
    && chown -R 1000:1000 /app /home/pixie /var/lib/pixie-browser
COPY LICENSE NOTICE.md /app/legal/
ENV HOME=/home/pixie
WORKDIR /app
ENTRYPOINT ["/usr/bin/tini", "-s", "--"]

FROM browser-packages AS browser-automation
COPY --from=agent-browser-fetch /out/agent-browser /usr/local/libexec/agent-browser
COPY --from=agent-browser-fetch /out/LICENSE /app/licenses/agent-browser/LICENSE
RUN printf '%s\n' \
        '#!/bin/sh' \
        'export PATH=/usr/local/bin:/usr/bin:/bin' \
        'export AGENT_BROWSER_EXECUTABLE_PATH=/usr/bin/chromium' \
        'exec /usr/local/libexec/agent-browser "$@"' \
        > /usr/local/bin/agent-browser \
    && chmod 0755 /usr/local/bin/agent-browser \
    && test -x /usr/local/libexec/agent-browser \
    && test -s /app/licenses/agent-browser/LICENSE

# The MCP host is the only published Browser-capable service image. It embeds
# the Browser module and keeps its legacy HTTP/MCP compatibility routes.
FROM browser-automation AS mcp
ARG VERSION
ARG REVISION
ARG SOURCE_URL
ARG CREATED
LABEL org.opencontainers.image.title="Pixie MCP" \
    org.opencontainers.image.version="${VERSION}" \
    org.opencontainers.image.revision="${REVISION}" \
    org.opencontainers.image.source="${SOURCE_URL}" \
    org.opencontainers.image.created="${CREATED}"
COPY --from=go-build /out/pixie-mcp /app/pixie-mcp
COPY --from=go-build /out/licenses/pixie-mcp /app/licenses
COPY pixie/internal/browser/config.json /app/config.json
RUN test -x /app/pixie-mcp \
    && test -f /app/config.json \
    && ! command -v git && ! command -v bun && ! command -v node && ! command -v go
USER 1000:1000
EXPOSE 8787
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
    CMD ["/app/pixie-mcp", "healthcheck"]
CMD ["/app/pixie-mcp"]

FROM browser-automation AS ui-acceptance
RUN --mount=type=cache,id=ui-acceptance-apt,target=/var/cache/apt,sharing=locked \
    --mount=type=cache,id=ui-acceptance-apt-lists,target=/var/lib/apt/lists,sharing=locked \
    apt-get update \
    && DEBIAN_FRONTEND=noninteractive apt-get install --no-install-recommends -y git \
    && mkdir -p /app/web /artifacts \
    && chown -R 1000:1000 /artifacts
COPY --from=ui-test-build /out/pixie-ui-fixture /app/pixie-ui-fixture
COPY --from=web-build /work/webui/dist /app/web
COPY pixie/tests/ui/run.sh /app/run-ui-acceptance
COPY pixie/tests/ui/pi.sh /app/run-pi-acceptance
COPY pixie/tests/ui/faults.js /app/ui-faults.js
COPY pixie/tests/ui/history.sh /app/run-history-measurement
COPY pixie/tests/ui/history.js /app/ui-history.js
COPY pixie/tests/ui/history-interactions.js /app/ui-history-interactions.js
RUN chmod 0755 /app/run-ui-acceptance \
    && test -x /app/pixie-ui-fixture \
    && test -f /app/web/index.html \
    && ! command -v bun && ! command -v node && ! command -v go
USER 1000:1000
CMD ["/app/run-ui-acceptance"]

# Keep the application last: a build without --target produces only Pixie.
FROM scratch AS pixie
ARG VERSION
ARG REVISION
ARG SOURCE_URL
ARG CREATED
LABEL org.opencontainers.image.title="Pixie" \
    org.opencontainers.image.version="${VERSION}" \
    org.opencontainers.image.revision="${REVISION}" \
    org.opencontainers.image.source="${SOURCE_URL}" \
    org.opencontainers.image.created="${CREATED}"
ENV HOME=/home/pixie \
    PATH=/usr/local/bin:/usr/bin:/bin \
    SSL_CERT_FILE=/etc/ssl/certs/ca-certificates.crt \
    GOGC=200
WORKDIR /app
COPY --from=git-runtime-trim /rootfs/ /
COPY LICENSE NOTICE.md /app/legal/
COPY --from=go-build /out/pixie /app/pixie
COPY --from=go-build /out/licenses/pixie /app/licenses
COPY --from=web-build /work/webui/dist /app/web
COPY --from=web-build /out/licenses /app/licenses
USER 1000:1000
EXPOSE 7312
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
    CMD ["/app/pixie", "healthcheck"]
ENTRYPOINT ["/app/pixie"]
