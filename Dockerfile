# Multi-stage build. The runtime image is python:slim, NOT scratch/distroless
# or bare debian: the exec tool is the agent's hands — whatever is installed
# here is the agent's capability ceiling inside a container. The toolset below
# is curated against actual nagobot features, not a kitchen sink:
#
#   - python3 + document/data libs: workspace scripts (mail.py etc.) and the
#     bidding workflows read docx/xlsx/pdf and crunch tables
#   - poppler-utils: read_file's PDF guidance says "exec pdftotext/pdftoppm" —
#     without poppler that path is dead in a container (native PDF was removed
#     in v1.6.6)
#   - node + playwright-cli + chromium: browser automation is a MAIN path, not
#     an edge case — prethink tells the model to reach for playwright on every
#     message carrying a URL. See the browser section below for why these are
#     baked in rather than installed at runtime.
#   - ripgrep/zip/sqlite3: search, attachments, light storage
#   - fonts-noto-cjk: without it headless chromium renders every CJK glyph as a
#     box. Screenshots of Chinese pages are the common case here, so the font is
#     as load-bearing as the browser itself.
#
# Deliberately NOT installed: torch/transformers (no local inference — embedding
# is remote), ffmpeg (transcription is provider-side via audio-capable models;
# add it only if media manipulation demand shows up).
# --platform=$BUILDPLATFORM: the Go build always runs on the runner's native
# arch and CROSS-compiles to $TARGETARCH. Without this, the arm64 leg of the
# multi-arch build compiles Go under QEMU emulation — by far the slowest step
# of the release pipeline. Only the runtime stage below (apt/pip layers, which
# are source-independent and gha-cached) runs emulated.
FROM --platform=$BUILDPLATFORM golang:1.25 AS build
ARG VERSION=dev
ARG TARGETOS TARGETARCH
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build \
    -ldflags="-s -w -X github.com/linanwx/nagobot/cmd.Version=${VERSION}" \
    -o /nagobot .

