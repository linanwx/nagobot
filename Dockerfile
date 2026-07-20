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
#   - node + npm: third-party skills and agent-written JS tooling
#   - ripgrep/zip/sqlite3: search, attachments, light storage
#
# Deliberately NOT installed: torch/transformers (no local inference — embedding
# is remote), playwright's chromium (~800MB, a runtime third-party-skill-setup
# concern), ffmpeg (transcription is provider-side via audio-capable models;
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
RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates tzdata curl git jq \
    poppler-utils ripgrep zip unzip sqlite3 nodejs npm \
    && rm -rf /var/lib/apt/lists/*
RUN pip install --no-cache-dir \
    requests pyyaml beautifulsoup4 lxml pandas openpyxl python-docx pypdf \
    pillow markdown
COPY --from=build /nagobot /usr/local/bin/nagobot
COPY docker-entrypoint.sh /usr/local/bin/docker-entrypoint.sh
RUN chmod +x /usr/local/bin/docker-entrypoint.sh

# Marks the container environment; `nagobot update` refuses to self-update
# when set — updating a container means pulling a new image.
ENV NAGOBOT_CONTAINER=1

# Runtime installs must survive image upgrades: anything the agent adds with
# `pip install` / `npm install -g` lands under the data mount, not in the
# container layer that the next `docker compose pull` throws away.
ENV PYTHONUSERBASE=/root/.nagobot/runtime/python \
    PIP_USER=1 \
    NPM_CONFIG_PREFIX=/root/.nagobot/runtime/npm \
    PATH=/root/.nagobot/runtime/python/bin:/root/.nagobot/runtime/npm/bin:$PATH

# config.yaml + workspace (sessions, memory, skills) all live here.
VOLUME /root/.nagobot
EXPOSE 8080

ENTRYPOINT ["docker-entrypoint.sh"]
CMD ["serve"]
