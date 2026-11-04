# Changelog

格式遵循 [Keep a Changelog](https://keepachangelog.com/zh-CN/1.1.0/)，
版本号遵循 [语义化版本](https://semver.org/lang/zh-CN/)。

## [Unreleased]

## [0.1.0] - 2026-11-04

首个可运行版本：单文件服务同时提供文档渲染与 Cloudflare named 隧道接入。

### Added

- 单文件二进制：`mdserve` 在任意目录启动即可把 markdown 映射成网页
- goldmark 服务端渲染：表格（横向滚动容器）、任务列表、脚注、定义列表
- 标题锚点与侧栏目录，中文标题也能得到可读的锚点
- mermaid 图表：在 `sandbox="allow-scripts"`（无 `allow-same-origin`）的 iframe 里渲染，
  代码通过 postMessage 传入，父页面不执行 mermaid
- 全文检索：大小写不敏感的子串匹配，命中行带行号
- 会话鉴权：HMAC token 绑定 host 与 epoch，cookie 为 `HttpOnly` + `SameSite=Lax`
- 一次性分享码：`mdserve share`，1 小时有效、只能用一次、与域名绑定
- `mdserve revoke`：轮换 epoch，所有会话与分享码立即失效（运行中的服务 2 秒内生效）
- `mdserve passwd`：生成随机口令与 argon2id 哈希
- 多站点：按 Host 路由，每个挂载点独立口令；未知 Host 返回 421
- 静态资源按内容哈希版本化（`?v=`），配 `immutable` + `ETag` + 304
- 资源与 HTML 页面均支持 gzip（`Vary: Accept-Encoding`）
- mermaid 主包懒加载：图表进入视口附近才下载
- 内部端点统一收在 `/-/` 下，用户目录不会再被 `/vendor`、`/search` 遮住
- `server.base_path`：支持挂在域名子路径下
- `server.hidden`：不展示但仍可直连的 glob，与「完全不扫描」的 `exclude` 分开
- `--log-format json`：结构化审计日志
- 配置：YAML + 环境变量 + 命令行，`mdserve config` 显示每项来源
- `mdserve uninstall`：清理本地状态（默认只预览，加 `--yes` 才执行）
- `/-/about` 与 404 门面页；不暴露根目录、配置路径、主机名

### Security

- 只绑回环；非回环监听必须配置 `public_url`，否则拒绝启动
- 路径解析走 `EvalSymlinks` + `Rel`，符号链接跳不出根目录
- 原始 HTML 经 bluemonday 消毒，禁 script / iframe / 事件属性
- 静态资源只直出白名单后缀，不返回 html/js；SVG 强制 `attachment` + `sandbox` CSP
- `/-/vendor/` 改为白名单查表，不再使用 `http.FileServer`，没有目录列表与路径穿越面
- CSP、`nosniff`、`Referrer-Policy: no-referrer`、`frame-ancestors 'none'`
- 登录请求体在读之前限制为 4KB；限流按 IP，只信任来自回环的 `CF-Connecting-IP`
- 口令只从 `password_file`（0600）或 `password_hash` 来，不接受明文配置与环境变量
- 隧道 token 通过 `TUNNEL_TOKEN` 环境变量传给子进程，不出现在 `ps` 输出里
