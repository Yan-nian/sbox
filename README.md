# sbox-panel

面向个人与小圈子自用场景的轻量级多 VPS `sing-box 1.12+` 管理面板。

采用 **Go 1.23+（纯 Go SQLite `modernc.org/sqlite`，零 CGO）+ 内嵌 React 19 / TypeScript / Vite / Tailwind Web UI** 的单一静态二进制设计，实际常驻内存约 **7–10 MiB RSS**，零外部运行时依赖。同一二进制文件同时支持 **Master 控制面** 与 **反向连接 Agent 受控端**，兼顾单机极简部署与多机集中运维。

---

## 核心特性

- **同二进制 Master + 反向连接 Agent 多 VPS 集群**
  - Master 与远端节点共用同一二进制，Agent 通过 WebSocket 主动反向连接 Master（远端 VPS 无需暴露任何管理端口）。
  - 节点离线容错：Agent 启动即自治拉起本地 `sing-box` 并持久化配置与证书，Master 重启或断网期间代理零中断，恢复后自动重连并补推流量与配置。
  - 统一控制台支持顶部节点切换、全局成员一键同步至全部在线节点、全集群 `sing-box` 并发升级，以及按节点/成员/入站维度的流量汇总。
- **`sing-box 1.12+` 核心守护与防失联在线升级**
  - 面板直接托管持久化目录下的官方独立 `sing-box` 二进制，零魔改兼容官方 Release 升级，支持 Stable / Pre-release 版本切换、历史版本降级、GitHub 下载代理与手动上传二进制。
  - 四步原子升级：`sing-box check` 预检 -> 备份 `sing-box.bak` -> 原子替换 -> 5 秒 Clash API `/version` 探活，异常自动回滚。
  - 通过本地回环 Clash API WebSocket 采集实时网速、内存、彩色日志流与活跃连接（支持一键踢断）。
- **9 大协议栈入站构建器与多成员模型**
  - 完整支持 `VLESS`（`XTLS-Vision` + `Reality` / TLS / WS / HTTPUpgrade / gRPC）、`Hysteria2`（上下行带宽、Salamander 混淆、`server_ports` 端口跳跃声明）、`TUIC v5`、`AnyTLS`、`Shadowsocks`（含 `2022-blake3` EIH 单端口多用户）、`Trojan`、`ShadowTLS v3`（一键联动回环 Detour 入站并自动合成客户端链式配置）、`Direct`（TCP/UDP 端口转发）与 `Mixed` / `SOCKS5` / `HTTP`。
  - 全局多成员凭据管理（自动生成独立 UUID、强密码、SS-2022 密钥与专属 `sub_token`），支持为单条入站配置多条外部/中转入口地址。
- **统一证书中心与 Reality 工具箱**
  - 基于 `go-acme/lego` 的 ACME HTTP-01 / DNS-01（Cloudflare、DNSPod、AliDNS 等）自动签发与后台自动续期，支持一键自签 ECDSA 证书及外部 Nginx/Caddy 证书导入。
  - 内置 `x25519` 密钥对 / ShortID 一键生成，并支持从目标节点 VPS 实测伪装域名（SNI）的 TLS 1.3 / H2 连通性与握手延迟。
- **外部上游订阅、一键 Cloudflare WARP 与可视化路由分流**
  - 自适应嗅探解析上游 **Base64 URI / Clash (Mihomo) YAML / sing-box JSON** 订阅，支持正则筛选与定时拉取，自动生成 `urltest` / `selector` 出站组并支持面板实时测速与手动切换落地节点。
  - 内置免费 **Cloudflare WARP** 账户一键注册与 WireGuard `endpoints` 生成（支持双栈或仅 IPv6 出口）。
  - 提供 AI 服务 / 流媒体 / BT 安全拦截等预设分流卡片（挂载远程 `.srs` 规则集）、自定义规则排序、完整 `config.json` 高亮预览与 RFC 7396 JSON Merge Patch 覆写。
