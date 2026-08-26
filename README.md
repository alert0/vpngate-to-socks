# VpnGate to SOCKS5

一个 VPN Gate 管理工具，提供节点浏览 Web 页面、OpenVPN 测试/连接、自动监测 Runner，以及可供外网客户端使用的 SOCKS5 代理。

## 当前安全模型

原生运行时默认按下面的方式工作：

```text
Internet
   │
   ├── 0.0.0.0:1080  SOCKS5
   │      └── RFC 1929 用户名/密码认证
   │
   └── 0.0.0.0:8080  Web UI
          └── 用户名/密码登录 + Session Cookie
                    │
                    ▼
          127.0.0.1:18081 Runner API
                    │
                    ▼
                 OpenVPN
                    │
                    ▼
                 VPNGate
```

- SOCKS5 可以监听公网，但非本机客户端必须使用用户名/密码认证。
- Runner 第一次启动时允许 SOCKS5 尚未配置账号密码，此时公网 SOCKS 连接会被拒绝，不会退化成匿名代理。
- 登录 Web 后可在 `/settings/socks` 设置 SOCKS5 地址、端口、用户名和密码。
- Runner 自己通过 `127.0.0.1` 使用 SOCKS5 做健康检查时允许无认证。
- Web UI 可以监听公网，但 Web 启动时强制要求 `WEB_PASSWORD`；`WEB_USERNAME` 默认是 `admin`。
- Runner 控制 API 默认只监听 `127.0.0.1:18081`，不应该直接暴露公网。
- Web 登录失败达到 5 次后会临时限制该来源 IP 的继续登录。
- Session Cookie 使用 `HttpOnly`、`SameSite=Strict`；启用 Go 原生 TLS 后同时设置 `Secure`。

> SOCKS5 用户名/密码认证解决的是“谁可以使用代理”，并不会给 SOCKS5 客户端到服务器之间的 TCP 流量自动增加 TLS 加密。

## 功能概览

- 从 VPN Gate iPhone API 拉取节点列表
- 按推荐规则排序节点
- 关键词、国家筛选
- 单节点 OpenVPN 测试
- 连接推荐节点 / 指定节点 / 断开连接
- SOCKS5 CONNECT 代理
- SOCKS5 用户名/密码认证
- Web 后台修改 SOCKS5 监听地址、端口、用户名和密码
- SOCKS5 设置热更新，无需重启 Runner
- SOCKS5 设置本地持久化，重启后继续使用
- Runner 自动连接、探活、失败隔离与重试
- Web 登录 Session 与登录失败限速
- Go 自带 HTTPS，可选，不依赖 Caddy / Nginx

## 组件

### `vpngate-web`

负责节点列表、管理页面、Runner 控制操作和 SOCKS5 后台设置。

默认：

```text
0.0.0.0:8080
```

必须设置：

```text
WEB_PASSWORD
```

### `vpngate-runner`

负责 OpenVPN 生命周期、SOCKS5、SOCKS5 配置持久化和自动监控。

默认：

```text
Runner API: 127.0.0.1:18081
SOCKS5:     0.0.0.0:1080
```

SOCKS5 用户名密码可以通过环境变量提供初始值，也可以在 Web 后台首次设置。

## 环境要求

原生运行：

- Go `1.26.1`
- OpenVPN
- Runner 需要足够的网络接口/路由权限

Linux 原生模式会使用 `ip route`、`ip rule` 等网络能力，建议以明确受控的权限运行。

## 原生运行

先复制示例配置：

```bash
cp .env.example .env
```

`.env` 已加入 `.gitignore`，不要把真实密码提交到 Git。

程序本身不会自动读取 `.env` 文件；你需要通过 shell、systemd、PowerShell 或其他进程管理器把变量注入进程。

### 启动 Runner

SOCKS 账号密码可以先不设置，之后从 Web 后台设置：

```bash
export SOCKS_LISTEN_ADDR='0.0.0.0:1080'
export RUNNER_CONTROL_ADDR='127.0.0.1:18081'
go run ./cmd/vpngate-runner
```

也可以给第一次启动提供初始账号密码：

```bash
export SOCKS_USERNAME='proxy-user'
export SOCKS_PASSWORD='replace-with-a-long-random-password'
```

如果已经在 Web 后台保存过 SOCKS5 设置，保存值会在后续启动时优先使用，环境变量只作为“还没有保存配置时”的初始默认值。

### 启动 Web

```bash
export WEB_LISTEN_ADDR='0.0.0.0:8080'
export WEB_USERNAME='admin'
export WEB_PASSWORD='replace-with-another-long-random-password'
export WEB_SESSION_TTL='12h'
export RUNNER_API_URL='http://127.0.0.1:18081'
go run .
```

访问：

```text
节点管理： http://服务器IP:8080/
SOCKS 设置：http://服务器IP:8080/settings/socks
```

## Web 后台配置 SOCKS5

登录 Web 后访问：

```text
/settings/socks
```

可以配置：

- SOCKS5 监听地址，例如 `0.0.0.0:1080`
- 端口
- 用户名
- 密码

保存行为：

- 地址和端口变更会先尝试绑定新端口；绑定失败时保留当前 SOCKS 服务不变。
- 保存成功后立即切换，无需重启 Runner。
- 密码不会回显到页面。
- 密码输入框留空表示保持当前密码。
- 首次配置时必须填写用户名和密码。
- 非本机客户端在账号密码未配置前会被拒绝。

配置默认保存在操作系统当前用户配置目录：

