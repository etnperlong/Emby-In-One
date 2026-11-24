FROM golang:1.23-bookworm AS builder
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates && rm -rf /var/lib/apt/lists/*
WORKDIR /src
COPY go.mod ./
COPY go.sum ./
COPY cmd ./cmd
COPY internal ./internal
COPY public ./public
ARG VERSION=dev
ARG REVISION=unknown
RUN CGO_ENABLED=0 go build -ldflags="-s -w -X main.Version=${VERSION}" -o /out/emby-in-one ./cmd/emby-in-one
# distroless has no shell: create the writable state directories here.
RUN mkdir -p /out/state/config /out/state/data

FROM gcr.io/distroless/static-debian13
ARG VERSION=dev
ARG REVISION=unknown
LABEL org.opencontainers.image.version="${VERSION}" \
      org.opencontainers.image.revision="${REVISION}"
WORKDIR /app
COPY public ./public
COPY --from=builder /out/emby-in-one ./emby-in-one
COPY --from=builder --chown=1000:1000 /out/state/ ./
# 非 root 运行 (uid 1000，与 docker-compose.yml 的 user 一致)
USER 1000:1000
EXPOSE 8096
HEALTHCHECK --interval=30s --timeout=5s --start-period=20s --retries=3 \
  CMD ["/app/emby-in-one", "--healthcheck"]
CMD ["./emby-in-one"]
