#!/bin/sh
set -eu

IPTABLES_BIN="${IPTABLES_BIN:-iptables}"
LAN_IF="${LAN_IF:-eth1}"
LAN_IP="${LAN_IP:-192.168.8.11}"
LAN_CIDR="${LAN_CIDR:-192.168.8.0/24}"
ENPROXY_PORT="${ENPROXY_PORT:-28080}"
BYPASS_CIDRS="${BYPASS_CIDRS:-10.0.0.0/8,100.64.0.0/10,127.0.0.0/8,169.254.0.0/16,172.16.0.0/12,192.168.0.0/16,224.0.0.0/4,240.0.0.0/4}"
KEEP_FAIL_CLOSED="${KEEP_FAIL_CLOSED:-1}"
NETWORK_WAIT_SECONDS="${NETWORK_WAIT_SECONDS:-60}"
ENPROXY_WAIT_SECONDS="${ENPROXY_WAIT_SECONDS:-30}"

# 新规则链使用 ENPROXY 命名；同时清理旧版本创建的 REDSOCKS 规则链。
NAT_CHAIN="TP_ENPROXY"
LEGACY_NAT_CHAIN="TP_REDSOCKS"
FWD_CHAIN="TP_ENPROXY_FWD"
LEGACY_FWD_CHAIN="TP_REDSOCKS_FWD"
UDP_NAT_CHAIN="TP_ENPROXY_UDP"
LEGACY_UDP_NAT_CHAIN="TP_REDSOCKS_UDP"
INPUT_CHAIN="TP_ENPROXY_IN"
LEGACY_INPUT_CHAIN="TP_REDSOCKS_IN"

# 保留原 comment，便于识别并删除旧版本创建的跳转规则。
NAT_COMMENT="transparent-proxy-nat"
FWD_COMMENT="transparent-proxy-forward"
UDP_NAT_COMMENT="transparent-proxy-udp-masq"
INPUT_COMMENT="transparent-proxy-input"
STARTUP_COMMENT="transparent-proxy-startup-guard"

log() {
    printf '%s\n' "[transparent-proxy] $*"
}

die() {
    log "ERROR: $*"
    exit 1
}

ipt() {
    "$IPTABLES_BIN" -w 5 "$@"
}

remove_nat_chain() {
    chain="$1"

    while ipt -t nat -C PREROUTING -m comment --comment "$NAT_COMMENT" -j "$chain" >/dev/null 2>&1; do
        ipt -t nat -D PREROUTING -m comment --comment "$NAT_COMMENT" -j "$chain" || break
    done

    ipt -t nat -F "$chain" >/dev/null 2>&1 || true
    ipt -t nat -X "$chain" >/dev/null 2>&1 || true
}

remove_nat() {
    remove_nat_chain "$NAT_CHAIN"
    remove_nat_chain "$LEGACY_NAT_CHAIN"
}

remove_forward_chain() {
    chain="$1"

    while ipt -t filter -C FORWARD -m comment --comment "$FWD_COMMENT" -j "$chain" >/dev/null 2>&1; do
        ipt -t filter -D FORWARD -m comment --comment "$FWD_COMMENT" -j "$chain" || break
    done

    ipt -t filter -F "$chain" >/dev/null 2>&1 || true
    ipt -t filter -X "$chain" >/dev/null 2>&1 || true
}

remove_forward() {
    remove_forward_chain "$FWD_CHAIN"
    remove_forward_chain "$LEGACY_FWD_CHAIN"
}

remove_udp_nat_chain() {
    chain="$1"

    while ipt -t nat -C POSTROUTING -m comment --comment "$UDP_NAT_COMMENT" -j "$chain" >/dev/null 2>&1; do
        ipt -t nat -D POSTROUTING -m comment --comment "$UDP_NAT_COMMENT" -j "$chain" || break
    done

    ipt -t nat -F "$chain" >/dev/null 2>&1 || true
    ipt -t nat -X "$chain" >/dev/null 2>&1 || true
}

