# GithubDownloader

一个用于下载 GitHub 文件和目录的小工具，支持 GitHub 原链接与常见镜像链接。

## 功能特性

- 支持 GitHub 原链接与镜像链接输入
- 首次运行自动测速，选择最快线路并保存配置
- 支持单文件下载
- 支持子目录递归下载（包含所有子文件和子目录）
- 镜像下载失败时自动回退直连

## 支持的链接类型

- 单文件（blob）
  - `https://github.com/<owner>/<repo>/blob/<branch>/<path/to/file>`
- 子目录（tree）
  - `https://github.com/<owner>/<repo>/tree/<branch>/<path/to/dir>`
- 原始文件（raw）
  - `https://raw.githubusercontent.com/<owner>/<repo>/<branch>/<path/to/file>`
- 镜像链接
  - 在镜像站后拼接原 GitHub 链接，例如：
  - `https://ghfast.top/https://github.com/<owner>/<repo>/blob/<branch>/<path/to/file>`

## 快速开始

### 1. 编译

```bash
go build -o GithubDownloader .
```

### 2. 运行

```bash
GithubDownloader <GitHub链接或镜像链接> [输出目录]
```

示例：

```bash
# 下载单文件（输出到当前目录）
GithubDownloader "https://github.com/golang/go/blob/master/README.md"

# 下载子目录到 out 目录
GithubDownloader "https://github.com/golang/go/tree/master/src" "./out"
```

## 首次运行配置

首次运行会进行测速，并在当前目录生成配置文件：

- `.ghdownloader.json`

配置中包含：

- `best_mirror`：当前最快线路
- `benchmarks_ms`：各线路测速结果（毫秒）
- `saved_at`：保存时间

## GitHub Actions 自动构建

仓库已提供自动构建工作流：

- [build.yml](.github/workflows/build.yml)

触发方式：

- `push` 到 `main`
- `pull_request` 到 `main`
- 手动触发 `workflow_dispatch`
- 推送 tag（`v*`）时会自动创建 Release 并上传构建产物

构建产物：

- `GithubDownloader_linux_amd64`
- `GithubDownloader_windows_amd64.exe`
- `GithubDownloader_darwin_amd64`

## 注意事项

- 目录下载依赖 GitHub API 列目录，若仓库较大或频繁请求可能遇到限流
- 私有仓库当前未集成 Token 鉴权
- 若镜像不可用会自动回退到直连下载
