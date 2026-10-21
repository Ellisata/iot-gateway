-- 运行期配置覆盖表。
--
-- default.yaml 仍是默认值的唯一真源：本表**不播种**任何行，
-- 表里没有某个 key 就表示「使用 default.yaml 的值」。
-- 页面 / API 修改开关时写入一行覆盖值，重启后仍然生效，
-- 「恢复默认」删除该行。
--
-- value 存 JSON 标量（当前只有 true / false），避免布尔值出现
-- 'true' / '1' / 'on' 三种写法各自为政。覆盖值的优先级高于
-- 嵌入与磁盘上的 default.yaml，低于环境变量（环境变量仍只用于
-- jwt.secret / admin 口令 / gateway.name 这几个启动期项）。
--
-- key 是 default.yaml 的点分路径，与 runtimecfg 注册表一一对应。
-- 注册表之外的 key 即使出现在本表里也不会生效（只记一条 WARN），
-- 这样升级 / 降级网关时多出来的行不会把启动搞坏。
CREATE TABLE IF NOT EXISTS system_config (
    key        TEXT PRIMARY KEY,
    value      TEXT NOT NULL,
    updated_by TEXT NOT NULL DEFAULT '',
    updated_at TEXT NOT NULL DEFAULT (datetime('now', 'localtime'))
);
