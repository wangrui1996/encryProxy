# enproxy

> 使用 Go 编写的轻量级、多入站、多出站 TCP 代理与中继服务。
>
> 当前源码版本：`v0.1.0-beta`。本文档根据当前提供的源码整理，优先描述**已经实现的行为**，而非规划中的功能。

enproxy 将连接接入、协议处理、路由选择、出站拨号与双向转发分别封装在不同模块中。可以用作固定目标 TCP 中继、HTTP/HTTPS 正向代理，或传统 AEAD Shadowsocks TCP 代理；支持自定义 AES-GCM 外层加密、账号认证、限速、流量统计，以及基于域名、IP 和本地规则文件的分流。

## 目录

- [主要功能](#主要功能)
- [架构与连接流程](#架构与连接流程)
- [运行环境与快速开始](#运行环境与快速开始)
- [配置说明](#配置说明)
- [路由与规则文件](#路由与规则文件)
- [典型配置](#典型配置)
- [认证加密与限速](#认证加密与限速)
- [DNS流量统计与Web](#dns流量统计与web)
- [项目结构](#项目结构)
- [使用限制与安全建议](#使用限制与安全建议)
- [常见问题](#常见问题)

## 主要功能

| 功能 | 当前实现 |
| --- | --- |
| 多入站 | 同时监听多个 TCP 地址；每个入站独立指定协议、加密与端口限速 |
| 入站协议 | `tcp`、`http`、`shadowsocks` |
| 自定义出站协议 | `tcp`、`http`、`shadowsocks` |
| 内置出站 | `freedom`（直连目标）、`blackhole`（丢弃/关闭连接） |
| TCP 中继 | 通过 `relay` 路由，将一个 TCP 入站连接至固定 TCP 出站 |
| HTTP 代理 | 普通 HTTP 请求转发、HTTPS `CONNECT` 隧道、可选 HTTP Basic 认证 |
| Shadowsocks | 传统 AEAD TCP 入站与出站，支持三种加密方法；**不支持 UDP** |
| 外层加密 | 可选自定义 AES-GCM 分帧传输，与 HTTP/Shadowsocks 协议层相互独立 |
| 账号管理 | 区分加密通道认证与 HTTP 代理认证；共享账号级限速器 |
| 带宽限制 | 按入站端口、按账号分别设置上传/下载速率；采用令牌桶 |
| 路由分流 | 按入站标签与目标域名/IP 匹配，支持配置内规则及本地文件 |
| 规则集 | 解析 `Loyalsoldier/clash-rules` 使用的部分 `payload:` 列表格式，以及纯文本逐行规则 |
| DNS | 基于系统 DNS 解析，支持可选 TTL 缓存、同名并发查询合并及多 IP 轮换尝试 |
| 流量统计 | 端口和用户累计统计、近实时采样、小时/日/月历史桶 |
| Web | 已有可选 WebServer 初始化、启动和关闭接入；具体页面/API 请以仓库中的 `web/` 模块为准 |
| 生命周期 | 统一初始化组件、启动监听、响应停止信号、关闭活动连接并回收资源 |

> **范围说明：** 当前项目不是 SOCKS5、TUN/TAP、透明网关或完整 Clash 内核；仅凭部署一个 HTTP 代理，不会自动接管局域网设备的所有网络流量。

## 架构与连接流程

### 模块关系

```text
                     配置文件 (JSON)
                            │
                            ▼
                    config.Load / Validate
                            │
                            ▼
                       ProxyManager
          ┌─────────────────┼────────────────────┐
          │                 │                    │
       Inbound           Account              DNS / Stat / Web
          │              Manager                Managers
          ▼
    TCP 连接接入
          │
          ├─ 可选 ENPA 外层认证
          ├─ 端口及账号限速
          ├─ 流量计数
          └─ 可选 AES-GCM 流解码
          │
          ▼
   Protocol Handler (TCP / HTTP / Shadowsocks)
          │  解析代理目标（TCP relay 无动态目标）
          ▼
      Route Manager
          │  按 inbound tag、目标域名/IP 选择出口标签
          ▼
     Outbound Manager
          │  freedom / blackhole / tcp / http / shadowsocks
          ▼
     Outbound Dialer + DNS
          │  可选上游认证与 AES-GCM 加密包装
          ▼
         Core Relay
          │
          └─ 双向复制、半关闭、连接结束后的资源回收
```

### 两类路由

**`relay`：固定 TCP 转发。** 入站协议必须是 `tcp`，出站协议也必须是自定义 `tcp`。路由不需要解析客户端请求的目标地址，因为上游 `server` 是预先配置好的固定地址。

**`proxy`：按请求目标分流。** 入站协议必须是 `http` 或 `shadowsocks`。协议处理器提取目标主机/IP，再按照 `policies` 的配置顺序决定使用哪个出站；没有命中时使用 `default_outbound`。

一个入站标签最多只能被一个路由条目引用，但同一路由条目可绑定多个入站标签。同一个入站标签不能同时配置 `relay` 和 `proxy` 路由。

### 生命周期

`ProxyManager` 在构造期间加载、校验配置，创建账号、路由、DNS、出站、入站、统计及可选 Web 组件；`Start()` 负责绑定监听端口并启动服务，`Close()` 负责停止入站、结束连接、停止 Web 与后台 DNS/统计任务。构建时会打印入站、出站、路由及策略信息。

**未被路由引用的入站或出站允许存在。** 未使用的出站仅产生警告；未绑定路由的入站仍会监听，但收到请求后无法成功完成路由和转发。

## 运行环境与快速开始

### 准备

- Go 工具链，以及项目对应的 `go.mod`/`go.sum` 依赖。源码使用了 `atomic.Pointer` 等较新 API，Go 至少应支持 Go 1.19；实际建议遵循仓库 `go.mod` 指定的版本。
- 支持 TCP 网络通信的运行环境，例如 Linux、树莓派 Raspberry Pi OS 或其他可运行 Go 二进制的系统。
- 一个 UTF-8 编码的标准 JSON 配置文件。**JSON 不支持注释及尾随逗号。**

下面假定在含有 `enProxy.go`（`package main`）和 `go.mod` 的**仓库根目录**执行命令。

### 编译

```bash
go mod download
go build -o enproxy .
```

树莓派 64 位 Linux 可交叉编译（在安装相应 Go 工具链的机器上）：

```bash
GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -o enproxy-linux-arm64 .
```

> 本次提供的源码摘录未包含 `go.mod`、`go.sum` 或完整 `web/` 目录，因此上述命令是仓库结构齐备时的标准编译方式，不表示这份摘录本身可独立完成编译。

### 最小 HTTP 代理示例

新建 `config.json`：

```json
{
  "log": { "level": "info", "path": "", "access": "" },
  "web": { "enabled": false, "listen": "", "user": "", "passwd": "" },
  "inbounds": [
    {
      "tag": "http-in",
      "listen": "127.0.0.1:18080",
      "protocol": "http",
      "http": { "auth_required": false },
      "rate_limit": { "up_kbps": 0, "down_kbps": 0 }
    }
  ],
  "outbounds": [],
  "accounts": [],
  "route": [
    {
      "type": "proxy",
      "inbound": ["http-in"],
      "rule": {
        "default_outbound": "freedom",
        "policies": []
      }
    }
  ]
}
```

启动：

```bash
./enproxy config.json
```

测试 HTTP 与 HTTPS 代理：

```bash
curl -x http://127.0.0.1:18080 -I http://example.com/
curl -x http://127.0.0.1:18080 -I https://example.com/
```

第二个请求通过 HTTP `CONNECT` 建立 HTTPS 隧道，不会在代理中解密客户端与目标站点之间的 TLS 内容。默认情况下入口程序未传参数时读取当前工作目录的 `config.json`。可通过 `Ctrl+C` 或 `SIGTERM` 触发关闭。

## 配置说明

### 顶层结构

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| `log` | object | 日志配置：`level`、`path`、`access` |
| `web` | object | Web 服务开关、监听地址和登录凭据 |
| `inbounds` | array | 入站监听列表 |
| `dns` | object/null | 可选 DNS 缓存设置 |
| `outbounds` | array | 自定义出站列表，**不配置内置出口** |
| `accounts` | array | 认证账号和账号级限速 |
| `route` | array | 入站与出站之间的路由关系、代理策略 |

配置读取器会拒绝未知 JSON 字段、非法字段值、额外的 JSON 根对象和重复的入站/出站标签。引用不存在的入站/出站、协议组合不兼容或同一入站重复路由，也会在启动初始化阶段报错。

### `inbounds`：入站

| 字段 | 说明 |
| --- | --- |
| `tag` | 唯一标识，用于路由的 `inbound` 引用 |
| `listen` | TCP 监听地址，如 `127.0.0.1:18080`、`:18080`、`0.0.0.0:18080` |
| `protocol` | `tcp`、`http` 或 `shadowsocks` |
| `crypto` | 可选自定义 AES-GCM 外层加密；不填即无此加密层 |
| `http` | `protocol=http` 时必填的 HTTP 选项 |
| `shadowsocks` | `protocol=shadowsocks` 时必填 |
| `rate_limit` | 端口共享上传/下载速率限制 |

同一配置中的入站 `listen` 和 `tag` 不可重复。`http` 与 `shadowsocks` 选项不可混用；`tcp` 入站不可设置两种应用层协议选项。

### `outbounds`：自定义出站

| 字段 | 说明 |
| --- | --- |
| `tag` | 唯一出站标签 |
| `protocol` | `tcp`、`http`、`shadowsocks` |
| `server` | 上游固定服务器地址，必须是 `host:port` |
| `crypto` | 可选连接上游前使用的外层 AES-GCM 包装及认证 |
| `http` | HTTP 上游代理的协议选项，使用 Basic 认证时包含账号和密码 |
| `shadowsocks` | Shadowsocks 上游的 `method` 与 `password` |

无需在 `outbounds` 中声明 `freedom` 和 `blackhole`：它们已经是内置出站，可直接在路由中使用。`direct` 不是当前可配置的自定义出站协议。

| 出站 | 行为及注意事项 |
| --- | --- |
| `freedom` | 直接向代理请求中指定的目标地址建立 TCP 连接 |
| `blackhole` | 阻止请求继续转发；HTTP/Shadowsocks 入站会关闭对应请求连接 |
| `tcp` | **仅连接固定 `server`**，不会按照代理请求动态改写目标地址 |
| `http` | 通过上游 HTTP 代理转发 HTTP 请求或使用 `CONNECT` 建立隧道 |
| `shadowsocks` | 通过传统 AEAD Shadowsocks TCP 服务建立目标连接 |

虽然当前配置校验允许 `proxy` 路由选择 `tcp` 出站，但它始终忽略请求中的目标地址。因此，不应把它当成可以自动访问任意网站的通用代理出口；普通正向代理优先使用 `freedom`、`http` 或 `shadowsocks`。

### `accounts`：账号

```json
{
  "name": "alice",
  "password": "REPLACE_WITH_A_STRONG_PASSWORD",
  "crypto_enable": true,
  "proxy_enable": true,
  "rate_limit": { "up_kbps": 512, "down_kbps": 2048 }
}
```

- `crypto_enable`：是否允许用该账号完成自定义外层加密通道的 ENPA 认证。
- `proxy_enable`：是否允许用该账号完成 HTTP 代理 Basic 认证。
- 一个账号可同时开启两种认证权限，也可分别启用。
- 同名账号不允许重复；账号密码不能为空。

在 `inbounds[].crypto`、`inbounds[].http` 中**不能**填写用户凭据；服务端凭据统一放入 `accounts`。上游出站登录信息则放在对应的 `outbounds[].crypto` 或 `outbounds[].http`。

### `rate_limit`：限速

```json
"rate_limit": { "up_kbps": 1024, "down_kbps": 2048 }
```

- `up_kbps`：客户端 → 代理方向的限制。
- `down_kbps`：代理 → 客户端方向的限制。
- 源码实际按 `参数 × 1024 字节/秒` 计算，即这里的 `1024` 约代表 `1 MiB/s`。虽然字段名写作 `kbps`，**并不是千比特/秒**。
- `0` 表示该方向不启用限速；不允许负数。
- 端口限速器由同一入站下所有连接共享；账号限速器由同一账号的连接共享。若两者都配置，会依次经过两层限制，而非仅选择其中一层。
- 每个方向令牌桶突发额度为 `32 KiB`；关闭连接可取消正在等待的限速操作。

### `crypto`：自定义外层加密

```json
"crypto": {
  "key": "0123456789abcdef0123456789abcdef",
  "auth_required": false
}
```

`crypto` 对象存在即启用 AES-GCM 外层分帧传输；不配置则无外层加密。`key` 必须是 UTF-8 编码后 **16、24 或 32 字节**的字符串。以上固定密钥**仅用于解释格式，不可直接用于生产环境**。

在入站使用 `auth_required: true` 时，需要至少一个 `crypto_enable: true` 的本地账号；在出站使用 `auth_required: true` 时，还必须设置 `account` 和 `passwd`，以便向上游发送认证请求。例如：

```json
"crypto": {
  "key": "0123456789abcdef0123456789abcdef",
  "auth_required": true,
  "account": "alice",
  "passwd": "REPLACE_WITH_A_STRONG_PASSWORD"
}
```

上面的带凭据写法**仅用于出站**。入站的 `crypto` 只能设置 `key` 与 `auth_required`。

外层加密采用 `4 字节大端帧长 | 随机 nonce | AES-GCM 密文及认证标签` 的格式，普通数据按不超过 `32 KiB` 的明文分块发送。它是 enproxy 的**自定义协议，不是 TLS**；通信双方必须支持相同封装并使用相同的密钥。它可以与 HTTP 或 Shadowsocks 应用层协议叠加，但不是与任意普通 HTTP/SS 服务直接互通的标准扩展。

### `http`：HTTP 代理认证

入站：

```json
"http": { "auth_required": true }
```

必须同时提供至少一个 `proxy_enable: true` 的本地账号。无认证时可写为 `{ "auth_required": false }`。

HTTP 上游出站：

```json
"http": {
  "auth_required": true,
  "account": "upstream-user",
  "passwd": "REPLACE_WITH_UPSTREAM_PASSWORD"
}
```

请求使用 `Proxy-Authorization: Basic ...` 认证；失败时入站返回 `407 Proxy Authentication Required`。入站的 Basic 用户名和密码应通过安全网络传送，或者使用匹配的自定义外层加密通道，因为普通 HTTP 代理本身**不提供传输加密**。

### HTTP 处理边界与超时

当前 HTTP 入站按 HTTP/1.x 正向代理请求处理：普通请求要求使用绝对 URL（如 `GET http://example.com/ HTTP/1.1`），HTTPS 通常走 `CONNECT host:port` 隧道；不会对 CONNECT 内的 TLS 数据执行中间人解密。`Upgrade` 请求目前返回 `501 Not Implemented`，因此不能据此认为原生 WebSocket 升级已获得支持。

| 实现中的限制 | 当前数值或行为 |
| --- | --- |
| 入站 HTTP 请求行与请求头合计上限 | `64 KiB`，超出时返回 `431` |
| 等待下一次 HTTP 请求的空闲时间 | `90s` |
| HTTP 请求头读取超时 | `30s` |
| HTTP 请求体/响应体传输停滞超时 | `2min` |
| 上游响应头大小限制 | `64 KiB` |
| 上游 HTTP 响应头等待超时 | `30s`（普通转发使用的 Transport） |
| 上游 HTTP CONNECT 握手期限 | `15s` |
| 客户端 `Expect: 100-continue` | 普通请求提供有限支持；其余 Expect 值会被拒绝 |

这些限制主要用于保护代理免受异常大请求头或长期停滞连接的影响，不是对整个下载或长连接设置一个统一的 2 分钟总时长上限。

### `shadowsocks`：传统 AEAD

```json
"shadowsocks": {
  "method": "aes-256-gcm",
  "password": "REPLACE_WITH_A_STRONG_PASSWORD"
}
```

目前支持：`aes-128-gcm`、`aes-256-gcm`、`chacha20-ietf-poly1305`。实现采用传统 Shadowsocks AEAD TCP 格式；Shadowsocks 的 `password` 与 enproxy 外层 `crypto.key` 是两个独立参数。目前没有 Shadowsocks UDP 转发或 Shadowsocks 2022 协议实现。

### `dns`：缓存

```json
"dns": { "cache_ttl": "5m" }
```

`cache_ttl` 使用 Go 的 `time.ParseDuration` 格式，如 `30s`、`5m`、`1h`，且必须大于零。不设置 `dns` 时，不启用 enproxy 内部的 DNS 缓存；系统解析器仍然用于需要解析的主机名。

## 路由与规则文件

### 路由配置结构

```json
{
  "type": "proxy",
  "inbound": ["http-in"],
  "rule": {
    "default_outbound": "freedom",
    "policies": [
      {
        "domain": ["example.com", "domain:google.com"],
        "outbound": "proxy-up"
      },
      {
        "domain": ["ip:1.1.1.1", "file:rules/proxy.yaml"],
        "outbound": "proxy-up"
      }
    ]
  }
}
```

对于 `proxy`：

1. 先依次检查 `policies`（**第一条命中者生效**）。
2. 同一策略内的 `domain` 数组是 **OR** 关系（任意一项匹配即可）。
3. 全部未命中时走 `default_outbound`。
4. `default_outbound`、策略里的 `outbound` 可以引用任何**已配置且校验允许的**出站，或内置的 `freedom`、`blackhole`。

对于 `relay`：

```json
{
  "type": "relay",
  "inbound": ["tcp-in"],
  "rule": { "outbound": "tcp-up" }
}
```

`rule` 中的字段取决于 `type`，不能把两类规则的字段混在一起。

### `domain` 数组支持的规则格式

| 写法 | 含义 | 示例 |
| --- | --- | --- |
| `example.com` | 原有兼容写法；匹配自身及所有子域名 | `example.com`、`www.example.com` |
| `domain:example.com` | 显式域名匹配，语义与无前缀写法相同 | `domain:example.com` |
| `ip:1.1.1.1` | 匹配请求目标为该字面 IP 的情况 | `ip:1.1.1.1` |
| `file:路径` | 初始化时加载本地规则文件 | `file:rules/proxy.yaml` |
| `*` | 任意目标匹配 | `*` |

**注意：** `domain:` 用来标识一条域名匹配规则，**路由匹配阶段不会为了匹配域名而先做 DNS 查询**。`ip:` 仅匹配协议请求中传来的**字面 IP**，不会主动解析任意域名并与 IP 比较，也不会把域名替换成该 IP；该规则真正控制的是**选择哪个 outbound**。域名需要直连时，仍由出站拨号器按需进行 DNS 解析。

### 从 `Loyalsoldier/clash-rules` 读取规则

支持在 `domain` 数组中引用本地文件：

```json
{
  "domain": ["file:rules/proxy.yaml"],
  "outbound": "proxy-up"
}
```

文件路径若为相对路径，**相对于 `config.json` 所在目录**解析，而不是固定相对于进程工作目录。文件只在初始化 `route.Manager` 时读取一次：修改文件后需要重启进程才能重新加载。

支持的简化 Clash `payload:` 格式示例（`rules/proxy.yaml`）：

```yaml
payload:
  - '+.google.com'
  - '+.youtube.com'
  - 'api.example.org'
  - '1.1.1.1'
  - '203.0.113.0/24'
```

这些条目的实际匹配语义：

- `+.example.com`：匹配根域名和各级子域名。
- `example.com`：在**规则文件中**只匹配完整域名（与 JSON 内的无前缀旧规则不同）。
- `1.1.1.1`：精确 IP 匹配。
- `203.0.113.0/24`：IP CIDR 网段匹配，支持 IPv4/IPv6 前缀。

同时支持不带 `payload:` 的普通逐行规则文件（可含空行或以 `#` 开头的整行注释）：

```text
+.google.com
+.youtube.com
api.example.org
203.0.113.0/24
```

这是一种**有限格式兼容**，不是完整 YAML/Clash 配置解析器：不能直接把含有 `rules:`、`RULE-SET`、`DOMAIN-SUFFIX,...` 或复杂 YAML 结构的整个 Clash 配置文件当成域名规则文件使用。请选择仓库中内容实际符合 `payload:` 列表结构的规则文件。当前实现也不提供网络 URL 下载、自动更新或热重载。

### 匹配性能

规则加载时预编译到内存：完整域名用哈希表、后缀域名用哈希表、IP 用哈希表、CIDR 按前缀长度组织的哈希表。匹配时依次检查配置中的策略，每条策略按目标域名层级或 IP 前缀做查找，**不会逐条扫描规则文件里的所有域名**。因此比每次对规则列表做线性扫描更适合较大的域名集合；不过策略数量仍会影响单次请求的工作量。

## 典型配置

### 方案一：国内直连、指定域名走上游 HTTP 代理

假定上游 HTTP 代理地址是 `127.0.0.1:28080`。创建 `rules/proxy.yaml` 后可使用以下完整配置：

```json
{
  "log": { "level": "info", "path": "", "access": "" },
  "web": { "enabled": false, "listen": "", "user": "", "passwd": "" },
  "dns": { "cache_ttl": "5m" },
  "inbounds": [
    {
      "tag": "http-in",
      "listen": "127.0.0.1:18080",
      "protocol": "http",
      "http": { "auth_required": false },
      "rate_limit": { "up_kbps": 0, "down_kbps": 0 }
    }
  ],
  "outbounds": [
    {
      "tag": "http-up",
      "protocol": "http",
      "server": "127.0.0.1:28080",
      "http": { "auth_required": false }
    }
  ],
  "accounts": [],
  "route": [
    {
      "type": "proxy",
      "inbound": ["http-in"],
      "rule": {
        "default_outbound": "freedom",
        "policies": [
          {
            "domain": ["domain:google.com", "file:rules/proxy.yaml"],
            "outbound": "http-up"
          }
        ]
      }
    }
  ]
}
```

注意：这是“默认直连，命中规则才走上游”的实现方式，不具备自动识别全部国内/国外网站的能力；分流效果取决于实际规则内容与维护情况。确保 `127.0.0.1:28080` 确实有兼容的 HTTP 代理服务，并避免把 enproxy 的出站指向自己的监听端口形成循环。

### 方案二：给 HTTP 代理开启用户名密码认证

在方案一或最小配置基础上，将 HTTP 入站选项改为：

```json
"http": { "auth_required": true }
```

然后在顶层 `accounts` 中加入：

```json
"accounts": [
  {
    "name": "alice",
    "password": "REPLACE_WITH_A_STRONG_PASSWORD",
    "crypto_enable": false,
    "proxy_enable": true,
    "rate_limit": { "up_kbps": 512, "down_kbps": 2048 }
  }
]
```

客户端测试：

```bash
curl -x http://127.0.0.1:18080 --proxy-user 'alice:REPLACE_WITH_A_STRONG_PASSWORD' https://example.com/ -I
```

`accounts[].proxy_enable` 必须为 `true`，否则认证会失败。未认证请求会收到 HTTP `407`；不应该仅靠 Basic 认证就把明文 HTTP 代理暴露到公网。

### 方案三：建立一条端到端的固定 TCP 加密中继

以下两个配置**分别部署**在客户端一侧和服务器一侧，连接关系为：

```text
本地 TCP 客户端
  -> 127.0.0.1:19000 (本地 enproxy，tcp 入站)
  -> SERVER_IP:19001 (远端 enproxy，AES-GCM tcp 入站)
  -> target.example:443 (远端 enproxy 的固定 tcp 出站)
```

远端服务器 `server.json`：

```json
{
  "log": { "level": "info", "path": "", "access": "" },
  "web": { "enabled": false },
  "inbounds": [
    {
      "tag": "encrypted-tcp-in",
      "listen": "0.0.0.0:19001",
      "protocol": "tcp",
      "crypto": {
        "key": "0123456789abcdef0123456789abcdef",
        "auth_required": false
      },
      "rate_limit": { "up_kbps": 0, "down_kbps": 0 }
    }
  ],
  "outbounds": [
    { "tag": "target-tcp", "protocol": "tcp", "server": "target.example:443" }
  ],
  "accounts": [],
  "route": [
    {
      "type": "relay",
      "inbound": ["encrypted-tcp-in"],
      "rule": { "outbound": "target-tcp" }
    }
  ]
}
```

客户端 `client.json`：

```json
{
  "log": { "level": "info", "path": "", "access": "" },
  "web": { "enabled": false },
  "inbounds": [
    {
      "tag": "local-tcp-in",
      "listen": "127.0.0.1:19000",
      "protocol": "tcp",
      "rate_limit": { "up_kbps": 0, "down_kbps": 0 }
    }
  ],
  "outbounds": [
    {
      "tag": "encrypted-server",
      "protocol": "tcp",
      "server": "SERVER_IP:19001",
      "crypto": {
        "key": "0123456789abcdef0123456789abcdef",
        "auth_required": false
      }
    }
  ],
  "accounts": [],
  "route": [
    {
      "type": "relay",
      "inbound": ["local-tcp-in"],
      "rule": { "outbound": "encrypted-server" }
    }
  ]
}
```

把 `SERVER_IP` 与 `target.example:443` 改成真实地址，并为**两端**更换相同的强随机密钥。该示例是**固定目标端口转发**，并不会自动识别 HTTP Host、TLS SNI 或其他协议中的动态目标；也不意味着该固定目标一定支持 HTTP 正向代理。

若希望对远端 enproxy 再进行账号认证，请在服务器端 `crypto.auth_required` 设为 `true` 并创建 `crypto_enable: true` 账号；在客户端相应出站 `crypto` 中设为 `true`，同时填写匹配的 `account` / `passwd`。两侧均需使用兼容的加密封装。

### 方案四：使用 Shadowsocks 上游

在 `outbounds` 中配置：

```json
{
  "tag": "ss-up",
  "protocol": "shadowsocks",
  "server": "SS_SERVER_IP:8388",
  "shadowsocks": {
    "method": "aes-256-gcm",
    "password": "REPLACE_WITH_SS_PASSWORD"
  }
}
```

然后在 HTTP 或 Shadowsocks 入站的 `proxy` 路由中，将 `default_outbound` 或某条 `policies[].outbound` 设置为 `ss-up`。服务器必须支持相同的传统 AEAD 方法及密码；**无需**再设置外层 `crypto`，除非上游本身是专门支持该扩展的 enproxy 服务。

## 认证加密与限速

### 三套互相独立的认证/密钥机制

1. **ENPA 认证**：`crypto.auth_required` 控制。使用项目自定义 `ENPA`（版本 1）请求/响应格式；放在外层加密通道的初始化阶段，和 `accounts[].crypto_enable` 对应。
2. **HTTP Basic 认证**：`http.auth_required` 控制。用于 HTTP 代理请求，和 `accounts[].proxy_enable` 对应。
3. **Shadowsocks 密码**：配置在 `shadowsocks.password`，用于 Shadowsocks 协议自己的 AEAD 密钥派生；不是 ENPA 的用户名密码。

对于同时启用 ENPA 认证和 HTTP Basic 认证的入站，两者都需要认证成功；**限速及用户流量归属优先采用外层加密认证的账号**。同一个入站的端口级限制始终作为独立一层保留。

### 流量统计与加密开销

流量计数包装在接入连接上，发生于外层 AES-GCM 解密之前，因而统计的是**入站方向实际经过该连接的数据字节数**（可能包含帧头、nonce、认证标签和 HTTP 协议开销），而不是业务有效载荷大小。外层 ENPA 握手发生在统计包装创建之前，不包含在该计数中。HTTP Basic 认证发生在接入计数包装创建之后；认证前已读取的数据可能计为 `anonymous`。

## DNS流量统计与Web

### DNS 解析

- `OutboundDialer` 先识别字面 IP；字面 IP 直接尝试连接，不执行 DNS 查询。
- 域名通过 `dns.Resolver` 和系统默认解析器获得候选 IP；多个 IP 从轮换起点依次尝试。
- 整体连接过程设有约 `10s` 的上下文超时，单次 IP 尝试约 `3s`，系统拨号器还设有 `5s` 超时。
- 启用 DNS 缓存后，同名查询合并为一次进行中的系统查询；缓存使用配置的 TTL，过期清理周期约 `1min`。
- 当前配置**没有**指定自定义 DNS 服务器、DoH 或 DoT 的字段。

### 端口与用户流量

`stat.Manager` 每秒采样，提供：

| 统计数据 | 内容 |
| --- | --- |
| 累计统计 | 总上传/下载量，以及各监听端口、各账号的流量 |
| 近实时统计 | 最近一次 1 秒采样得到的上传/下载字节数 |
| 小时历史 | 最近 24 个小时桶（包含当前小时） |
| 日期历史 | 最近 7 个日期桶（包含今天） |
| 月份历史 | 最近 12 个月份桶（包含当月） |

历史桶按本地时区对齐。用户未通过可识别的代理认证时统计到 `anonymous`；流量数据目前驻留在**进程内存中**，服务重启后不会从持久化数据库恢复。进程正常关闭时会额外采样一次，尽量减少最后一个采样周期的漏计。

### Web 服务

在顶层开启：

```json
"web": {
  "enabled": true,
  "listen": "127.0.0.1:8088",
  "user": "admin",
  "passwd": "REPLACE_WITH_STRONG_PASSWORD"
}
```

Web 打开时 `listen`、`user` 和 `passwd` 必须有效且非空。WebServer 在 ProxyManager 构造阶段创建、在 `Start()` 中启动、在 `Close()` 中关闭。它可以获取统计管理器以及入站/账号配置。

**说明：** 本次源码摘录没有提供 `web/` 目录的路由注册、登录处理和页面文件，因此这里不列出未经核实的管理页面路径、REST API 或权限细节。安全起见应默认只绑定 `127.0.0.1`，通过受信任的安全通道远程访问，不应假设它自行提供 HTTPS。

### 日志

`log` 配置支持 `level`、`path`、`access` 字段，并对 `level` 值做合法性校验。不过**当前提供的 `enProxy.go` 入口调用的是 `common.InitLogger("enproxy", "debug")`，并未把 `config.log` 字段传给日志初始化函数**。因此不要认为修改 JSON 的 `log.level`、`log.path` 就一定会改变实际日志输出；在该入口中日志将按硬编码的 `debug` 和文件名 `enproxy` 初始化，并同时输出到控制台（日志文件可打开时）。

入站、出站、路由的创建信息及未引用警告，会在管理器初始化阶段写入日志；代理连接错误也会记录。

## 项目结构

根据本次提供的源码和已出现的 Go 导入路径，目录职责大致如下（`enProxy.go` 是主程序；上传文本中的 `proxy/` 包文件显示为相对路径）：

```text
enproxy/
├── enProxy.go                # main：读取配置路径、处理退出信号
├── go.mod                    # 构建时需要；本次摘录未附
├── config.json               # 用户自行提供
├── rules/                    # 可选：存放本地路由规则文件
└── proxy/
    ├── proxy.go              # 版本、共享日志对象
    ├── manager.go            # 生命周期及模块组装
    ├── inbound.go            # 监听、连接处理与入站装饰器
    ├── outbound.go           # 出站标签注册及协议连接
    ├── outbound_dialer.go    # DNS + TCP 拨号
    ├── account.go            # 账号管理与认证授权
    ├── auth.go               # ENPA 报文及加密通道认证
    ├── common/
    │   └── logger.go         # 日志格式及初始化
    ├── config/               # JSON 结构、解析与校验
    ├── core/                 # 双向复制、限速、AES-GCM、SS AEAD
    ├── protocol/             # TCP / HTTP / Shadowsocks 协议处理
    ├── route/
    │   ├── route.go          # 策略预编译、规则匹配与出站选择
    │   └── file.go           # 本地 payload YAML / 逐行规则读取
    ├── dns/
    │   └── resolver.go       # DNS 解析、缓存与并发合并
    ├── stat/                 # 流量累计、实时采样和历史统计
    └── web/                  # 项目依赖该包；本次未附其具体源码
```

职责划分简述：`config` 负责配置数据而不创建运行服务；`route` 只负责出站标签选择；`dns` 只负责解析；`protocol` 负责协议握手和目标提取；`outbound` 负责实际连接上游；`core` 负责通用流转、加解密与限速；`stat` 负责流量；`ProxyManager` 将这些部件组合起来。

## 使用限制与安全建议

- **仅 TCP：** HTTP CONNECT 和 Shadowsocks 当前实现主要是 TCP 流处理；没有实现 SOCKS5、Shadowsocks UDP、透明代理或路由器级流量重定向。
- **不是完整 Clash 规则引擎：** 文件规则为启动时静态加载的有限子集，不支持在线订阅自动更新；路由不进行 GeoIP/进程识别，也不会为 IP 规则主动解析目标域名。
- **自定义 AES-GCM 非标准：** 它只适用于相同封装的两端，不能作为任意上游代理的透明加密插件。
- **敏感配置：** 配置文件中可能有明文密码与对称密钥，请限制文件权限（例如 `chmod 600 config.json`），不要提交到公开仓库。
- **监听范围：** 示例默认使用回环地址，避免无认证开放代理。确需对外服务时应配合身份认证、防火墙和可信加密传输。
- **出口协议限制：** 固定 `tcp` 出站不能自动识别目标网站；`freedom` 代表由代理所在设备直接拨号，不表示一定能连通外网。
- **流量与历史：** 数据保存在内存中；统计的是入站传输字节数，不能等同于应用净载荷或外部计费流量。
- **源码覆盖：** 本次没有完整 Web 源码、依赖清单和测试报告；README 不宣称已通过端到端兼容性或安全审计。

## 常见问题

**Q：为什么出现 `inbound ... has no route`？**  
该入站被成功创建并会监听，但 `route[].inbound` 没有引用它。补充一个类型与入站协议匹配的路由，才能正常转发。

**Q：为什么提示自定义 `outbound` 没被使用？**  
说明它未被任何 `relay.outbound`、`proxy.default_outbound` 或 `proxy.policies[].outbound` 引用。属于警告，不会阻止初始化。

**Q：为什么 `ip:1.1.1.1` 没匹配 `example.com`？**  
因为路由匹配看的是代理协议携带的目标主机名/IP，不是预先解析得到的 IP。要按域名分流请使用 `domain:` 或域名规则文件。

**Q：为什么规则文件修改后没有生效？**  
文件只在创建路由管理器时读取一次。修改后需要重新启动服务。

**Q：为什么 `file:` 无法加载完整 Clash YAML？**  
读取器接受有限的 `payload:` 列表或逐行文本规则，不是通用 YAML 解析器。请选择匹配格式的规则集文件。

**Q：为什么配置了 `log.level` 但日志级别没变化？**  
当前提供的主入口将日志初始化参数硬编码为 `debug`，尚未接入 JSON 的日志配置。

**Q：为什么仅设置树莓派为默认网关并不能自动使用本 HTTP 代理？**  
普通局域网设备发送的流量不是自动符合 HTTP 正向代理协议。需要设备主动设置 HTTP 代理，或者另行部署透明流量转发/协议适配组件；当前源码未实现后者。

---

**版本：** `v0.1.0-beta`（以源码中的 `proxy.VERSION` 为准）  
**文档依据：** 2026-11-19 提供的 enproxy Go 源码摘录；未验证的组件细节均已单独标记。
