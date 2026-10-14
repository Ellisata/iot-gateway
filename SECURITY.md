# 安全策略 / Security Policy

- [English](#english) · [简体中文](#简体中文)

---

<a id="简体中文"></a>

## 简体中文

### 报告漏洞

发现安全漏洞请**不要**公开 Issue / Discussion，请发送邮件至 **15289288565@163.com**，内容尽量包含：

- 漏洞类型与影响范围
- 复现步骤或概念验证（PoC）
- 受影响的版本（commit hash 或 release tag）
- 修复建议（如有）

我们会在 7 天内确认收到，并协商披露时间线与致谢方式。

### 建议的部署加固

- 首次启动后**立即修改**默认账号（`admin` / `hand`，初始密码 `hand@123`）的密码。
- 通过环境变量 `IOT_GATEWAY_JWT_SECRET` 或配置文件设置专属的 JWT 签名密钥；未配置时每次启动会随机生成临时密钥（重启后所有登录态失效）。
- 不要将管理控制台（`/admin`）直接暴露到公网，建议置于内网或反向代理 + 访问控制之后。
- Open API 密钥仅授予只读查询权限，按需生成、定期轮换。

### 外部协议驱动插件（进程外驱动）

**全部内置协议驱动都已是进程外插件**：网关主二进制不再包含任何协议实现，协议由
`plugins/` 目录下的独立可执行文件（`iot-driver-*`）提供，丢进目录即自动加载（见
[docs/driver-plugins.md](docs/driver-plugins.md)）。因此 `plugin.enabled` **出厂值即为 `true`**
—— 关掉它等于网关不支持任何协议（设备会被静默跳过：只有一行 WARN，前端与健康检查都正常）。

这决定了本项目的**核心信任边界**：

> `plugin.enabled: true` 等价于允许网关**以自身的 OS 权限执行 `plugins/` 下的任意可执行文件**。
> 插件是与网关同权限的**可信代码**，不受任何沙箱约束。`plugins/` 目录因此是一条
> **可执行代码的信任边界**——谁能写该目录，谁就能以网关的权限执行代码。

部署时请务必：

- 确保插件目录**只对管理员可写**。Unix 下网关会拒绝世界可写的目录
  （除显式设置 `plugin.allowInsecureDir: true`）；Windows 的 stat 权限位无意义，
  网关只记警告、**不做 ACL 校验**，需由部署方自行保证。
- 只放入自己构建或可信来源的插件二进制。目录中的符号链接会按解析后的真实父目录校验，
  但仍不应把该目录开放给低权限用户写入。
- 每个插件的 SHA-256 会在加载时记入日志，可作为审计与比对依据。

网关侧已实施的加固：子进程环境变量从零构造（不含 `IOT_GATEWAY_JWT_SECRET` 等密钥）、
不经 shell 执行且不接受插件提供的参数、帧长上限、插件失步即杀进程、进程崩溃自动重启并限次、
Windows 下插件进程被放入 `KILL_ON_JOB_CLOSE` 的 Job Object（网关进程终止时由内核连带终止，
不留孤儿进程）。

---

<a id="english"></a>

## English

### Reporting a Vulnerability

Please **do not** open a public issue or discussion for security vulnerabilities. Email **15289288565@163.com** with, where possible:

- Vulnerability type and impact
- Reproduction steps or a proof of concept
- Affected version (commit hash or release tag)
- Suggested fix (if any)

We will acknowledge within 7 days and coordinate disclosure and credit.

### Deployment Hardening Recommendations

- **Change the default accounts immediately** on first start (`admin` / `hand`, initial password `hand@123`).
- Set a dedicated JWT signing secret via the `IOT_GATEWAY_JWT_SECRET` environment variable or the config file. If unset, an ephemeral secret is generated on each start (all sessions are invalidated on restart).
- Do not expose the admin console (`/admin`) directly to the public internet; keep it behind an intranet or a reverse proxy with access control.
- Open API keys are read-only by design; grant them per need and rotate regularly.

### Out-of-process driver plugins

**All built-in protocol drivers are now out-of-process plugins**: the gateway binary contains no
protocol implementation at all — protocols come from standalone executables (`iot-driver-*`) in the
`plugins/` directory, loaded automatically as soon as they are dropped in (see
[docs/driver-plugins.md](docs/driver-plugins.md)). `plugin.enabled` therefore **ships as `true`** —
turning it off means the gateway supports no protocol at all (devices are skipped silently: a single
WARN line, while the web console and health check stay green).

This defines the project's **core trust boundary**:

> `plugin.enabled: true` grants the gateway permission to execute **any executable in `plugins/`
> with the gateway's own OS privileges**. Plugins are trusted code on par with the gateway
> itself, not sandboxed in any way. The `plugins/` directory is therefore a **trust boundary for
> executable code** — whoever can write it can execute code with the gateway's privileges.

When deploying:

- Ensure the plugin directory is **writable only by administrators**. On Unix the gateway
  refuses world-writable directories (unless `plugin.allowInsecureDir: true` is set
  explicitly); on Windows file permission bits are meaningless, so the gateway only logs a
  warning and performs **no ACL verification** — this is the deployer's responsibility.
- Only place binaries you built yourself or obtained from a trusted source. Symlinks in the
  directory are validated against their resolved real parent, but the directory still must not
  be writable by low-privileged users.
- Each plugin's SHA-256 is logged at load time and can serve as an audit/comparison record.

Hardening already implemented on the gateway side: the child environment is built from scratch
(excluding `IOT_GATEWAY_JWT_SECRET` and similar secrets), no shell is involved and no
plugin-supplied arguments are accepted, frame size caps, kill-on-desync, bounded automatic
restart with backoff, and — on Windows — plugin processes are placed in a
`KILL_ON_JOB_CLOSE` Job Object so the kernel terminates them if the gateway process goes away
(no orphan processes).