FROM python:3.12-slim-bookworm
ARG TARGETARCH
RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates tzdata curl git jq \
    poppler-utils ripgrep zip unzip xz-utils sqlite3 \
    fonts-noto-cjk \
    && rm -rf /var/lib/apt/lists/*
RUN pip install --no-cache-dir \
    requests pyyaml beautifulsoup4 lxml pandas openpyxl python-docx pypdf \
    pillow markdown

# Node from nodejs.org, NOT from apt. Debian bookworm ships node 18 and always
# will, while playwright-core has required node >=20 since 1.62 — so the apt
# package cannot run the browser tooling at all, no matter when the image is
# rebuilt. This was not theoretical: the deployed bot hit exactly that wall and
# hand-installed its own node into the data volume to get around it.
# Installing into /usr/local also means PATH resolves it ahead of any leftover
# /usr/bin/node, so there is one node in the image and no version roulette.
ENV NODE_VERSION=22.23.2
RUN set -eux; \
    case "$TARGETARCH" in \
      amd64) NODE_ARCH=x64 ;; \
      arm64) NODE_ARCH=arm64 ;; \
      *) echo "unsupported TARGETARCH: $TARGETARCH" >&2; exit 1 ;; \
    esac; \
    curl -fsSL "https://nodejs.org/dist/v${NODE_VERSION}/node-v${NODE_VERSION}-linux-${NODE_ARCH}.tar.xz" -o /tmp/node.tar.xz; \
    tar -xJf /tmp/node.tar.xz -C /usr/local --strip-components=1 --no-same-owner; \
    rm /tmp/node.tar.xz; \
    node --version; \
    npm --version

# Browser stack, baked into the image layer on purpose.
#
# Two constraints make the placement load-bearing, and both are easy to get
# wrong:
#
#   1. This RUN must stay ABOVE the NPM_CONFIG_PREFIX block below. That prefix
#      points into the data volume, so installing after it would put the CLI at
#      a path the runtime bind mount hides — the package would vanish the first
#      time a real deployment started.
#   2. PLAYWRIGHT_BROWSERS_PATH is pinned to an image path. The default is
#      ~/.cache, which is the container's writable layer: the browser survives
#      restarts but NOT a `docker compose pull`. That is precisely how a
#      deployed bot ended up re-downloading ~350MB of chromium into a layer
#      that the next image upgrade discards.
#
# The CLI version is pinned rather than tracking @latest because a playwright
# release binds to one exact chromium build (1.63 -> chromium-1237). An
# unpinned upgrade would leave the image's browser unusable and silently pull a
# new one at runtime, back into the throwaway layer. third-party-skill-setup
# therefore tells the agent to USE what is here, not to install or upgrade it.
ENV PLAYWRIGHT_CLI_VERSION=0.1.18 \
    PLAYWRIGHT_BROWSERS_PATH=/usr/local/ms-playwright
RUN set -eux; \
    npm install -g "@playwright/cli@${PLAYWRIGHT_CLI_VERSION}"; \
    apt-get update; \
    playwright-cli install-browser --with-deps chromium; \
    rm -rf /var/lib/apt/lists/* /root/.npm; \
    playwright-cli --version

# Without this the image installs a browser it then refuses to use: with no
# config, playwright-cli defaults to channel "chrome" and dies with "Chromium
# distribution 'chrome' is not found at /opt/google/chrome/chrome" — it looks
# for the SYSTEM Google Chrome, which no container has. It lives at the global
# path (~/.playwright) rather than a project's .playwright/ so it applies from
# any working directory the agent happens to run in.
#
# `chromiumSandbox: false` is what makes it work as root, and it is deliberately
# NOT expressed as `channel: "chromium"` even though upstream's own installer
# writes it that way. Both disable the sandbox, but the channel form also pins
# execution to the FULL chromium binary, and measured in this image that costs
# 777MB resident versus 394MB for the headless shell that playwright picks on
# its own. The deployment runs three bots on 3.7GB of RAM, so that difference
# decides whether two of them can hold a browser open at the same time.
# Full chromium stays installed for the headed/extension paths; it is simply
# not what a default headless run pays for.
RUN mkdir -p /root/.playwright && printf '%s\n' \
    '{' \
    '  "browser": {' \
    '    "browserName": "chromium",' \
    '    "launchOptions": { "chromiumSandbox": false }' \
    '  }' \
    '}' > /root/.playwright/cli.config.json
COPY --from=build /nagobot /usr/local/bin/nagobot
COPY docker-entrypoint.sh /usr/local/bin/docker-entrypoint.sh
RUN chmod +x /usr/local/bin/docker-entrypoint.sh

# Marks the container environment; `nagobot update` refuses to self-update
# when set — updating a container means pulling a new image.
ENV NAGOBOT_CONTAINER=1

# Runtime installs must survive image upgrades: anything the agent adds with
# `pip install` / `npm install -g` lands under the data mount, not in the
# container layer that the next `docker compose pull` throws away.
#
# Note the PATH order: the data mount comes FIRST, so an agent-installed
# package shadows the image's copy of the same tool. That is the intended
# behaviour for tools the agent adds itself, but it means a stray
# `npm install -g @playwright/cli` at runtime silently overrides the pinned CLI
# above and pairs it with the image's older chromium. Deployments upgrading
# into this image should clear any pre-existing runtime/ tree once.
ENV PYTHONUSERBASE=/root/.nagobot/runtime/python \
    PIP_USER=1 \
    NPM_CONFIG_PREFIX=/root/.nagobot/runtime/npm \
    PATH=/root/.nagobot/runtime/python/bin:/root/.nagobot/runtime/npm/bin:$PATH

# config.yaml + workspace (sessions, memory, skills) all live here.
VOLUME /root/.nagobot
EXPOSE 8080

ENTRYPOINT ["docker-entrypoint.sh"]
CMD ["serve"]
