# TinyFM

[English](README.md) | **中文**

TinyFM 是一个单二进制、零依赖、自托管、极简的文件管理器，可以将某一目录内的文件暴露在浏览器界面中进行访问和修改，并配有简单的访问控制。

TinyFM 有以下特色：

- 一键单文件部署，完全无依赖，性能极佳；
- 能独立控制每个目录/文件的访问权限；
- 能通过链接下载文件，适配脚本。
- 移动端适配、深色模式、中英文适配；

## 快速开始

```bash
# 默认将当前目录作为工作目录
./tinyfm

# 指定工作目录
./tinyfm <path>

# 指定配置文件
./tinyfm --config <config>
```

## 配置

若未指定配置文件，TinyFM 默认在工作目录下读取 `config.json` 进行配置。若未找到配置文件，则会创建一个全新的配置文件。配置文件更新后，需要重新启动以加载最新配置。

**注意：所有配置项必须都存在，否则无法启动。**

配置文件模板为 `config.example.json`，内容如下：

| 字段 | 说明 |
|---|---|
| `site_name` | 浏览器标题和顶栏名称 |
| `listen_host` | 监听地址 |
| `listen_port` | 监听端口 |
| `admin_password` | 管理密码；为空时则点击直接登录 |
| `session_hours` | 会话有效期（小时）|
| `max_upload_mb` | 单文件上传上限（MB） |
| `show_hidden` | 显示隐藏文件 |
| `log_level` | 日志级别 |
| `trust_proxy` | 信任 `X-Forwarded-For`；仅放在可信反向代理后开启 |
| `insecure_cookie` | 本地 HTTP 调试时使用非 Secure Cookie；生产部署保持 `false` |
| `rules` | 权限规则列表 |

## 构建

需要 Go ≥ 1.25。

```bash
make build    # 本机二进制 dist/tinyfm
make test     # 单元 + 集成测试
make dist     # linux/amd64、linux/arm64、darwin/arm64
```