remove_udp_nat() {
    remove_udp_nat_chain "$UDP_NAT_CHAIN"
    remove_udp_nat_chain "$LEGACY_UDP_NAT_CHAIN"
}

remove_input_chain() {
    chain="$1"

    while ipt -t filter -C INPUT -m comment --comment "$INPUT_COMMENT" -j "$chain" >/dev/null 2>&1; do
        ipt -t filter -D INPUT -m comment --comment "$INPUT_COMMENT" -j "$chain" || break
    done

    ipt -t filter -F "$chain" >/dev/null 2>&1 || true
    ipt -t filter -X "$chain" >/dev/null 2>&1 || true
}

remove_input_guard() {
    remove_input_chain "$INPUT_CHAIN"
    remove_input_chain "$LEGACY_INPUT_CHAIN"
}

# 重启时在清理旧链前先放置临时 TCP 防直连规则。
# 规则独立于自定义链，即使旧链被删除也仍会生效。
startup_guard_exists() {
    ipt -t filter -C FORWARD -i "$LAN_IF" -s "$LAN_CIDR" -p tcp ! -d "$LAN_CIDR" \
        -m comment --comment "$STARTUP_COMMENT" -j REJECT --reject-with tcp-reset >/dev/null 2>&1
}

install_startup_guard() {
    if ! startup_guard_exists; then
        ipt -t filter -I FORWARD 1 -i "$LAN_IF" -s "$LAN_CIDR" -p tcp ! -d "$LAN_CIDR" \
            -m comment --comment "$STARTUP_COMMENT" -j REJECT --reject-with tcp-reset
    fi
}

remove_startup_guard() {
    while startup_guard_exists; do
        ipt -t filter -D FORWARD -i "$LAN_IF" -s "$LAN_CIDR" -p tcp ! -d "$LAN_CIDR" \
            -m comment --comment "$STARTUP_COMMENT" -j REJECT --reject-with tcp-reset || return 1
    done
}

cleanup_all() {
    remove_nat
    remove_input_guard
    remove_udp_nat
    remove_forward
}

verify_cleanup() {
    for chain in "$NAT_CHAIN" "$LEGACY_NAT_CHAIN" "$UDP_NAT_CHAIN" "$LEGACY_UDP_NAT_CHAIN"; do
        if ipt -t nat -L "$chain" -n >/dev/null 2>&1; then
            log "ERROR: 清理后仍存在 NAT 链：$chain"
            return 1
        fi
    done
    for chain in "$FWD_CHAIN" "$LEGACY_FWD_CHAIN" "$INPUT_CHAIN" "$LEGACY_INPUT_CHAIN"; do
        if ipt -t filter -L "$chain" -n >/dev/null 2>&1; then
            log "ERROR: 清理后仍存在 filter 链：$chain"
            return 1
        fi
    done
    if startup_guard_exists; then
        log "ERROR: 清理后仍存在启动防直连规则。"
        return 1
    fi
}

cleanup_runtime() {
    # 停止管理容器时撤销 TCP REDIRECT，避免继续把新连接送往不可用的代理。
    remove_nat

    # 不允许清理失败却以“已停止”误报：自定义链还存在即说明可能有残留。
    if ipt -t nat -L "$NAT_CHAIN" -n >/dev/null 2>&1 || \
       ipt -t nat -L "$LEGACY_NAT_CHAIN" -n >/dev/null 2>&1; then
        log "ERROR: TCP NAT 链清理不完整，请检查宿主机 iptables。"
        return 1
    fi

    if [ "$KEEP_FAIL_CLOSED" = "1" ]; then
        # 保留 UDP 直连/SNAT、公网 TCP FORWARD 拒绝规则，以及 enproxy 端口的 LAN 访问限制。
        log "TCP NAT 已停止；公网 TCP 转发仍被阻断，UDP SNAT 和 enproxy INPUT 访问限制继续保留。"
        if startup_guard_exists; then
            log "WARNING: 启动时的临时防直连规则仍在，成功重启后会自动撤销。"
        fi
        log "彻底卸载所有规则：先停止服务，再执行 docker compose run --rm -e ACTION=cleanup transparent-proxy"
    else
        remove_input_guard
        remove_udp_nat
        remove_forward
        remove_startup_guard || return 1
        verify_cleanup || return 1
        log "已移除 TCP NAT、INPUT、UDP SNAT 和 FORWARD 规则。"
    fi
}

