# 一键部署（Linux / systemd / 非 Docker）

本项目可以直接使用 `deploy.sh` 在 Linux 服务器上完成原生部署，不依赖 Docker、Caddy 或 Nginx。

## 支持范围

- Ubuntu / Debian（`apt`）
- Fedora / Rocky / Alma / RHEL 系（`dnf` / `yum`，前提是仓库中可安装 OpenVPN）
- CPU：`amd64` / `arm64`
- init：systemd

脚本会自动：

1. 安装 `OpenVPN`、`Git`、`curl`、`iproute2/iproute`。
2. 拉取 `alert0/vpngate-to-socks` 最新 `main`。
3. 按 `go.mod` 下载项目专用 Go，不修改系统现有 Go。
4. 编译 `vpngate-web` 与 `vpngate-runner`。
5. 创建独立 Web 服务账号 `vpngate`。
6. 生成 Web 与 SOCKS5 初始随机密码。
7. 安装并启动两个 systemd 服务。
8. 设置开机自启。
9. 如果检测到已启用的 UFW/firewalld，则放行 Web 与 SOCKS 默认 TCP 端口。
10. 保留以后在 Web 后台保存的 SOCKS5 配置。

## 一条命令安装

合并到 `main` 后可以直接运行：

```bash
curl -fsSL https://raw.githubusercontent.com/alert0/vpngate-to-socks/main/deploy.sh | sudo bash
```

完成后脚本会输出：

- Web 后台地址
- Web 用户名 / 初始随机密码
- SOCKS5 地址
- SOCKS5 用户名 / 初始随机密码

默认端口：

```text
Web:     5777/tcp
SOCKS5:  5888/tcp
Runner:  127.0.0.1:18081（仅本机）
```

SOCKS5 后台配置入口：

```text
http://服务器IP:5777/settings/socks
```

## 自定义初始配置

例如：

```bash
curl -fsSL https://raw.githubusercontent.com/alert0/vpngate-to-socks/main/deploy.sh -o /tmp/vpngate-deploy.sh
sudo WEB_USERNAME=admin2 \
  WEB_PASSWORD='YourWebPassword' \
  WEB_LISTEN_ADDR='0.0.0.0:9000' \
  SOCKS_USERNAME='proxy01' \
  SOCKS_PASSWORD='YourSocksPassword' \
  SOCKS_LISTEN_ADDR='0.0.0.0:1088' \
  bash /tmp/vpngate-deploy.sh
```

可用变量：

| 变量 | 默认值 | 说明 |
| --- | --- | --- |
| `REPO_REF` | `main` | 部署的 Git 分支/Tag |
| `INSTALL_ROOT` | `/opt/vpngate` | 安装目录 |
| `CONFIG_DIR` | `/etc/vpngate` | 配置目录 |
| `WEB_LISTEN_ADDR` | `0.0.0.0:5777` | Web 监听地址 |
| `WEB_USERNAME` | `admin` | Web 初始用户名 |
| `WEB_PASSWORD` | 随机生成 | Web 初始密码 |
| `WEB_SESSION_TTL` | `12h` | Session 时长 |
| `SOCKS_LISTEN_ADDR` | `0.0.0.0:5888` | SOCKS5 初始监听地址 |
| `SOCKS_USERNAME` | `proxy` | SOCKS5 初始用户名 |
| `SOCKS_PASSWORD` | 随机生成 | SOCKS5 初始密码 |
| `OPEN_FIREWALL` | `1` | 自动处理已启用的 UFW/firewalld；设 `0` 可关闭 |
| `WEB_TLS_CERT` | 空 | Go 原生 HTTPS 证书路径 |
| `WEB_TLS_KEY` | 空 | Go 原生 HTTPS 私钥路径 |

## 升级

再次执行同一条部署命令即可。

脚本会：

- 拉取最新代码；
- 重新编译；
- 重启服务；
- 保留 `/etc/vpngate/runner.env`；
- 保留 `/etc/vpngate/web.env`；
- 保留 Web 后台已经写入的 SOCKS5 持久化配置。

因此日常升级不会重新生成密码，也不会强行把已有自定义端口改回默认值。

## systemd

查看状态：

```bash
systemctl status vpngate-runner vpngate-web
```

查看实时日志：

```bash
journalctl -u vpngate-runner -u vpngate-web -f
```

重启：

```bash
systemctl restart vpngate-runner vpngate-web
```

## 配置位置

```text
/etc/vpngate/runner.env
/etc/vpngate/web.env
/etc/vpngate/socks.json
```

`/etc/vpngate/socks.json` 是 Web 后台保存 SOCKS5 配置后由 Runner 创建的持久化文件，权限为 `0600`。

## 防火墙 / 云安全组

脚本只能处理服务器本机已经启用的 UFW/firewalld。

如果服务器来自 AWS、Google Cloud、Azure、阿里云、腾讯云或其他 VPS 服务商，还需要在云控制台安全组放行：

```text
TCP 5777
TCP 5888
```

如果以后通过 Web 后台修改 SOCKS5 端口，也要同步修改本机防火墙和云安全组。

Runner 的 `18081` 不要放公网。

## HTTPS

不使用 Caddy/Nginx。已有证书时可以直接让 Go 提供 HTTPS：

```bash
curl -fsSL https://raw.githubusercontent.com/alert0/vpngate-to-socks/main/deploy.sh -o /tmp/vpngate-deploy.sh
sudo WEB_LISTEN_ADDR='0.0.0.0:8443' \
  WEB_TLS_CERT='/path/server.crt' \
  WEB_TLS_KEY='/path/server.key' \
  bash /tmp/vpngate-deploy.sh
```

未设置 TLS 时，Web 默认是 HTTP；如果 Web 暴露到公网，登录密码和 Session 在传输层没有 HTTPS 保护。