- **多格式集群订阅与远端主动推送**
  - 内置 `{web_base_path}/sub/{sub_token}` 订阅接口，自动汇总全集群节点并按客户端 `User-Agent` 自适应返回 **Base64 URI**、**客户端 `sing-box 1.12+` JSON** 或 **Clash (Mihomo) YAML**（亦支持 `?format=base64|uri|singbox|clash` 显式指定与网页二维码扫码导入）。
  - 配置变更自动防抖推送至 **GitHub Gist**、**GitHub Repo**（支持 `merge_by_instance` 按实例区块无损增量合并，多台面板共用同一仓库/Gist 互不覆盖）与 **Sub-Store** 后端 API。
- **公网防扫描安全与 AES-256-GCM 远端加密备份**
  - 首次启动自动生成高熵随机安全路径（`web_base_path`，访问 `/` 返回无指纹 `404`），配合 Argon2id 密码哈希、登录滑动窗口限流、TOTP 2FA 及面板原生 HTTPS。
  - 支持本地一键导出/导入备份包，以及基于 **Argon2id + AES-256-GCM** 客户端加密的定时远端备份（私有 GitHub Repo / Gist，自动保留最近 N 份快照并支持网页端一键回滚恢复）。

---

## 一键部署（Linux `systemd`）

支持 Linux `amd64` 与 `arm64`，自动从 [GitHub Releases](https://github.com/Yan-nian/sbox/releases) 拉取静态二进制并校验 `checksums.txt` SHA-256。

### 1. 安装 Master 面板

```bash
curl -fsSL https://raw.githubusercontent.com/Yan-nian/sbox/main/deploy/install.sh | sudo bash
```

安装完成后脚本会自动调用 `sbox-panel show-url` 输出随机生成的安全入口 URL（例如 `http://<YOUR_IP>:18080/sbox-xxxxxxxx/`）。

常用可选参数（支持任意组合）：

```bash
curl -fsSL https://raw.githubusercontent.com/Yan-nian/sbox/main/deploy/install.sh | sudo bash -s -- install \
  --port 18080 \
  --data-dir /var/lib/sbox-panel \
  --service-name sbox-panel \
  --bin-path /usr/local/bin/sbox-panel \
  --version latest
```

- 国内网络加速：`--proxy https://ghproxy.net/`
- 私有仓库鉴权：`--gh-token <GITHUB_TOKEN>`（或环境变量 `GITHUB_TOKEN`）

### 2. 原地升级 Master（完整保留数据与配置）

```bash
curl -fsSL https://raw.githubusercontent.com/Yan-nian/sbox/main/deploy/install.sh | sudo bash -s -- upgrade
```

升级会自动复用现有 `systemd` 单元的 `--bin-path`、`--data-dir` 与监听端口，并在启动探活失败时自动回滚至旧版二进制。若服务名自定义（如 `sbox-panel-test`），附加 `--service-name sbox-panel-test` 即可。

### 3. 接入远端 Agent 节点

在 Master 面板进入 **节点管理 → 添加远程节点**，复制面板生成的一键接入命令并在远端 VPS 上执行：

```bash
curl -fsSL https://raw.githubusercontent.com/Yan-nian/sbox/main/deploy/install.sh | sudo bash -s -- agent \
  --master "https://<MASTER_HOST>:18080/<web_base_path>" \
  --token "<NODE_TOKEN>"
```

- 若 Master 使用自签 HTTPS 证书，追加 `--insecure`。
- Agent 接入凭据（`SBOX_MASTER`、`SBOX_TOKEN`）默认保存在权限为 `0600` 的 `/etc/sbox-agent.env` 中，不会暴露在进程命令行（`ps aux`）内。
- 原地升级 Agent 二进制（保持 `/etc/sbox-agent.env` 与 `/var/lib/sbox-agent` 不变）：

```bash
curl -fsSL https://raw.githubusercontent.com/Yan-nian/sbox/main/deploy/install.sh | sudo bash -s -- upgrade --service-name sbox-agent
```

### 4. 卸载

```bash
# 默认保留数据目录（/var/lib/sbox-panel 或 /var/lib/sbox-agent）
curl -fsSL https://raw.githubusercontent.com/Yan-nian/sbox/main/deploy/install.sh | sudo bash -s -- uninstall

# 如需连同数据目录一并清空，追加 --purge；卸载 Agent 时追加 --service-name sbox-agent
curl -fsSL https://raw.githubusercontent.com/Yan-nian/sbox/main/deploy/install.sh | sudo bash -s -- uninstall --service-name sbox-agent --purge
```

---

## Docker Compose 部署

项目提供多阶段构建的轻量级 Alpine 镜像（[deploy/Dockerfile](deploy/Dockerfile)）与标准编排模板（[deploy/docker-compose.yml](deploy/docker-compose.yml)），默认使用 `network_mode: host` 以便 `sing-box` 直接监听任意 TCP/UDP 端口、端口跳跃区间与 ACME HTTP-01 80 端口。

```bash
# 从源码构建并启动 Master
docker compose -f deploy/docker-compose.yml up -d --build

# 查看首次启动生成的随机安全入口 URL
docker exec sbox-panel sbox-panel show-url --data-dir /data
```

常用环境变量（可在宿主机 Shell 或同级 `.env` 文件中配置）：

| 变量名 | 默认值 | 说明 |
| :--- | :--- | :--- |
| `SBOX_IMAGE` | `sbox-panel:latest` | 镜像名称与标签 |
| `SBOX_DATA_DIR` | `/data` | 容器内持久化目录（对应 `./data:/data`） |
| `SBOX_LISTEN` | `0.0.0.0:18080` | 面板 Web 监听地址 |
| `TZ` | `UTC` | 容器时区（如 `Asia/Shanghai`） |

**Docker Agent 模式**：[deploy/docker-compose.yml](deploy/docker-compose.yml) 内已附带注释好的 `sbox-agent` 模板，使用同一镜像并通过 `command: ["agent", "--data-dir", "/data"]` 配合 `SBOX_MASTER` / `SBOX_TOKEN`（可选 `SBOX_INSECURE=true`）环境变量即可在容器内以 Agent 模式运行。

---

## SSH CLI 应急救援命令

若忘记安全路径、管理员密码、误配 2FA 或需要紧急回滚核心，可直接通过 SSH 执行内置子命令（默认数据目录为 `/var/lib/sbox-panel`，可通过 `--data-dir` 或环境变量 `SBOX_DATA_DIR` 指定）：

```bash
# 查看当前监听端口、安全路径（web_base_path）与访问 URL
sbox-panel show-url --data-dir /var/lib/sbox-panel

# 查看面板初始化状态、核心版本与备份状态
sbox-panel status --data-dir /var/lib/sbox-panel

# 重置管理员密码（省略 --password 时自动生成随机密码，默认同步关闭 TOTP 2FA）
sbox-panel reset-auth --data-dir /var/lib/sbox-panel [--username admin] [--password <new_pass>] [--disable-totp=true]

# 修改持久化监听端口（需重启服务生效）
sbox-panel set-port --data-dir /var/lib/sbox-panel --port 18080

# 从 sing-box.bak 紧急回滚核心二进制
sbox-panel rollback-core --data-dir /var/lib/sbox-panel

# 查看版本信息
sbox-panel version
```

---

## 源码构建

需要 Go 1.23+ 与 Node.js 22+：

```bash
# 1. 构建前端静态资源（产出至 web/dist，供 //go:embed 嵌入）
cd web && npm ci && npm run build && cd ..

# 2. 编译单静态二进制（支持 linux/amd64 与 linux/arm64 零 CGO 交叉编译）
make build-linux-amd64 VERSION=1.0.0
make build-linux-arm64 VERSION=1.0.0

# 3. 运行全量测试与静态检查
go vet ./...
go test -v -count=1 ./...
```