on_exit() {
    status=$?
    trap - EXIT

    if ! cleanup_runtime; then
        log "ERROR: 退出清理失败；宿主机可能仍有旧规则，请手动检查。"
        [ "$status" -ne 0 ] || status=1
    fi
    exit "$status"
}

require_uint() {
    name="$1"
    value="$2"

    case "$value" in
        ""|*[!0-9]*) die "$name 必须是正整数，当前值：$value" ;;
    esac

    [ "$value" -gt 0 ] || die "$name 必须大于 0"
}

add_bypass_rules() {
    table="$1"
    chain="$2"

    # LAN_CIDR 总是加入绕过列表，避免访问局域网设备被送入代理。
    ipt -t "$table" -A "$chain" -d "$LAN_CIDR" -j RETURN

    old_ifs="$IFS"
    IFS=','
    for cidr in $BYPASS_CIDRS; do
        [ -n "$cidr" ] || continue
        [ "$cidr" = "$LAN_CIDR" ] && continue
        ipt -t "$table" -A "$chain" -d "$cidr" -j RETURN
    done
    IFS="$old_ifs"
}

setup_nat() {
    remove_nat

    ipt -t nat -N "$NAT_CHAIN"
    ipt -t nat -A "$NAT_CHAIN" ! -i "$LAN_IF" -j RETURN
    ipt -t nat -A "$NAT_CHAIN" ! -s "$LAN_CIDR" -j RETURN

    # DNS 不交给透明 TCP 代理，TCP 53 保持直连；UDP 本来不会命中 TCP REDIRECT。
    ipt -t nat -A "$NAT_CHAIN" -p tcp --dport 53 -j RETURN

    add_bypass_rules nat "$NAT_CHAIN"

    # TCP 直接交给 Host 网络模式的 enproxy，不再经过 redsocks。
    ipt -t nat -A "$NAT_CHAIN" -p tcp -j REDIRECT --to-ports "$ENPROXY_PORT"

    ipt -t nat -I PREROUTING 1 -m comment --comment "$NAT_COMMENT" -j "$NAT_CHAIN"
}

# 非 TCP 流量仍由 Linux 内核直接路由，不经过 enproxy。
# 单网卡同网段旁路由对非 TCP 流量做 MASQUERADE，使返回流量继续经过树莓派。
setup_udp_nat() {
    remove_udp_nat

    ipt -t nat -N "$UDP_NAT_CHAIN"
    ipt -t nat -A "$UDP_NAT_CHAIN" ! -o "$LAN_IF" -j RETURN
    ipt -t nat -A "$UDP_NAT_CHAIN" ! -s "$LAN_CIDR" -j RETURN
    ipt -t nat -A "$UDP_NAT_CHAIN" -d "$LAN_CIDR" -j RETURN
    # DNS TCP 53 直连需要 SNAT，使回包继续经过本机。
    ipt -t nat -A "$UDP_NAT_CHAIN" -p tcp --dport 53 -j MASQUERADE
    ipt -t nat -A "$UDP_NAT_CHAIN" ! -p tcp -j MASQUERADE

    ipt -t nat -I POSTROUTING 1 -m comment --comment "$UDP_NAT_COMMENT" -j "$UDP_NAT_CHAIN"
}

