# 运行期配置开关（runtimecfg）

`default.yaml` 仍然是**默认值**的唯一真源。其中一部分开关可以在管理端
「运行开关」页面动态修改，修改结果写入 SQLite 的 `system_config` 表并长期覆盖
默认值。未登记的配置项不受影响，仍只在本文件维护、改动需重启。

## 1. 优先级

```
system_config 表里的覆盖行  >  configFile（default.yaml + APP_ENV 环境配置）
```

- `system_config` **不播种**：表里没有某个 key 就表示「使用 default.yaml 的值」。
- 「恢复默认」= 删除该行，不是把默认值写进表里。
- 环境变量仍只用于 `jwt.secret` / 初始 admin 口令 / `gateway.name` 等启动期项，
  不参与本机制。
- 手工修改磁盘上的 `default.yaml` 不会被监听；动态修改只走页面 / API。

## 2. 首批纳入的开关

| key | 生效方式 | 说明 |
|---|---|---|
| `discovery.enabled` | 立即生效 | 启停 UDP 广播 / 探询应答套接字。`port` / `intervalMs` 不在范围内。 |
| `deviceScan.enabled` | 立即生效 | 只拦截**新**扫描；运行中的任务照常结束或由用户取消。 |
| `push.changeOnly` | 立即生效 | 从下一批采集起生效。`false -> true` 时清空已投递记录，下一批全量下发一次。 |
| `plugin.enabled` | **重启后生效** | 插件管理器的 `Stop` 是终态的，运行期启停需要重写进程监管生命周期。 |

未纳入（有意为之）：`server.port`、`jwt.*`、`log.*`、`sqlite.path`、
`plugin.dir/glob/timeouts`、`discovery.port/intervalMs`，以及
`plugin.allowInsecureDir`（安全敏感启动项）等。完整排除清单在
`runtimecfg/registry_test.go` 的 `excludedBools` 里，每条都写了理由。

## 3. 存储

```sql
CREATE TABLE IF NOT EXISTS system_config (
    key        TEXT PRIMARY KEY,   -- default.yaml 的点分路径，如 "push.changeOnly"
    value      TEXT NOT NULL,      -- JSON 标量，当前是 true / false
    updated_by TEXT NOT NULL DEFAULT '',
    updated_at TEXT NOT NULL DEFAULT (datetime('now', 'localtime'))
);
```

- value 用 JSON 而不是字符串布尔：`"true"` / `"1"` / `"on"` 一律拒绝，
  避免同一个开关出现多种写法。
- 未知 key、类型不符、损坏的覆盖行只记 WARN 并忽略，不阻断启动。
- 降级后多出来的行不删除：再升级回去时它们仍然有效。

## 4. 更新事务

单 key 修改，顺序固定为：

1. 查注册表；未知 key 直接拒绝。
2. 严格解析 JSON 值（类型 / 范围）。
3. 持更新锁，检查管理器未关闭。
4. **落库**（UPSERT / DELETE），不持有事务。
5. 发布新的内存快照（读方立刻看到新值）。
6. 调用 applier（仅 `hot-action` 必须注册；`hot-read` 可选）。
7. applier 失败：恢复旧快照 → 补偿写回旧行 / 删除新行 → 尽力用旧值再 apply
   一次 → 返回 `ErrApplyFailed`。

先落库的理由：落库失败不应改变运行行为；apply 失败可以用一次补偿写回旧值，
而数据库才是重启后的真源。**应用期间不持有 SQLite 事务** —— 单写连接不能被
`discovery.Stop()` 这类会等待协程退出的操作占住。

## 5. API

| 方法 | 路径 | 分类 | 说明 |
|---|---|---|---|
| GET | `/systemConfig/list` | read | 每项含生效值、默认值、来源、是否立即生效、是否待重启 |
| POST | `/systemConfig/update` | write | body `{key, value}`，返回 `requiresRestart` |
| POST | `/systemConfig/reset` | write | body `{key}`，删除覆盖值 |

三者都按项目约定镜像到免鉴权的 `/agentApi`，分类见
`router/agentApiSurface.go`。`update` / `reset` 是写操作，dsh 调用前应取得用户确认。

## 6. 新增一个开关

以新增 `xxx.enabled` 为例：

1. **默认值**：在 `default.yaml` 与 `configFile.Config` 里加字段（已有字段则跳过）。
2. **登记**：在 `runtimecfg/registry.go` 的 `specs` 里加一条 `Spec`：
   - `Key` 用 default.yaml 的点分路径；
   - `Mode` 选 `ApplyHotRead`（每次用到时读）、`ApplyHotAction`（需要启停组件）
     或 `ApplyRestart`；
   - 填 `Base`、分组与 `LabelKey` / `DescKey`。
3. **消费**：
   - 热读项：组件从 `runtimecfg.Reader.Bool(key)` 读取，nil 时回退启动期配置；
   - 热应用项：组件在构造时 `RegisterApplier(key, fn)`，applier 必须原子
     （失败时保持调用前状态），且不得回调 `Manager.Update/Reset`；
   - 重启项：组件在构造时读一次并冻结，接口会返回 `requiresRestart=true`。
4. **文案**：在 `frontend/src/i18n/locales/zh-CN.js` 与 `en.js` 同时补上
   `runtimeConfig.fields.<name>`（label / hint），以及必要的确认文案。

`runtimecfg/registry_test.go` 会反射遍历 `configFile.Config` 的所有布尔叶子：
新增布尔配置却没有登记、也没有写进排除表时，测试会失败。这样「要不要让它
可动态修改」必须被有意识地决定一次。

## 7. 安全

- 这些开关能改变网关的对外行为（局域网可见性、扫描能力、推送量、重启后是否
  加载任何协议）。写接口应只对管理员开放。
- `/agentApi` 按项目现有约定免鉴权，与 plugin 上传、设备删除同一暴露面；
  风险说明见 `router/agentApiRouter.go` 顶部。若网关会暴露到不可信网络，
  应先给整个 `/agentApi` 分组加鉴权。
- `plugin.enabled=false` 在重启后会让网关不支持任何协议，页面对该操作做了二次确认。
