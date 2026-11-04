<div align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset=".github/logo-dark.svg">
    <source media="(prefers-color-scheme: light)" srcset=".github/logo-light.svg">
    <img alt="mdserve" src=".github/logo-light.svg" width="360">
  </picture>
</div>

<div align="center">

[![License: MIT][license-shield]][license-url]
[![Latest release][release-shield]][release-url]
[![CI][ci-shield]][ci-url]
[![Go 1.26][go-shield]][go-url]
[![Platforms][platform-shield]](#安装)

</div>

<div align="center">
  <a href="#快速开始">快速开始</a> &middot;
  <a href="#用法">用法</a> &middot;
  <a href="docs/configuration.md">配置</a> &middot;
  <a href="docs/security.md">安全</a> &middot;
  <a href="AGENTS.md">Agent 配置</a> &middot;
  <a href="README.md">English</a>
</div>

> 把一个 Markdown 目录变成需要口令的网站。单个二进制，无需构建，无需数据库。

---

## 为什么是 mdserve

文档本来就是 Markdown。想把它放到一个 URL 上，通常要引入静态站点生成器、构建步骤和部署流水线；
换成 wiki 又要多一个数据库和一个你并不需要的编辑器；文件浏览器给你一棵文件树，而不是一篇能读的页面。

mdserve 直接服务这个目录：一条命令、一个口令，需要离开本机时再挂一条 Cloudflare 隧道。
它是给小型、可信的读者群用的——团队手册、客户文档、个人笔记——不是 wiki、文件管理器或多租户服务。

## 特性

- **不用构建就能发布。** 指向一个目录，Markdown 在请求时渲染。改完文件刷新页面即可。
- **一个进程，多个站点。** 按域名路由，每个站点有自己的目录和口令；域名不认识就返回 `421`，
  不会串到别的站点。
- **分发的是访问权，不是口令。** 生成一小时内有效、只能用一次的分享码；
  `mdserve revoke` 一次性让所有会话失效。
- **设计上只读。** 不往目录里写任何东西；不直出 HTML 文件；内联 HTML 会被消毒；
  SVG 强制下载而不是在页面里渲染。
- **自包含。** CSS、字体、Mermaid 全部内嵌并预压缩。没有 CDN、运行时零下载，
  在不能出网的机器上也能用。
- **为阅读而做。** 服务端渲染目录树、TOC 与全文检索；关掉 JavaScript 也能读。

## 什么时候用

| 适合 | 不适合 |
| --- | --- |
| 有一个 Markdown 目录，只有少量读者 | 需要上传、WebDAV 或文件管理 |
| 想按受众分口令，而不是维护用户库 | 需要按人授权、SSO 或审计日志 |
| 机器可以躲在隧道后面，不开放端口 | 文档成千上万，需要相关性排序检索 |

不适合的场景可以换成 dufs、miniserve、filebrowser；需要按人授权时，在 mdserve 前面加一层
Cloudflare Access。

## 快速开始

```bash
cd ~/notes
mdserve serve
```

它会打印本地地址和自动生成的口令，口令保存在 `~/.local/state/mdserve/passwords/`（权限 `0600`）。
打开地址、输入口令即可。

想自己定口令：

```bash
mdserve passwd   # 输出明文口令，以及可填进 auth.password_hash 的 argon2id 哈希
```

## 安装

```bash
# Linux x86_64 —— 换成对应平台的资产名
VER=$(curl -fsSL https://api.github.com/repos/Fel1xKan/mdserve/releases/latest \
      | sed -n 's/.*"tag_name": *"v\([^"]*\)".*/\1/p')
curl -fsSL -o mdserve \
  "https://github.com/Fel1xKan/mdserve/releases/latest/download/mdserve-$VER-linux-amd64"
install -m 755 mdserve ~/.local/bin/mdserve
```

有 Go 1.26 或更新版本时：

```bash
go install github.com/Fel1xKan/mdserve@latest
```

| 平台 | 资产 |
| --- | --- |
| Linux | `mdserve-<version>-linux-{amd64,arm64}` |
| macOS | `mdserve-<version>-darwin-{arm64,amd64}` |
| Windows | `mdserve-<version>-windows-{amd64,arm64}.exe` |

macOS 首次运行会拦下未签名二进制：`xattr -d com.apple.quarantine ./mdserve`。

只有两个可选依赖：用隧道时需要 `cloudflared`，从源码构建时需要 Go。

## 用法

### 服务一个目录

```bash
mdserve serve --root ~/notes --port 8080
```

### 一个进程两个站点

```yaml
mounts:
  - name: docs
    host: docs.example.com
    root: /srv/docs
    auth:
      password_file: /etc/mdserve/docs.pass
  - name: handbook
    host: handbook.example.com
    root: /srv/handbook
    auth:
      password_hash: "$argon2id$..."
```

每个域名都要在 Cloudflare 隧道里加一条 Public Hostname，Service 都指向 `localhost:8787`。

### 挂到公网

```bash
mdserve serve --root ~/notes \
  --tunnel named \
  --tunnel-token-file /etc/mdserve/tunnel.token \
  --tunnel-host docs.example.com
```

`cloudflared` 作为受管子进程运行，异常退出会按退避重启。token 通过环境变量传入，
不会出现在 `ps` 里。

### 分享与撤销

```bash
mdserve share --host docs.example.com   # 一次性分享码，一小时有效
mdserve revoke                          # 让所有会话与分享码失效
```

→ [命令行参考](docs/cli.md)：所有命令、所有参数，以及 `mdserve config` 的输出含义。

## 工作原理

请求先按 `Host` 找到挂载点，再经过符号链接校验解析到根目录内，把 Markdown 渲染成消毒后的 HTML，
最后带上与域名绑定的会话 cookie 返回。

→ [实现细节](docs/how-it-works.md)

## 配置

YAML 放在 `./mdserve.yaml` 或 `~/.config/mdserve/config.yaml`；命令行覆盖环境变量，
环境变量覆盖配置文件。口令只从哈希或 `0600` 文件读取，不接受明文；`mdserve config`
会打印生效值和每个值的来源。

→ [配置参考](docs/configuration.md)

## 安全

只绑回环、只读、HTML 消毒、登录限流、会话绑定域名、一条命令撤销。口令挡的是爬虫和路人，
挡不住有动机的攻击者。

→ [安全模型](docs/security.md)

## 参与贡献

欢迎提 issue 和 PR。提交前请跑 `make test` 与 `make vet`。

## License

MIT —— 见 [LICENSE](LICENSE)。

---

[license-shield]: https://img.shields.io/badge/license-MIT-blue.svg
[license-url]: LICENSE
[release-shield]: https://img.shields.io/github/v/release/Fel1xKan/mdserve
[release-url]: https://github.com/Fel1xKan/mdserve/releases
[ci-shield]: https://img.shields.io/github/actions/workflow/status/Fel1xKan/mdserve/ci.yml
[ci-url]: https://github.com/Fel1xKan/mdserve/actions/workflows/ci.yml
[go-shield]: https://img.shields.io/badge/Go-1.26-00ADD8.svg
[go-url]: https://go.dev
[platform-shield]: https://img.shields.io/badge/platforms-linux%20%7C%20macOS%20%7C%20Windows-lightgrey.svg