# Docker 可能将宿主机 FORWARD 默认策略设为 DROP。
# 放行非 TCP 原路及回程、内网绕过流量；公网 TCP 若未被 REDIRECT 到本机 INPUT，
# 则拒绝转发，避免代理服务停止后 TCP 流量意外直连。
setup_forward_rules() {
    remove_forward

    ipt -t filter -N "$FWD_CHAIN"

    ipt -t filter -A "$FWD_CHAIN" -i "$LAN_IF" -o "$LAN_IF" -d "$LAN_CIDR" \
        ! -p tcp -m conntrack --ctstate ESTABLISHED,RELATED -j ACCEPT
    ipt -t filter -A "$FWD_CHAIN" -i "$LAN_IF" -o "$LAN_IF" -d "$LAN_CIDR" \
        -p tcp --sport 53 -m conntrack --ctstate ESTABLISHED,RELATED -j ACCEPT

    ipt -t filter -A "$FWD_CHAIN" ! -i "$LAN_IF" -j RETURN
    ipt -t filter -A "$FWD_CHAIN" ! -s "$LAN_CIDR" -j RETURN
    ipt -t filter -A "$FWD_CHAIN" ! -o "$LAN_IF" -j RETURN

    ipt -t filter -A "$FWD_CHAIN" -d "$LAN_CIDR" -j ACCEPT

    old_ifs="$IFS"
    IFS=','
    for cidr in $BYPASS_CIDRS; do
        [ -n "$cidr" ] || continue
        [ "$cidr" = "$LAN_CIDR" ] && continue
        ipt -t filter -A "$FWD_CHAIN" -d "$cidr" -j ACCEPT
    done
    IFS="$old_ifs"

    ipt -t filter -A "$FWD_CHAIN" -p udp -j ACCEPT
    ipt -t filter -A "$FWD_CHAIN" -p tcp --dport 53 -j ACCEPT
    ipt -t filter -A "$FWD_CHAIN" -p tcp -j REJECT --reject-with tcp-reset
    # 已匹配 LAN 接口/来源/出口，剩余 ICMP、GRE、ESP 等非 TCP 流量正常转发。
    ipt -t filter -A "$FWD_CHAIN" -j ACCEPT

    ipt -t filter -I FORWARD 1 -m comment --comment "$FWD_COMMENT" -j "$FWD_CHAIN"
}

# enproxy 的 28080 同时承载：
# 1. 局域网设备主动连接的普通 HTTP 正向代理；
# 2. 经 PREROUTING REDIRECT 进入的透明 TCP 连接。
#
# 因此不能再像旧版那样只允许 conntrack DNAT 连接，否则普通 HTTP 代理也会被拒绝。
# 这里改为仅允许 LAN 接口/LAN 网段访问该端口，并允许本机 loopback。
setup_input_guard() {
    remove_input_guard

    ipt -t filter -N "$INPUT_CHAIN"
    ipt -t filter -A "$INPUT_CHAIN" -i lo -j RETURN
    ipt -t filter -A "$INPUT_CHAIN" -p tcp --dport "$ENPROXY_PORT" ! -i "$LAN_IF" \
        -j REJECT --reject-with tcp-reset
    ipt -t filter -A "$INPUT_CHAIN" -p tcp --dport "$ENPROXY_PORT" ! -s "$LAN_CIDR" \
        -j REJECT --reject-with tcp-reset
    ipt -t filter -A "$INPUT_CHAIN" -j RETURN

    ipt -t filter -I INPUT 1 -m comment --comment "$INPUT_COMMENT" -j "$INPUT_CHAIN"
}

wait_for_lan_ip() {
    attempt=0

    while ! ip -o -4 addr show dev "$LAN_IF" 2>/dev/null | grep -Fq "inet $LAN_IP/"; do
        if [ "$attempt" -ge "$NETWORK_WAIT_SECONDS" ]; then
            die "等待网卡 $LAN_IF 获得地址 $LAN_IP 超时；请检查 LAN_IF/LAN_IP 和宿主机静态地址。"
        fi

        attempt=$((attempt + 1))
        sleep 1
    done
}

enproxy_is_listening() {
    ss -H -lnt "sport = :$ENPROXY_PORT" 2>/dev/null | grep -q .
}

wait_for_enproxy() {
    attempt=0

    while ! enproxy_is_listening; do
        if [ "$attempt" -ge "$ENPROXY_WAIT_SECONDS" ]; then
            die "等待 enproxy 监听 TCP 端口 $ENPROXY_PORT 超时；请检查 enproxy 是否使用 host 网络，以及 HTTP inbound 是否监听 0.0.0.0:$ENPROXY_PORT。"
        fi

        attempt=$((attempt + 1))
        sleep 1
    done
}

