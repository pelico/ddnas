# syntax=docker/dockerfile:1
#
# 修复 armv7l "exec format error" 的关键：
# 构建阶段固定运行在 BUILDPLATFORM（CI 运行机原生 amd64），通过 GOOS/GOARCH/GOARM
# 让 Go 原生交叉编译出目标架构的二进制 —— 完全不依赖 QEMU，也不依赖多架构在
# 同一次 buildx 调用里的共享缓存，从而避免之前 arm/v7 镜像里被装进 amd64 二进制的坑。
#
# 本地单架构验证：  docker buildx build --platform linux/arm/v7 -t ddnas:armv7 .
# CI 用矩阵为每个架构单独调用一次 buildx（见 .github/workflows/docker.yml）。

FROM --platform=$BUILDPLATFORM golang:1.25-alpine AS build
ARG TARGETOS=linux
ARG TARGETARCH=amd64
ARG TARGETVARIANT=
ENV CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH}
WORKDIR /src
COPY go.mod go.sum* ./
RUN go mod download
COPY . .
# TARGETVARIANT 形如 "v7"，剥掉前缀得到 GOARM=7；非 arm 架构不设 GOARM。
RUN set -eux; \
    case "${TARGETARCH}" in \
      arm) export GOARM="${TARGETVARIANT#v}"; [ -n "$GOARM" ] || export GOARM=7 ;; \
    esac; \
    go build -ldflags="-s -w" -trimpath -o /out/ddnas ./cmd/ddnas

FROM alpine:3.20
RUN apk add --no-cache ca-certificates tzdata && \
    adduser -D -u 1000 ddnas && mkdir -p /data && chown -R ddnas:ddnas /data
WORKDIR /app
COPY --from=build /out/ddnas /app/ddnas
USER ddnas
ENV DDNAS_DATA_DIR=/data DDNAS_ADDR=:8080 TZ=Asia/Shanghai
EXPOSE 8080
VOLUME ["/data"]
ENTRYPOINT ["/app/ddnas"]
