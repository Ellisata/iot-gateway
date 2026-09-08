# Changelog

本项目遵循 [Keep a Changelog](https://keepachangelog.com/zh-CN/1.1.0/) 格式，版本号遵循 [语义化版本](https://semver.org/lang/zh-CN/)。

All notable changes to this project are documented in this file (Keep a Changelog format, SemVer versioning).

## [Unreleased]

### Changed

- JWT 签名密钥不再使用内置固定值：未配置时每次启动随机生成临时密钥；生产环境建议通过 `IOT_GATEWAY_JWT_SECRET` 环境变量或 `jwt.secret` 配置固定密钥。
  The JWT signing secret no longer ships with a built-in fixed value: an ephemeral random secret is generated per start when unset; set `IOT_GATEWAY_JWT_SECRET` or `jwt.secret` in production to keep sessions across restarts.

### Added

- 开放接口文档按管理端用户语言展示：新增英文版文档，`/openApiSecret/doc` 接口根据用户语言（`area` 头）返回中文或英文版本。
  Open API documentation now follows the admin user's language: an English version was added, and `/openApiSecret/doc` returns the Chinese or English version based on the user's language (`area` header).
- 开源发布配套文件：CONTRIBUTING、SECURITY、CHANGELOG、CLA、GitHub/Gitee Issue 与 PR 模板。
  Open-source release scaffolding: CONTRIBUTING, SECURITY, CHANGELOG, CLA, GitHub/Gitee issue and PR templates.
- `build.sh`：Linux/macOS 一键构建脚本（与 build.bat 等价，支持 all/windows/linux/native 目标）。
  `build.sh`: one-click build script for Linux/macOS (equivalent to build.bat; targets: all/windows/linux/native).
