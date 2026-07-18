# Multi-stage build. The runtime image is debian-slim, NOT scratch/distroless:
# the exec tool is the agent's hands — it needs a real shell and basic
# utilities, or the bot loses most of its self-service ability in a container.
FROM golang:1.24 AS build
ARG VERSION=dev
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build \
    -ldflags="-s -w -X github.com/linanwx/nagobot/cmd.Version=${VERSION}" \
    -o /nagobot .

FROM debian:stable-slim
RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates tzdata curl git jq \
    && rm -rf /var/lib/apt/lists/*
COPY --from=build /nagobot /usr/local/bin/nagobot
COPY docker-entrypoint.sh /usr/local/bin/docker-entrypoint.sh
RUN chmod +x /usr/local/bin/docker-entrypoint.sh

# Marks the container environment; `nagobot update` refuses to self-update
# when set — updating a container means pulling a new image.
ENV NAGOBOT_CONTAINER=1

# config.yaml + workspace (sessions, memory, skills) all live here.
VOLUME /root/.nagobot
EXPOSE 8080

ENTRYPOINT ["docker-entrypoint.sh"]
CMD ["serve"]
