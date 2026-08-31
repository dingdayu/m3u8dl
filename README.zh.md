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

> 🤖 **给 Agent/自动化：** 你可以自动安装并驱动此工具。仓库根目录的
> [`llms.txt`](llms.txt) 提供了紧凑、机器可读的快速入门（安装命令、参数、
> JSON 输出），直接运行 `m3u8dl --url <播放列表地址> --json` 即可。

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

GitHub Release 在国内下载通常较慢。**推荐优先使用以下有大厂背书、可验证的渠道**，而非来源不明的第三方 GitHub 加速代理。

### 方式一（推荐）：npm / npmmirror（阿里巴巴镜像）

`m3u8dl` 已发布到 npm（包名 [`@dingdayu/m3u8dl`](https://www.npmjs.com/package/@dingdayu/m3u8dl)），
自动按平台选择预编译二进制。npm 官方源在国内由
[npmmirror](https://npmmirror.com)（阿里巴巴维护）完整镜像，速度快且可信：

```bash
# 全局安装（推荐，使用 npmmirror 镜像源）
npm install -g @dingdayu/m3u8dl --registry=https://registry.npmmirror.com

# 或免安装直接运行
npx @dingdayu/m3u8dl --url https://example.com/index.m3u8 --json --registry=https://registry.npmmirror.com
```

支持平台：`darwin-arm64`、`darwin-x64`、`linux-arm64`、`linux-x64`、`win32-x64`。

### 方式二：jsDelivr CDN（Cloudflare/Fastly 背书）

npm 包内的二进制文件同时由 [jsDelivr](https://www.jsdelivr.com/) 提供 CDN
分发。可直接从 jsDelivr 下载对应平台的二进制：

```bash
# 以 v1.0.0 为例，Linux x64：
curl -fSL -o m3u8dl \
  "https://cdn.jsdelivr.net/npm/@dingdayu/m3u8dl-linux-x64@1.0.0/bin/m3u8dl"
chmod +x m3u8dl
sudo mv m3u8dl /usr/local/bin/
m3u8dl --version
```

各平台包名：`@dingdayu/m3u8dl-darwin-arm64`、`@dingdayu/m3u8dl-darwin-x64`、
`@dingdayu/m3u8dl-linux-arm64`、`@dingdayu/m3u8dl-linux-x64`、`@dingdayu/m3u8dl-win32-x64`
（Windows 下二进制为 `bin/m3u8dl.exe`）。

### 方式三：go install + goproxy.cn（七牛云背书）

源码构建可通过 [goproxy.cn](https://goproxy.cn)（七牛云维护）加速模块下载：

```bash
GOPROXY=https://goproxy.cn,direct go install github.com/dingdayu/m3u8dl@latest
```

### 方式四：gh CLI 官方 API

若已登录 GitHub 并安装 [gh](https://cli.github.com)，可直接走官方 API 下载（相对稳定）：

```bash
gh release download v1.0.0 --repo dingdayu/m3u8dl --pattern 'm3u8dl_*_Linux_x86_64.tar.gz'
```

> ⚠️ **关于第三方 GitHub 加速代理**（如各类 ghproxy/ghfast 镜像站）：这些服务
> 由匿名个人/社区维护，**无法保证文件未被篡改**。如确需使用，请务必按下方
> 「验证下载」一节完成签名校验。

### 验证下载（强烈建议）

无论通过哪种渠道下载，发布产物均带有以下可验证信息：

1. **SHA-256 校验和** — 每个 Release 附带 `checksums.txt`（归档的 SHA-256）：

   ```bash
   sha256sum -c checksums.txt --ignore-missing
   ```

2. **GitHub Artifact Attestations** — Release 的**归档文件**（`.tar.gz` /
  `.zip`）带有 GitHub 官方构建证明，一条命令即可确认由本仓库的 release
  工作流构建：

   ```bash
   gh attestation verify m3u8dl_v1.0.0_Linux_x86_64.tar.gz --repo dingdayu/m3u8dl
   ```

3. **独立二进制（npm / jsDelivr）** — 从 jsDelivr 直接下载（或从 npm 解出）
  的裸二进制不属于 attested 产物。请对照随 Release 发布、且本身已 attest
  的 `bin-checksums.txt` 进行校验：

  ```bash
  curl -fSLO https://github.com/dingdayu/m3u8dl/releases/download/v1.0.0/bin-checksums.txt
  sha256sum -c bin-checksums.txt --ignore-missing
  # 可选：证明该清单确实来自本仓库的 release 构建：
  gh attestation verify bin-checksums.txt --repo dingdayu/m3u8dl
  ```

4. **npm provenance 与自检** — npm 包发布时附带来源证明（见包页面的
  provenance 徽章，或本地 `npm audit signatures`）。此外，`m3u8dl` 启动器
  会在**每次运行时**将平台二进制的 SHA-256 与打包时记录的摘要比对，若
  下载被损坏或篡改则明确报错并中止：

   ```bash
   npm audit signatures
   ```

> 归档命名说明：Releases 上的档案按 `m3u8dl_<版本>_<OS>_<ARCH>.tar.gz`（Windows 为 `.zip`）
> 命名，其中 `<OS>` 为 `Linux` / `Darwin` / `Windows`，amd64 写作 `x86_64`。
> 例如 `m3u8dl_v1.0.0_Linux_x86_64.tar.gz`。

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
{
  "ok": true,
  "path": "/data/videos/movie.mp4",
  "duration_sec": 12.34,
  "url": "https://...",
  "name": "movie",
  "mode": "single"
}
```

失败时：

```json
{ "ok": false, "error": "download failed ...", "mode": "single" }
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
make install-hooks    # 安装 git 提交钩子（lefthook）
make release          # 运行 goreleaser（需安装 goreleaser）
```

### 提交钩子（Git Hooks）

本仓库使用 [Lefthook](https://lefthook.dev)（单一 Go 二进制）管理 git hooks，确保代码风格一致：

| 工具                 | 作用                                                                                            |
| -------------------- | ----------------------------------------------------------------------------------------------- |
| `make install-hooks` | 安装 lefthook 并设置 `pre-commit` + `commit-msg` 钩子                                           |
| `pre-commit`         | 提交前自动检查/修复：`gofmt`、`go vet`、末尾换行、尾随空白、LF 行尾、YAML/JSON 验证、大文件检查 |
| `commit-msg`         | 强制 [Conventional Commits](CONTRIBUTING.md#commit-messages) 提交消息                           |
| `lefthook.yml`       | 钩子配置（并行执行、自动 stage 修复）                                                           |
| `.gitattributes`     | 统一文本文件 LF 行尾、二进制声明，配合 Linguist                                                 |
| `.editorconfig`      | 跨编辑器统一缩进/编码/行尾                                                                      |

---

## 路线图

- [ ] 补充 CLI shell 自动补全文档/脚本
- [ ] 发布 Homebrew tap
- [ ] 使用 mock m3u8 服务器做端到端测试
- [ ] 支持 HTTP/2、Range 请求与限速

---

## 许可证

[MIT](LICENSE) © [dingdayu](https://github.com/dingdayu)
