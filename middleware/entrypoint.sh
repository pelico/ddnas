#!/bin/sh
# DDNAS 入口：解决 bind-mount 的 /data 属主不匹配导致配置写不进去的问题。
#
# 现象：宿主用 -v /your/path:/data 挂载时，/data 属主是宿主用户（常为 root），
# 而镜像内运行用户 ddnas(uid 1000) 无权写，保存配置报
# "open /data/config.yaml.tmp: permission denied"。
#
# 做法：容器以 root 启动 -> 把 /data 递归 chown 到运行用户 -> 用 su-exec 降权
# 到该用户执行主程序。默认运行用户 ddnas(1000)；可用 PUID/PGID 覆盖以匹配 NAS 账号
# （此时新建 app 用户并以其身份运行）。
# 若 docker run --user 指定了非 root 用户，则跳过 chown 直接运行（需自保证 /data 可写）。
set -e

PUID="${PUID:-1000}"
PGID="${PGID:-1000}"

if [ "$(id -u)" = "0" ]; then
  mkdir -p /data
  if [ "$PUID" = "1000" ] && [ "$PGID" = "1000" ]; then
    # 默认：用镜像内已存在的 ddnas(1000)，无需新建用户
    RUNUSER="ddnas"
  else
    # 自定义 PUID/PGID：新建 app 用户匹配 NAS 账号；失败则回退 root（仍可写 /data）
    RUNUSER=""
    addgroup -g "$PGID" app 2>/dev/null || true
    adduser -D -u "$PUID" -G app -H app 2>/dev/null && RUNUSER="app"
    [ -n "$RUNUSER" ] || RUNUSER="root"
  fi
  chown -R "$PUID:$PGID" /data 2>/dev/null || true
  exec su-exec "$RUNUSER" /app/ddnas "$@"
fi

# 非 root 启动：直接运行
exec /app/ddnas "$@"
