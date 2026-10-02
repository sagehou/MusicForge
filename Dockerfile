# syntax=docker/dockerfile:1
FROM --platform=$BUILDPLATFORM node:26.10.0-trixie-slim AS frontend
WORKDIR /src/web
COPY web/package*.json ./
RUN npm ci
COPY web/ ./
RUN npm run build

FROM --platform=$BUILDPLATFORM golang:1.27.1-trixie AS backend
ARG TARGETOS
ARG TARGETARCH
ARG VERSION=development
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd/ cmd/
COPY internal/ internal/
COPY web/embed.go web/embed.go
COPY --from=frontend /src/web/dist web/dist
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -mod=readonly -trimpath -ldflags="-s -w -X main.version=$VERSION" -o /musicforge ./cmd/musicforge

FROM --platform=$BUILDPLATFORM debian:trixie-slim AS media-builder
RUN apt-get update && apt-get install -y --no-install-recommends build-essential curl ca-certificates pkg-config nasm zlib1g-dev xz-utils && rm -rf /var/lib/apt/lists/*
COPY build/ /src/build/
RUN sh /src/build/media.sh

FROM scratch AS media
COPY --from=media-builder /usr/local/bin/ffmpeg /usr/local/bin/ffprobe /bin/

FROM debian:trixie-slim
LABEL org.opencontainers.image.source="https://github.com/sagehou/MusicForge" \
      org.opencontainers.image.title="MusicForge" \
      org.opencontainers.image.description="Incremental builds for a self-hosted streaming music library"
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates && rm -rf /var/lib/apt/lists/* && groupadd -g 10001 musicforge && useradd -u 10001 -g musicforge -M -s /usr/sbin/nologin musicforge && mkdir -p /config /music/source /music/output && chown -R musicforge:musicforge /config /music/output
COPY --from=media-builder /usr/local/bin/ffmpeg /usr/local/bin/ffprobe /usr/local/bin/
COPY --from=media-builder /usr/local/share/musicforge/media /usr/share/doc/musicforge/media
COPY --from=backend /musicforge /usr/local/bin/musicforge
COPY THIRD_PARTY_NOTICES.md /usr/share/doc/musicforge/THIRD_PARTY_NOTICES.md
USER 10001:10001
EXPOSE 8787
VOLUME ["/config"]
HEALTHCHECK --interval=30s --timeout=5s --start-period=15s CMD ["musicforge", "-healthcheck"]
ENTRYPOINT ["musicforge"]
