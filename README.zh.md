# m3u8dl — 多线程 m3u8 视频下载器

> 用 Go 编写的高性能多线程 m3u8 视频下载器。
>
> 🌐 其他语言： **[English](README.md)**

它解析 m3u8 播放列表，**并发下载所有 TS 切片**，并合并为单个 MP4 —— 支持
**AES 解密**、**嵌套 m3u8（Master Playlist）**、**广告/重复切片去重** 与
**断点续传**。合并时优先调用系统 `ffmpeg` 无损合并，缺失时回退内置合并。

CLI 基于 [cobra](https://github.com/spf13/cobra)：语义化长参数 + 短别名，
支持 `--help` / `--json` / `--version` 与 shell 自动补全。对人、LLM 与 Agent
工作流都很友好。

---

## 功能特性

- ⚡ 多线程下载 TS 切片，实时进度条 + 网速显示
- 🔐 AES-128 CBC 解密（支持每切片独立密钥，缺省时复用 key 前 16 字节作 IV）
- 🪆 嵌套 m3u8 / Master Playlist 递归展开
- 🛡️ 广告 / 重复切片按内容哈希（MD5）去重
- ⏯️ 断点续传 —— 重跑同一命令自动补齐缺失切片
- 🎞️ 优先 `ffmpeg` 无损 concat 合并，内置合并兜底
- 🤖 机器友好的 `--json` 输出；日志/进度走 stderr，结果走 stdout
- 📦 批量下载（URL 列表文件）
- ✅ 兼容旧版参数（`-ht`/`-sp`/`-pd`/`-ua`）

---

## 安装

从 [Releases](../../releases) 页面下载预编译二进制，或：

### 方式 A — `go install`（需 Go 1.27+）

```bash
go install github.com/dingdayu/m3u8dl@latest
```

### 方式 B — 从源码构建

```bash
git clone https://github.com/dingdayu/m3u8dl.git
cd m3u8dl
make build          # 生成 ./bin/m3u8dl
```

### 方式 C — Homebrew（发布 tap 后可）

```bash
# brew install dingdayu/tap/m3u8dl
```

---

## 国内加速下载

GitHub Release 在国内下载通常较慢，推荐以下加速方式。

### 1. 使用 GitHub 加速镜像（无需登录）

将 Release 下载链接中的 `github.com` 替换为镜像域名，例如：

```bash
# 镜像前缀（任选可用者）：
#   https://ghproxy.net/            https://gh-proxy.com/
#   https://ghfast.top/             https://mirror.ghproxy.com/

m3u8dl --version   # 先获取最新版本号

# 以 v1.0.0 为例，Linux amd64：
curl -fSL -o m3u8dl \
  "https://ghfast.top/https://github.com/dingdayu/m3u8dl/releases/download/v1.0.0/m3u8dl_v1.0.0_linux_amd64.tar.gz"

tar -xzf m3u8dl_v1.0.0_linux_amd64.tar.gz
chmod +x m3u8dl
sudo mv m3u8dl /usr/local/bin/
```

> 镜像域名可能变动，请以当前可用者为准；`ghfast.top`、`ghproxy.net` 等均为社区维护的下载加速代理。

### 2. 使用 `gh` CLI 与 GitHub 官方加速（`ghproxy`）

若已登录 GitHub 并安装 [gh](https://cli.github.com)，直接用 gh 下载（走官方 API，相对稳定）：

```bash
gh release download v1.0.0 --repo dingdayu/m3u8dl --pattern 'm3u8dl_*_linux_amd64.tar.gz'
```

### 3. 一键脚本示例（Linux/macOS, amd64/arm64）

```bash
#!/usr/bin/env bash
# 安装 m3u8dl 最新版（含国内镜像加速）
set -euo pipefail
VERSION="${1:-latest}"
ARCH="$(uname -m)"
case "$(uname -s)" in
  Linux)  OS="Linux" ;;
  Darwin) OS="Darwin" ;;
  *) echo "Unsupported OS"; exit 1 ;;
esac
case "$ARCH" in
  x86_64) GOARCH="amd64" ;;
  arm64)  GOARCH="arm64" ;;
  *) echo "Unsupported arch: $ARCH"; exit 1 ;;
esac

if [ "$VERSION" = "latest" ]; then
  VERSION=$(curl -fsSL https://api.github.com/repos/dingdayu/m3u8dl/releases/latest | grep '"tag_name"' | head -1 | sed 's/.*"v\(.*\)",/\1/')
fi

# 镜像前缀：需可访问 GitHub，可直接写 https://github.com
MIRROR="https://ghfast.top"
URL="$MIRROR/https://github.com/dingdayu/m3u8dl/releases/download/v${VERSION}/m3u8dl_v${VERSION}_${OS}_${GOARCH}.tar.gz"

echo "Downloading m3u8dl v${VERSION} (${OS}/${GOARCH}) ..."
TMP=$(mktemp -d)
curl -fSL -o "$TMP/m3u8dl.tar.gz" "$URL"
tar -xzf "$TMP/m3u8dl.tar.gz" -C "$TMP"
sudo mv "$TMP/m3u8dl" /usr/local/bin/m3u8dl
rm -rf "$TMP"
m3u8dl --version
echo "Installed: /usr/local/bin/m3u8dl"
```

---

## 快速开始

```bash
# 下载单个 m3u8
m3u8dl -u https://example.com/index.m3u8

# 自定义输出文件名与 32 线程
m3u8dl -u https://example.com/index.m3u8 -o 我的视频 -n 32

# 结构化 JSON 输出（编程/LLM/Agent 使用）
m3u8dl --url https://example.com/index.m3u8 --threads 16 --json

# 批量下载
m3u8dl --list urls.txt --save-path /data/videos
```

---

## 用法

```
m3u8dl [flags] <m3u8-url> [outputName]

Flags:
  -u, --url string        m3u8 下载地址 (http(s)://url/xx/index.m3u8)
  -n, --threads int       下载线程数 (默认 24)
      --host-type string  host 解析方式 v1|v2|auto [旧参数 -ht] (默认 "v2")
  -o, --output string     输出文件名（不带后缀，默认 movie）
  -c, --cookie string     请求 Cookie
  -r, --referer string    Referer 请求头（默认取 m3u8 的 host）
  -t, --timeout int       每个请求超时秒数 (默认 120)
      --save-path string  文件保存目录 [旧参数 -sp]
      --user-agent string User-Agent 请求头 [旧参数 -ua]
  -s, --insecure          允许不安全的 TLS 请求
      --purge-dup         广告/重复切片去重 [旧参数 -pd]
      --clean-ts          合并成功后自动清除 ts 目录 (默认 true)
  -j, --json              输出结构化 JSON
  -l, --list string       批量下载列表文件（每行一个 m3u8 地址）
  -H, --header strings    自定义请求头，可重复，格式 "Key: Value"
  -h, --help              help for m3u8dl
      --version           version for m3u8dl
```

### host 类型

部分服务器对相对 TS 路径的解析方式不同，`--host-type` 用于选择策略：

- `v2`（默认）— 以 `scheme://host` 解析 TS
- `v1` — 以 `scheme://host/m3u8 所在目录` 解析 TS
- `auto` — 用 `v1`，下载失败时回退 `v2`

### JSON 输出

搭配 `--json` 时，所有结果输出到 **stdout**（单个 JSON 对象）：

```json
{"ok":true,"path":"/data/videos/movie.mp4","duration_sec":12.34,"url":"https://...","name":"movie","mode":"single"}
```

失败时：

```json
{"ok":false,"error":"download failed ...","mode":"single"}
```

日志、进度、告警走 **stderr**，可安全只解析 stdout 的 JSON。

---

## 示例

### 续传失败的下载

若部分切片失败，ts 目录会被保留。重跑同一命令会自动补齐缺失切片。

### 去除广告切片

```bash
m3u8dl -u https://example.com/index.m3u8 --purge-dup
```

### 下载加密播放列表

AES-128 密钥会从 `#EXT-X-KEY` 自动识别。若密钥无显式 IV，则取 key 前 16 字节作为 IV。

---

## 开发

贡献指南见 [CONTRIBUTING.md](CONTRIBUTING.md)，Agent 友好引导见 [AGENTS.md](AGENTS.md)。

```bash
make build            # 构建到 ./bin/m3u8dl
make test             # 运行测试
make lint             # 运行 golangci-lint
make install-hooks    # 安装 git 提交钩子（代码风格/规范检查）
make release          # 运行 goreleaser（需安装 goreleaser）
```

### 提交钩子（Git Hooks）

本仓库内置了提交钩子与统一风格工具，解决不同编辑器差异（文件末尾换行、尾随空白、行尾符不一致）以及代码协同风格规范问题：

| 工具 | 作用 |
| --- | --- |
| `make install-hooks` | 安装 git hooks（`core.hooksPath` → `.hooks/`） |
| `.hooks/pre-commit` | 提交前自动检查/修复：`gofmt`、`go vet`、末尾换行、尾随空白、LF 行尾 |
| `.hooks/commit-msg` | 强制 [Conventional Commits](CONTRIBUTING.md#commit-messages) 提交消息 |
| `pre-commit` 框架 | 跨插件规范（`end-of-file-fixer`、`trailing-whitespace`、`check-yaml` 等），见 `.pre-commit-config.yaml` |
| `.gitattributes` | 统一文本文件 LF 行尾、二进制声明，配合 Linguist |
| `.editorconfig` | 跨编辑器统一缩进/编码/行尾 |

```bash
pip install pre-commit   # 可选：启用 pre-commit 框架
pre-commit install
pre-commit run --all-files
```

---

## 路线图

- [ ] 补充 CLI shell 自动补全文档/脚本
- [ ] 发布 Homebrew tap
- [ ] 使用 mock m3u8 服务器做端到端测试
- [ ] 支持 HTTP/2、Range 请求与限速

---

## 许可证

[MIT](LICENSE) © [dingdayu](https://github.com/dingdayu)
