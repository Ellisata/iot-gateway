# 网关与插件版本兼容性

> 适用于三个仓库：网关主程序 [`iot-gateway-premium`](../README.md)、官方驱动
> [`iot-gateway-drivers`](https://github.com/Ellisata/iot-gateway-drivers)、驱动 SDK
> [`iot-gateway-sdk`](https://github.com/Ellisata/iot-gateway-sdk)。最后更新：2026-10-27。

## 1. 结论

网关与插件之间**只有一个硬性兼容判据**：`driversdk.APIVersion`。握手时两者必须严格相等，
不相等就拒绝加载插件 —— 不存在“版本接近就先跑起来”的路径。

- **网关发行版本、驱动发行版本、SDK 的 semver 都不参与运行时判定**。它们只用于发行配对、
  日志诊断和问题定位。
- **官方发行包按「网关版本 + `drivers.version`」成对构建、成对测试**。生产环境按
  [第 3 节](#3-官方发行配对矩阵)的矩阵配对。
- **同一 `APIVersion` 内**，插件二进制可以单独替换（热升级）；**`APIVersion` 进位后**，
  全部插件必须重新编译，不存在混跑。

一句话记忆：**看 APIVersion 决定能不能加载，看发行矩阵决定官方支不支持这个组合。**

## 2. 版本从哪来

| 版本 | 真源 | 当前值 | 参与兼容判定 |
|---|---|---|---|
| 网关发行版本 | git tag；构建脚本以 `-X main.version` 注入，写入 `iot_gateway.firmware_version` 并随局域网发现广播 | `v0.3.0` | 否 |
| 官方驱动发行版本 | 仓库根的 `drivers.version`；构建时经 `-X main.version` 写入每个插件的 `Meta.Version` | `v0.1.0` | 否 |
| 驱动 SDK 版本 | 网关 `backend/go.mod`、驱动仓库 `go.mod` | `v0.1.0` | 否 |
| 线协议版本 | `driversdk.APIVersion`（[SDK 源码](https://github.com/Ellisata/iot-gateway-sdk/blob/main/driversdk/meta.go)） | `4` | **是，必须相等** |
| 帧魔数 | `pluginwire.FrameMagic`（[SDK 源码](https://github.com/Ellisata/iot-gateway-sdk/blob/main/pluginwire/wire.go)） | `IGP2` | 是，流层第二道保护 |

网关版本的解析优先级（实现见 [configFile.ResolveGatewayVersion](../backend/configFile/configFile.go)）：

```
IOT_GATEWAY_VERSION > 构建注入版本（非 dev）> default.yaml 的 gateway.version > "dev"
```

发行二进制以构建注入值为准；`default.yaml` 里的 `gateway.version` 只服务于未注入版本的
本地构建（`go run`、普通 `go build`）。

## 3. 官方发行配对矩阵

每一行对应一次网关发行。**这一节回答“官方支持哪个组合”，不是“技术上能不能加载”。**

| 网关版本 | `drivers.version` | 依赖 SDK | `APIVersion` | 插件 / 协议 | 说明 |
|---|---|---|---|---|---|
| v0.2.0 | v0.1.0 | v0.1.0 | 4 | 9 / 15 | 上一发行版 |
| v0.3.0 | v0.1.0 | v0.1.0 | 4 | 9 / 15 | 当前离线发行版 |
| v0.4.0（待发布） | v0.2.0 | v0.2.0 | 4 | 9 / 15 | 驱动动态表单与扫描元数据支持中英切换 |

维护规则：

1. 每发一个网关版本追加一行，不要改历史行。
2. `drivers.version` 取仓库根同名文件；SDK 版本取该次构建时 `backend/go.mod` 里的
   `github.com/Ellisata/iot-gateway-sdk` 版本 —— **按实际构建取值，不按 tag 日期推断**。
3. `APIVersion` 取 `driversdk.APIVersion`；它是判定兼容性的唯一数字，必须与行内其它值对得上。

## 4. 插件兼容区间

这一节回答“我手里的插件能装到哪些网关上”，面向第三方驱动作者与自编插件。

| 构建所用 SDK | `APIVersion` | 可加载于 | 不可加载于 |
|---|---|---|---|
| v0.1.x | 4 | 任意 `APIVersion = 4` 的网关（当前即 v0.3.0 及以后） | `APIVersion ≠ 4` 的网关 |

补充：

- 官方驱动 v0.1.0 用 SDK v0.1.0 构建，`APIVersion = 4`，因此落在第一行。
- 插件作者必须在 `driversdk.Meta` 里填 `APIVersion: driversdk.APIVersion`，不要手写数字。
- `Meta.Version` 只用于日志与诊断。手工 `go build` 且未传 `-X main.version` 时它是
  `"dev"`，不代表任何官方版本。

## 5. 替换与升级规则

### 5.1 同一 `APIVersion` 内

- **官方支持**：把主二进制与 `plugins/` 目录作为整体替换成同一发行包的内容。
- **技术上**：同 `APIVersion` 的插件可以被单独替换，热升级路径见
  [驱动插件指南 §5.5](driver-plugins.md#55-热升级替换已加载的插件)。若现场确实只换其中一半，
  先用第 6 节的方法核对 `APIVersion`，并记录这一组合不是官方配对矩阵里的行。

> 旧文档里“旧网关 + 新插件、新网关 + 旧插件都会失效”的说法只对**跨 `APIVersion`**
> 成立。同位版本的混搭可以加载，但这不等于官方支持该组合。

### 5.2 `APIVersion` 进位

- 旧插件在新网关、新插件在旧网关上都会在握手期被拒绝，日志会给出期望值与实际值。
- 发行说明必须明确写出“本次 `APIVersion` 由 X 进位到 Y，全部插件必须重编”。
- 回滚时同样成对回滚：主二进制与 `plugins/` 一起换回。

### 5.3 什么情况必须进位

| 变化 | 是否进位 `APIVersion` |
|---|---|
| 帧信封格式、载荷字段、编码方式变化 | **是** |
| 新增或修改插件与网关之间的 RPC 方法 | **是** |
| 插件依赖的网关侧行为发生变化（校验、超时语义等） | **是** |
| 只新增可选 `Meta` 字段（旧网关忽略、旧插件走零值） | 否 |

最后一行是刻意的：能靠“未知字段忽略 / 缺省走空”兼容的演进，不应让全部旧插件二进制失效。
这条规则在 [SDK 的 `Meta` 注释](https://github.com/Ellisata/iot-gateway-sdk/blob/main/driversdk/meta.go)
里有逐例说明。

## 6. 如何确认现场版本

1. **网关启动日志**（启动时打印解析后的版本与插件线协议版本）：

   ```
   application starting... (version v0.3.0, plugin APIVersion 4)
   ```

2. **插件加载日志**（每个插件一条，版本来自 `Meta.Version`）：

   ```
   pluginhost: 已加载插件 iot-driver-modbus（协议 ModBus.TCP, ModBus.RTU，scope modbus，版本 v0.1.0，sha256 ...）
   ```

3. **网关身份 / 局域网发现**：`iot_gateway.firmware_version` 与广播报文里的
   `firmwareVersion`，均来自第 2 节的版本解析链。

4. **发行包 `MANIFEST.json`**（包根，`release.sh` / `release.bat` 生成）：

   ```json
   {
     "gateway": "v0.3.0",
     "os": "windows",
     "arch": "amd64",
     "buildTime": "2026-10-27T08:00:00Z",
     "sdk": "v0.1.0",
     "apiVersion": 4,
     "frameMagic": "IGP2",
     "drivers": "v0.1.0",
     "plugins": [
       {"name": "iot-driver-modbus", "version": "v0.1.0", "sha256": "..."}
     ]
   }
   ```

   这是现场核对最快、也最不容易被文件时间误导的入口：`apiVersion` 与插件列表都在里面。
   `install.sh` / `install.bat` 会把它一并复制到安装目录。主二进制与 `plugins/` 来自同一次
   构建；不要用文件时间或 tag 日期推断。

## 7. 发版检查清单

- [ ] 确认 `backend/go.mod` 的 SDK 版本、`drivers.version`、`driversdk.APIVersion` 三者已知。
- [ ] 在第 3 节追加本次发行行，不改历史行。
- [ ] 若 `APIVersion` 进位：更新第 4 节区间，并在 CHANGELOG 与发行说明里写明“全部插件必须重编”。
- [ ] 确认发行包里的主二进制与 `plugins/` 由同一次构建产出，且包根带 `MANIFEST.json`。
- [ ] 核对 `MANIFEST.json` 的 `gateway` / `apiVersion` / `drivers` 与本矩阵行一致。
- [ ] 确认主二进制启动日志打印的版本与发行 tag 一致（不一致说明构建脚本没传 `-X main.version`）。
