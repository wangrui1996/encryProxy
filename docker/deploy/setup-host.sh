#!/bin/sh
set -eu

[ "$(id -u)" -eq 0 ] || { echo "请使用 sudo ./setup-host.sh 运行" >&2; exit 1; }

# 支持从任意工作目录执行，并继续使用原先从 .env 提取 LAN_IF 的方式。
SCRIPT_DIR="$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)"
ENV_FILE="$SCRIPT_DIR/.env"
[ -f "$ENV_FILE" ] || { echo "找不到环境配置: $ENV_FILE" >&2; exit 1; }

# 从 .env 中读取 LAN_IF；只允许 Linux 常见网卡名字符。
LAN_IF="$(sed -n 's/^LAN_IF=//p' "$ENV_FILE" | tail -n 1)"
LAN_IF="${LAN_IF:-eth0}"
case "$LAN_IF" in
    *[!a-zA-Z0-9_.:-]*|'') echo "无效网卡名: $LAN_IF" >&2; exit 1 ;;
esac

ip link show dev "$LAN_IF" >/dev/null 2>&1 || { echo "网卡不存在: $LAN_IF" >&2; exit 1; }

# Docker host network 不允许使用 Compose sysctls 改宿主机这些参数。
# 单网卡同网段路由必须开启 IPv4 转发并关闭 ICMP Redirect。
cat > /etc/sysctl.d/99-transparent-proxy.conf <<EOF_SYSCTL
net.ipv4.ip_forward = 1
net.ipv4.conf.all.send_redirects = 0
net.ipv4.conf.default.send_redirects = 0
net.ipv4.conf.$LAN_IF.send_redirects = 0
EOF_SYSCTL

sysctl -p /etc/sysctl.d/99-transparent-proxy.conf
echo "宿主机网络参数已生效：IPv4 转发开启，ICMP Redirect 关闭。"
