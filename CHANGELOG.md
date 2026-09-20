# Changelog

本项目遵循 [Keep a Changelog](https://keepachangelog.com/zh-CN/1.1.0/) 格式，版本号遵循 [语义化版本](https://semver.org/lang/zh-CN/)。

All notable changes to this project are documented in this file (Keep a Changelog format, SemVer versioning).

## [Unreleased]

### Changed

- JWT 签名密钥不再使用内置固定值：未配置时每次启动随机生成临时密钥；生产环境建议通过 `IOT_GATEWAY_JWT_SECRET` 环境变量或 `jwt.secret` 配置固定密钥。
  The JWT signing secret no longer ships with a built-in fixed value: an ephemeral random secret is generated per start when unset; set `IOT_GATEWAY_JWT_SECRET` or `jwt.secret` in production to keep sessions across restarts.

### Added

- 新增 DL/T 645 电能表协议驱动（`DLT645.Serial` / `DLT645.TCP`）：经 RS-485 串口或串口服务器（DTU）采集多功能电能表的电压、电流、功率、电能等数据；支持 DL/T 645-2007 / 1997 两版数据标识、逐标识寻址与批量打包读（`maxDIsPerRead`）、内置数据标识字典与校验和校验，并参与断联报警。
  Added the DL/T 645 electricity-meter driver (`DLT645.Serial` / `DLT645.TCP`): reads voltage, current, power and energy from multifunction meters over RS-485 or a serial server (DTU), supporting both the DL/T 645-2007 and 1997 identifier sets, per-identifier addressing with batch packing (`maxDIsPerRead`), a built-in data-identifier dictionary and checksum verification, and disconnection alarms.
- 开放接口文档按管理端用户语言展示：新增英文版文档，`/openApiSecret/doc` 接口根据用户语言（`area` 头）返回中文或英文版本。
  Open API documentation now follows the admin user's language: an English version was added, and `/openApiSecret/doc` returns the Chinese or English version based on the user's language (`area` header).
