FROM golang:1.23-bookworm AS builder
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
WORKDIR /app
COPY public ./public
COPY --from=builder /out/emby-in-one ./emby-in-one
EXPOSE 8096
CMD ["./emby-in-one"]
