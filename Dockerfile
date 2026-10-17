# syntax=docker/dockerfile:1

# Both build stages run on the build machine's own architecture and cross-compile, so multi-arch
# images (amd64, arm64, arm/v7) need no emulation.

FROM --platform=$BUILDPLATFORM node:22-alpine AS ui
WORKDIR /src/frontend
COPY frontend/package.json frontend/package-lock.json ./
RUN npm ci
COPY frontend/ ./
# vite.config.ts writes the bundle to ../backend/ui/dist
RUN npm run build

FROM --platform=$BUILDPLATFORM golang:1.27-alpine AS build
ARG TARGETOS TARGETARCH TARGETVARIANT
ARG VERSION=dev
ARG COMMIT=
ARG BUILD_DATE=
WORKDIR /src/backend
COPY backend/go.mod backend/go.sum ./
RUN go mod download
COPY backend/ ./
COPY --from=ui /src/backend/ui/dist ./ui/dist
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH GOARM=${TARGETVARIANT#v} \
    go build -trimpath \
      -ldflags="-s -w -X main.version=${VERSION} -X main.commit=${COMMIT} -X main.buildDate=${BUILD_DATE}" \
      -o /out/repoman . \
 && mkdir -p /out/data /out/tmp && chmod 1777 /out/tmp

# The runtime image contains exactly: the static binary, CA certificates, an empty /data and /tmp.
# No shell, no package manager, nothing to patch.
FROM scratch
LABEL org.opencontainers.image.title="RepoMan" \
      org.opencontainers.image.description="Self-hosted artifact repository manager: Maven, npm, PyPI, Docker, Go, APT, YUM and raw files" \
      org.opencontainers.image.licenses="MIT"
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=build --chown=10001:10001 /out/data /data
COPY --from=build /out/tmp /tmp
COPY --from=build /out/repoman /usr/local/bin/repoman
USER 10001:10001
ENV RM_DATA_DIR=/data RM_ADDR=:8081
VOLUME /data
EXPOSE 8081
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 CMD ["repoman", "healthcheck"]
ENTRYPOINT ["repoman"]
CMD ["serve"]