main() {
    command -v "$IPTABLES_BIN" >/dev/null 2>&1 || die "找不到 iptables 命令：$IPTABLES_BIN"
    command -v ip >/dev/null 2>&1 || die "找不到 ip 命令"
    command -v ss >/dev/null 2>&1 || die "找不到 ss 命令"

    if [ "${ACTION:-run}" = "cleanup" ]; then
        cleanup_all
        remove_startup_guard
        verify_cleanup || die "仍有 iptables 规则未能清理。"
        log "已清理新旧版本创建的 TCP NAT、UDP SNAT、FORWARD 和 INPUT 规则。"
        exit 0
    fi

    [ "${ACTION:-run}" = "run" ] || die "ACTION 只支持 run 或 cleanup"

    : "${LAN_IF:?请设置 LAN_IF}"
    : "${LAN_IP:?请设置 LAN_IP}"
    : "${LAN_CIDR:?请设置 LAN_CIDR}"

    require_uint ENPROXY_PORT "$ENPROXY_PORT"
    require_uint NETWORK_WAIT_SECONDS "$NETWORK_WAIT_SECONDS"
    require_uint ENPROXY_WAIT_SECONDS "$ENPROXY_WAIT_SECONDS"

    case "$KEEP_FAIL_CLOSED" in
        0|1) ;;
        *) die "KEEP_FAIL_CLOSED 只能是 0 或 1" ;;
    esac

    ip link show dev "$LAN_IF" >/dev/null 2>&1 || die "网卡不存在：$LAN_IF"

    log "iptables 后端：$("$IPTABLES_BIN" --version)"
    log "等待网卡 $LAN_IF/$LAN_IP 就绪..."
    wait_for_lan_ip

    # UDP 直连需要宿主机内核启用转发。
    [ "$(cat /proc/sys/net/ipv4/ip_forward)" = "1" ] || die "需要宿主机 net.ipv4.ip_forward=1；请先执行 sudo ./setup-host.sh"

    if [ "$(cat /proc/sys/net/ipv4/conf/all/send_redirects)" != "0" ] || \
       [ "$(cat "/proc/sys/net/ipv4/conf/$LAN_IF/send_redirects")" != "0" ]; then
        log "WARNING: ICMP Redirect 未完全关闭，建议执行 sudo ./setup-host.sh"
    fi

    # 提前安装退出钩子和临时 FORWARD 防直连规则，覆盖重启清理旧链的空档。
    trap on_exit EXIT
    trap 'exit 0' TERM INT
    trap 'exit 1' HUP QUIT
    install_startup_guard

    # 清理本脚本新旧版本创建的规则，避免旧 REDSOCKS 规则残留。
    cleanup_all

    # 新 FORWARD 规则就绪后才撤销临时防护；随后继续初始化其它规则。
    setup_forward_rules
    remove_startup_guard
    setup_udp_nat
    setup_input_guard

    log "等待 enproxy 监听 TCP 端口 $ENPROXY_PORT..."
    wait_for_enproxy

    # enproxy 已就绪后才启用 TCP REDIRECT。
    setup_nat

    log "透明 TCP 代理已启动。"
    log "LAN: $LAN_IF $LAN_CIDR；enproxy: $LAN_IP:$ENPROXY_PORT"
    log "TCP NAT: $NAT_CHAIN -> $ENPROXY_PORT；非 TCP SNAT: $UDP_NAT_CHAIN；FORWARD: $FWD_CHAIN"
    log "普通 HTTP 代理与透明 TCP 连接共用 enproxy HTTP 入站；本容器不再启动 redsocks。"

    # 监控监听端口；若 enproxy 停止，则撤销 REDIRECT 并保留 fail-closed 规则。
    while enproxy_is_listening; do
        sleep 2
    done

    die "enproxy 已不再监听 TCP 端口 $ENPROXY_PORT。"
}

main "$@"
