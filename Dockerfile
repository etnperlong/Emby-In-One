FROM golang:1.26-trixie AS builder
ARG VERSION=dev
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates && rm -rf /var/lib/apt/lists/*
WORKDIR /src
COPY go.mod ./
COPY go.sum ./
COPY cmd ./cmd
COPY internal ./internal
COPY public ./public
RUN CGO_ENABLED=0 go build -ldflags="-s -w -X main.Version=${VERSION}" -o /out/emby-in-one ./cmd/emby-in-one

FROM gcr.io/distroless/static-debian13

ARG VERSION=dev
ARG BUILD_DATE
ARG VCS_REF

LABEL org.opencontainers.image.title="emby-in-one" \
      org.opencontainers.image.description="Emby aggregation proxy" \
      org.opencontainers.image.version="${VERSION}" \
      org.opencontainers.image.created="${BUILD_DATE}" \
      org.opencontainers.image.revision="${VCS_REF}" \
      org.opencontainers.image.licenses="MIT"

WORKDIR /app
COPY public ./public
COPY --from=builder /out/emby-in-one ./emby-in-one
EXPOSE 8096
CMD ["./emby-in-one"]
