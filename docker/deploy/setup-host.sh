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

# 单网卡旁路由：客户端 DHCP 网关可指向本机，但宿主机不能以自身为默认网关。
# UPSTREAM_GATEWAY 留空则保持原行为；设置后忽略 DHCP 路由并指定 IPv4 默认路由。
UPSTREAM_GATEWAY="$(sed -n 's/^UPSTREAM_GATEWAY=//p' "$ENV_FILE" | tail -n 1)"
if [ -n "$UPSTREAM_GATEWAY" ]; then
    LAN_IP="$(sed -n 's/^LAN_IP=//p' "$ENV_FILE" | tail -n 1)"
    [ "$UPSTREAM_GATEWAY" != "$LAN_IP" ] || { echo "UPSTREAM_GATEWAY 不能是树莓派自身的 LAN_IP: $LAN_IP" >&2; exit 1; }
    ip -4 route get "$UPSTREAM_GATEWAY" >/dev/null 2>&1 || { echo "无效的 UPSTREAM_GATEWAY: $UPSTREAM_GATEWAY" >&2; exit 1; }
    command -v nmcli >/dev/null 2>&1 || { echo "设置 UPSTREAM_GATEWAY 需要 NetworkManager（nmcli）" >&2; exit 1; }

    # 使用实际管理该网卡的连接，而不是硬编码 netplan-eth0。
    NM_CONNECTION="$(nmcli -g GENERAL.CONNECTION device show "$LAN_IF")"
    [ -n "$NM_CONNECTION" ] && [ "$NM_CONNECTION" != "--" ] || { echo "NetworkManager 没有管理网卡 $LAN_IF" >&2; exit 1; }
    [ "$(nmcli -g ipv4.method connection show "$NM_CONNECTION")" = "auto" ] || { echo "连接 $NM_CONNECTION 不是 DHCP 模式，请手动检查其静态网关" >&2; exit 1; }

    # 保留既有静态路由，仅补充默认路由；重跑不重复追加。
    NM_ROUTES="$(nmcli -g ipv4.routes connection show "$NM_CONNECTION")"
    case "$NM_ROUTES" in
        *"0.0.0.0/0 $UPSTREAM_GATEWAY 100"*) ;;
        *"0.0.0.0/0 "*) echo "连接 $NM_CONNECTION 已存在其他静态默认路由，请先检查 ipv4.routes" >&2; exit 1 ;;
        *) nmcli connection modify "$NM_CONNECTION" +ipv4.routes "0.0.0.0/0 $UPSTREAM_GATEWAY 100" ;;
    esac
    nmcli connection modify "$NM_CONNECTION" ipv4.gateway "" ipv4.ignore-auto-routes yes ipv4.never-default no

    # 尽量在线生效，避免通过 SSH 运行时断开连接；同时修正当前错误路由。
    if ! nmcli device reapply "$LAN_IF"; then
        echo "WARNING: 无法在线应用 NetworkManager 配置；下次连接激活时生效" >&2
    fi
    ip -4 route replace default via "$UPSTREAM_GATEWAY" dev "$LAN_IF" metric 100
    echo "宿主机默认网关已设置为 $UPSTREAM_GATEWAY（$LAN_IF，metric 100），并保存到 NetworkManager 连接 $NM_CONNECTION。"
fi

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