```text
<vpngate user config dir>/vpngate/socks.json
```

可以通过环境变量指定其他位置：

```bash
export SOCKS_CONFIG_FILE='/absolute/path/to/socks.json'
```

配置文件包含 SOCKS 密码，因此程序创建文件时会尽量设置为 `0600`，不要把它提交到 Git。

## SOCKS 客户端

后台配置完成后，客户端填写：

```text
类型: SOCKS5
服务器: 服务器公网 IP
端口: 后台设置的端口
用户名: 后台设置的用户名
密码: 后台设置的密码
```

## Go 原生 HTTPS

不需要 Caddy 或 Nginx。已有证书和私钥时设置：

```bash
export WEB_LISTEN_ADDR='0.0.0.0:8443'
export WEB_TLS_CERT='/absolute/path/server.crt'
export WEB_TLS_KEY='/absolute/path/server.key'
export WEB_USERNAME='admin'
export WEB_PASSWORD='replace-with-a-long-random-password'
go run .
```

访问：

```text
https://服务器地址:8443
```

`WEB_TLS_CERT` 和 `WEB_TLS_KEY` 必须同时设置。如果不设置，Web 会继续使用 HTTP 并在日志中提示公网明文风险。

## 环境变量

### Web

| 变量 | 默认值 | 说明 |
| --- | --- | --- |
| `WEB_LISTEN_ADDR` | `0.0.0.0:8080` | Web 监听地址 |
| `PORT` | 空 | 兼容旧配置；未设置 `WEB_LISTEN_ADDR` 时使用 |
| `WEB_USERNAME` | `admin` | Web 登录用户名 |
| `WEB_PASSWORD` | 无 | **必填**，Web 登录密码 |
| `WEB_SESSION_TTL` | `12h` | 登录 Session 有效期 |
| `WEB_TLS_CERT` | 空 | Go HTTPS 证书路径 |
| `WEB_TLS_KEY` | 空 | Go HTTPS 私钥路径 |
| `RUNNER_API_URL` | `http://127.0.0.1:18081` | Web 调用 Runner 的地址 |

### Runner / SOCKS5

| 变量 | 默认值 | 说明 |
| --- | --- | --- |
| `RUNNER_CONTROL_ADDR` | `127.0.0.1:18081` | Runner 控制接口，默认仅本机 |
| `SOCKS_LISTEN_ADDR` | `0.0.0.0:1080` | 第一次运行时的 SOCKS5 默认监听地址 |
| `SOCKS_USERNAME` | 空 | 第一次运行时可选的 SOCKS5 初始用户名 |
| `SOCKS_PASSWORD` | 空 | 第一次运行时可选的 SOCKS5 初始密码 |
| `SOCKS_CONFIG_FILE` | 系统用户配置目录 | 后台 SOCKS5 配置持久化文件路径 |
| `SOCKS_BYPASS_CIDRS` | 空 | SOCKS5 直连网段，逗号分隔 |
| `AUTO_CONNECT` | `true` | 自动连接与自动守护 |
| `MONITOR_URL` | `https://www.gstatic.com/generate_204` | HTTP 探活地址 |
| `MONITOR_FAILURE_THRESHOLD` | `3` | 连续失败阈值 |
| `TCP_PROBE_ADDRESS` | 空 | 可选 TCP 探针地址 |
| `TCP_PROBE_TIMEOUT` | `3s` | TCP 探针超时 |
| `OPENVPN_CONNECT_TIMEOUT` | `30s` | OpenVPN 建连超时 |
| `MONITOR_INTERVAL` | `20s` | 监测间隔 |
| `MONITOR_TIMEOUT` | `6s` | 监测超时 |
| `FETCH_TIMEOUT` | `30s` | 抓取节点列表超时 |
| `CONNECT_COOLDOWN` | `5s` | 自动重连冷却 |
| `MONITOR_STABLE_AFTER` | `10s` | 建连后开始稳定性监测的延时 |
| `NODE_QUARANTINE` | `5m` | 节点失败后的基础隔离时间 |
| `BYPASS_ROUTE_TABLE` | `100` | Linux 策略路由表编号 |
| `BYPASS_FWMARK` | `1` | Linux 路由标记 |

## HTTP 接口

### Web

- `GET /login`：登录页
- `POST /login`：登录
- `POST /logout`：退出登录
- `GET /health`：健康检查，保持无需登录
- `GET /settings/socks`：SOCKS5 配置页面，需要登录
- `POST /settings/socks`：保存 SOCKS5 配置，需要登录
- 其他管理页面和操作：需要有效 Session

### Runner

Runner 默认只监听 `127.0.0.1:18081`：

- `GET /health`
- `GET /status`
- `GET /config/socks`
- `POST /config/socks`
- `POST /connect`
- `POST /disconnect`
- `POST /test`

Runner API 目前不单独做账号认证，因为原生部署设计为仅本机访问。不要把 `RUNNER_CONTROL_ADDR` 改成公网地址。

## 构建与测试

```bash
go build .
go build ./...
go test ./...
go vet ./...
gofmt -l .
```

## Docker

仓库仍保留 Docker Compose 配置。当前主要使用场景是原生运行；如果使用 Docker，需要额外考虑 SOCKS 配置文件的持久化挂载。

本项目的主要安全建议仍然是：公网只开放你确实需要的 Web 与 SOCKS 端口，Runner API 保持本机或内部网络可见。

## 数据来源

VPN Gate 节点数据来自：

```text
https://www.vpngate.net/api/iphone/
```
