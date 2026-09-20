# No third-party Go modules. CGO links to Debian's SQLite library.
ARG GO_VERSION=1.27.1
FROM golang:${GO_VERSION}-bookworm AS build
ARG GOPROXY=https://goproxy.cn,direct
ENV GOPROXY=${GOPROXY}
RUN apt-get update && apt-get install -y --no-install-recommends gcc libc6-dev libsqlite3-dev \
    && rm -rf /var/lib/apt/lists/*
WORKDIR /src
COPY . .
RUN CGO_ENABLED=1 go test ./... \
    && CGO_ENABLED=1 go build -trimpath -ldflags="-s -w" -o /out/rift-ledger-go ./cmd/server

FROM debian:bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends libsqlite3-0 sqlite3 ca-certificates tzdata curl fonts-noto-cjk \
    && rm -rf /var/lib/apt/lists/* \
    && groupadd --gid 10001 riftledger \
    && useradd --uid 10001 --gid 10001 --no-create-home --shell /usr/sbin/nologin riftledger \
    && install -d -o 10001 -g 10001 -m 0700 /data
COPY --from=build /out/rift-ledger-go /usr/local/bin/rift-ledger-go
USER 10001:10001
WORKDIR /data
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/rift-ledger-go"]
