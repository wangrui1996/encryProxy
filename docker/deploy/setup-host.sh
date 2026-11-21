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

# 单网卡旁路由：通过 DHCP 获取地址；可选地指定比 DHCP 默认路由优先的网关。
# 路由状态仅记录本脚本添加的默认路由，不改写其他静态路由。
UPSTREAM_GATEWAY="$(sed -n 's/^UPSTREAM_GATEWAY=//p' "$ENV_FILE" | tail -n 1)"
STATE_DIR="/var/lib/transparent-proxy"
STATE_FILE="$STATE_DIR/gateway-route-$LAN_IF.state"
ROUTE_METRIC=1

# 两种模式都需要同步 NetworkManager；未设置上游网关时恢复 DHCP 默认路由。
command -v nmcli >/dev/null 2>&1 || { echo "切换默认网关需要 NetworkManager（nmcli）" >&2; exit 1; }
NM_CONNECTION="$(nmcli -g GENERAL.CONNECTION device show "$LAN_IF")"
[ -n "$NM_CONNECTION" ] && [ "$NM_CONNECTION" != "--" ] || { echo "NetworkManager 没有管理网卡 $LAN_IF" >&2; exit 1; }
NM_UUID="$(nmcli -g connection.uuid connection show "$NM_CONNECTION")"
[ "$(nmcli -g ipv4.method connection show "$NM_UUID")" = "auto" ] || { echo "连接 $NM_CONNECTION 不是 DHCP 模式，不自动修改默认路由" >&2; exit 1; }

LAN_IP="$(sed -n 's/^LAN_IP=//p' "$ENV_FILE" | tail -n 1)"
if [ -n "$UPSTREAM_GATEWAY" ]; then
    [ "$UPSTREAM_GATEWAY" != "$LAN_IP" ] || { echo "UPSTREAM_GATEWAY 不能是树莓派自身的 LAN_IP: $LAN_IP" >&2; exit 1; }
    ip -4 route get "$UPSTREAM_GATEWAY" >/dev/null 2>&1 || { echo "无效的 UPSTREAM_GATEWAY: $UPSTREAM_GATEWAY" >&2; exit 1; }
fi

# 通过独立状态文件识别本脚本添加的路由；没有记录的静态默认路由不擅自删除。
MANAGED_UUID=""
MANAGED_GATEWAY=""
if [ -f "$STATE_FILE" ]; then
    read -r MANAGED_UUID MANAGED_GATEWAY < "$STATE_FILE"
    [ "$MANAGED_UUID" = "$NM_UUID" ] || { echo "已记录的网关属于其他 NetworkManager 连接，请检查 $STATE_FILE" >&2; exit 1; }
fi
MANAGED_ROUTE=""
[ -z "$MANAGED_GATEWAY" ] || MANAGED_ROUTE="0.0.0.0/0 $MANAGED_GATEWAY $ROUTE_METRIC"
NM_ROUTES="$(nmcli -g ipv4.routes connection show "$NM_UUID")"

# nmcli -g 对多条路由使用逗号分隔；仅认领状态文件精确记录的那一条。
route_exists() {
    printf '%s\n' "$NM_ROUTES" | tr ',' '\n' | sed 's/^[[:space:]]*//;s/[[:space:]]*$//' | grep -Fx "$1" >/dev/null 2>&1
}

# 存在用户自行配置的默认路由时拒绝更改，避免覆盖与代理无关的配置。
DEFAULT_ROUTES="$(printf '%s\n' "$NM_ROUTES" | tr ',' '\n' | sed 's/^[[:space:]]*//;s/[[:space:]]*$//' | grep -E '^(0\.0\.0\.0/0|0/0|default)[[:space:]]' || true)"
if [ -n "$DEFAULT_ROUTES" ]; then
    while IFS= read -r route; do
        [ "$route" = "$MANAGED_ROUTE" ] || {
            echo "存在非本脚本管理的静态默认路由：$route；为避免误删，停止修改" >&2
            echo "如果这是旧版 setup-host.sh 创建的路由，请确认后手动迁移一次。" >&2
            exit 1
        }
    done <<EOF_ROUTES
$DEFAULT_ROUTES
EOF_ROUTES
fi

NM_GATEWAY="$(nmcli -g ipv4.gateway connection show "$NM_UUID")"
[ -z "$NM_GATEWAY" ] || [ "$NM_GATEWAY" = "--" ] || {
    echo "连接 $NM_CONNECTION 已有 ipv4.gateway=$NM_GATEWAY，拒绝覆盖" >&2
    exit 1
}
NM_NEVER_DEFAULT="$(nmcli -g ipv4.never-default connection show "$NM_UUID")"
[ "$NM_NEVER_DEFAULT" != "yes" ] || {
    echo "连接 $NM_CONNECTION 已设置 ipv4.never-default=yes，拒绝覆盖" >&2
    exit 1
}

# 如果切换网关或切回 DHCP，只移除状态文件记录的默认路由。
if [ -n "$MANAGED_ROUTE" ] && [ "$UPSTREAM_GATEWAY" != "$MANAGED_GATEWAY" ]; then
    if route_exists "$MANAGED_ROUTE"; then
        nmcli connection modify "$NM_UUID" -ipv4.routes "$MANAGED_ROUTE"
    fi
fi

if [ -n "$UPSTREAM_GATEWAY" ]; then
    NEW_ROUTE="0.0.0.0/0 $UPSTREAM_GATEWAY $ROUTE_METRIC"
    if [ "$UPSTREAM_GATEWAY" != "$MANAGED_GATEWAY" ] || ! route_exists "$NEW_ROUTE"; then
        nmcli connection modify "$NM_UUID" +ipv4.routes "$NEW_ROUTE"
    fi
fi

# 保留 DHCP 下发的其他路由，只通过更低 metric 的手动默认路由选择上游网关。
# 同时兼容旧版 setup-host.sh 曾经设置的 ignore-auto-routes=yes。
nmcli connection modify "$NM_UUID" ipv4.ignore-auto-routes no

if [ -n "$UPSTREAM_GATEWAY" ]; then
    mkdir -p "$STATE_DIR"
    chmod 700 "$STATE_DIR"
    TEMP_STATE="$(mktemp "$STATE_FILE.XXXXXX")"
    chmod 600 "$TEMP_STATE"
    printf '%s %s\n' "$NM_UUID" "$UPSTREAM_GATEWAY" > "$TEMP_STATE"
    mv -f "$TEMP_STATE" "$STATE_FILE"
    echo "已配置手动默认网关：$UPSTREAM_GATEWAY（$LAN_IF；metric $ROUTE_METRIC）"
else
    [ ! -f "$STATE_FILE" ] || rm -f "$STATE_FILE"
    echo "已恢复 DHCP 默认网关模式：使用路由器下发的路由。"
fi

# 尽量在线生效，不强制断开 SSH；NetworkManager 连接配置已更新。
if ! nmcli device reapply "$LAN_IF"; then
    echo "WARNING: NetworkManager 未能在线应用路由；配置已保存，请在本机终端重新激活连接。" >&2
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
