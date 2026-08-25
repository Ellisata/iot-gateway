# iot-hand-gateway

工业物联网数据网关。前端 `frontend`（Vue3 + Vite），后端 `backend`（Go + Gin）。

## 一键运行

### 开发模式（前后端热更新，两个进程）

| 入口 | 说明 |
|---|---|
| `dev.bat`（Windows） / `dev.sh`（Git Bash/Linux） | 同时启动 Go 后端(9081) + Vite 前端(3000) |
| VS Code「终端 → 运行任务 → dev」 | 同上，在集成终端并行启动 |
| VS Code 按 F5 选「全栈开发 Full Stack Dev」 | 后端走 Go 调试器 + Vite 前端 |

访问 `http://localhost:3000`。前端请求经 Vite 代理/CORS 直连后端 9081，改代码即热更新。

### 生产模式（单二进制，内置前端）

后端通过 `go:embed` 内嵌 `backend/web/dist`，在 `/admin/*` 分发 SPA。前端改动后需重新打包。

| 入口 | 说明 |
|---|---|
| `build.bat` | 一键打包：`npm run build` → 同步到 `backend/web/dist` → Go 构建（默认 Linux + Windows；`build.bat windows` 仅构建 Windows） |
| `run.bat` | 运行打包产物（单二进制，前后端一个进程）；缺少 `iot-gateway.exe` 时自动构建，`run.bat --build` 强制重建 |

访问 `http://localhost:9081/admin`。产物为 `backend/iot-gateway(.exe)`，可拷贝到网关设备单独部署。

### 服务化部署（系统后台服务）

将可执行文件安装为开机自启的后台服务（服务名 `iot-hand-gateway`，管理地址 `http://<主机IP>:9081/admin`）。

| 平台 | 安装 | 卸载 |
|---|---|---|
| Windows | 管理员运行 `install.bat` | 管理员运行 `uninstall.bat`（`--purge` 连数据一并删除） |
| Linux | `sudo ./install.sh` | `sudo ./uninstall.sh`（`--purge` 连数据一并删除） |

- 安装目录：Windows 默认 `%ProgramFiles%\iot-hand-gateway`（可 `set IOT_GATEWAY_HOME=...` 覆盖）；Linux 默认 `/opt/iot-hand-gateway`（可 `INSTALL_DIR=...` 覆盖）。
- 服务以安装目录为工作目录运行，`data/`（SQLite）与 `log/`（日志）写入该目录；程序崩溃自动重启。
- Windows 后端已内置原生服务支持（`golang.org/x/sys/windows/svc`，见 `backend/service_windows.go`），`install.bat` 直接用 `sc` 命令注册，无需 NSSM 等第三方工具；崩溃自动重启由 `sc failure` 配置。
- 如需覆盖嵌入的默认配置，将自定义 `default.yaml` 放到脚本同目录，安装时会一并复制到安装目录。
- 卸载脚本默认保留安装目录与数据，避免误删采集数据。

## 目录

- `backend/`  Go 后端（配置 `default.yaml` + `APP_ENV=dev|prod`，端口 9081）
- `frontend/` Vue3 前端（`npm run dev` 开发 / `npm run build` 构建）
- `.vscode/` 一键启动配置（tasks + compound launch）
