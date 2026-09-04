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
