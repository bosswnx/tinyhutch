# TinyHutch

[English](README.md) | **中文**

[![Release](https://img.shields.io/github/v/release/bosswnx/tinyhutch?sort=semver)](https://github.com/bosswnx/tinyhutch/releases/latest)
[![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)
[![Go 1.25+](https://img.shields.io/badge/go-1.25%2B-00ADD8.svg)](https://go.dev/)

TinyHutch 是一个**简单、单二进制、零依赖、自托管**的网页文件管理器。指定一个目录，就能在浏览器里上传、重命名、移动、下载和分享其中的文件，并能为单个文件或整个目录设置独立的访问密码。没有数据库，没有运行时，也没有需要额外安装的依赖。

![TinyHutch 文件管理器](img.png)

## 为什么选 TinyHutch

- **一个文件，零依赖**：约 7 MB 的 Go 可执行文件，支持 Linux 和 macOS（amd64/arm64），不需要 Node、PHP、Python，也不需要数据库。
- **简单**：`./tinyhutch /srv/files` 就能跑起来，首次启动自动生成 `config.json`。
- **按路径加锁**：可以为单个文件或整个目录树设置独立密码，解锁一个路径不会连带解锁其他路径。
- **方便分享**：复制任意文件的下载链接，或把整个目录打包成 zip 下载；链接可以直接用于脚本和 `curl`。
- **用着舒服**：移动端适配、深色模式、中英文界面。

## 快速开始

从 [Releases](https://github.com/bosswnx/tinyhutch/releases/latest) 下载对应平台的二进制文件，然后：

```bash
chmod +x tinyhutch_linux_amd64
./tinyhutch_linux_amd64 /path/to/files
```

TinyHutch 默认将当前目录作为工作目录，监听 `127.0.0.1:7500`，并在工作目录下生成 `config.json`。

```bash
./tinyhutch                      # 默认将当前目录作为工作目录
./tinyhutch /srv/files           # 指定工作目录
./tinyhutch --config my.json     # 指定配置文件
```

打开 <http://127.0.0.1:7500>。

> 部署在远程服务器上时，建议把 `listen_host` 设为 `0.0.0.0`，并放在 TLS 反向代理之后；生产环境请设置 `admin_password`，并保持 `insecure_cookie` 为 `false`。

## Docker 部署

仓库自带 `Dockerfile` 和 `docker-compose.yml`，一条命令即可启动：

```bash
docker compose up -d --build
docker compose logs tinyhutch   # 首次启动会打印自动生成的管理员密码
```

（`make docker-up` 是同样命令的快捷方式。）

打开 <http://localhost:7500>。文件存放在 `./data`，首次启动会生成 `./data/config.json`；`./data/.tinyhutch.secret` 是会话密钥。

首次启动前如需设置管理员密码、宿主机端口、上传上限或路径加锁规则，可以复制并修改 `.env.example`：

```bash
cp .env.example .env
docker compose up -d --build
```

环境变量只在生成 `config.json` 时生效。之后请直接编辑 `./data/config.json`（TinyHutch 会自动重新加载，修改 `listen_host`/`listen_port` 需重启容器）。管理员密码也可以在网页 `/admin` 中修改。

放在 HTTPS 反向代理之后时，请设置 `TINYHUTCH_TRUST_PROXY=true`、`TINYHUT_INSECURE_COOKIE=false`（可选 `TINYHUT_BIND=127.0.0.1`，只允许代理访问该端口），并把代理的请求体上限调到不小于 `max_upload_mb`（nginx 示例：`client_max_body_size 4096m;`）。

常用命令：

```bash
docker compose logs -f tinyhutch
docker compose restart
docker compose down
```

容器启动后会降权到 uid/gid `1000` 运行。如果 `./data` 对该用户不可写（Linux 绑定挂载常见），执行 `sudo chown -R 1000:1000 ./data`，或在 compose 文件中设置 `user:`。

## 配置

TinyHutch 默认从工作目录读取 `config.json`，也可以用 `--config` 指定。**所有配置项必须都存在，否则无法启动。** 模板见 [`config.example.json`](config.example.json)。

| 字段 | 说明 |
|---|---|
| `site_name` | 浏览器标题和顶栏名称 |
| `listen_host` | 监听地址 |
| `listen_port` | 监听端口 |
| `admin_password` | 管理密码；为空时点击登录即可进入 |
| `session_hours` | 会话有效期（小时） |
| `max_upload_mb` | 单文件上传上限（MB） |
| `show_hidden` | 显示隐藏文件 |
| `log_level` | 日志级别 |
| `trust_proxy` | 信任 `X-Forwarded-For`；仅放在可信反向代理后开启 |
| `insecure_cookie` | 本地 HTTP 调试时使用非 Secure Cookie；生产部署保持 `false` |
| `rules` | 权限规则列表 |

TinyHutch 会自动监听 `config.json` 的变化并重新加载；修改 `listen_host`、`listen_port` 需要重启。

### 给路径加锁

在 `rules` 中为指定路径设置密码：

```json
"rules": [
  { "path": "/private", "password": "secret" },
  { "path": "/reports/q4.pdf", "password": "another-secret" }
]
```

锁定的目录会同时锁定其下所有内容，规则路径之间不能重叠。解锁某个路径只会在当前会话中解锁该路径及其子路径。

## 构建

需要 Go ≥ 1.25。

```bash
make build    # 本机二进制 dist/tinyhutch
make test     # 单元 + 集成测试
make dist     # linux/amd64、linux/arm64、darwin/amd64、darwin/arm64
make snapshot # 本地 GoReleaser 试构建，输出到 .goreleaser/
```

## 许可证

[MIT](LICENSE)
