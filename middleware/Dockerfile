# 语法: docker buildx build --platform linux/amd64,linux/arm64,linux/arm/v7 -t ddnas .
# CGO 禁用产出静态二进制，可在 alpine/musl 与 armv7l 等环境通用运行。
FROM golang:1.25-alpine AS build
WORKDIR /src
COPY go.mod go.sum* ./
RUN go mod download
COPY . .
ARG TARGETOS=linux
ARG TARGETARCH=amd64
ARG TARGETVARIANT=
ENV CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH}
RUN set -eux; \
    if [ "$TARGETARCH" = "arm" ]; then export GOARM=7; fi; \
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
