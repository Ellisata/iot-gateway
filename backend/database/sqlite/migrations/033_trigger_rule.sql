-- 事件触发采集规则：条件满足上升沿时，用采集活会话异步读取动作点位并推送到一个通道实例。
-- 记录表保留规则名与条件/结果快照，规则删除后历史仍可自证。
CREATE TABLE IF NOT EXISTS trigger_rule (
    id                 TEXT PRIMARY KEY,
    name               TEXT NOT NULL,
    description        TEXT NOT NULL DEFAULT '',
    status             INTEGER NOT NULL DEFAULT 1,
    action_device_id   TEXT NOT NULL,
    action_address_ids TEXT NOT NULL DEFAULT '[]',
    target_channel_id  TEXT NOT NULL,
    conditions_json    TEXT NOT NULL DEFAULT '{"groups":[]}',
    cooldown_ms        INTEGER NOT NULL DEFAULT 0,
    timeout_ms         INTEGER NOT NULL DEFAULT 5000,
    created_at         TEXT DEFAULT (datetime('now','localtime')),
    updated_at         TEXT DEFAULT (datetime('now','localtime'))
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_trigger_rule_name ON trigger_rule(name);
CREATE INDEX IF NOT EXISTS idx_trigger_rule_status ON trigger_rule(status);

CREATE TABLE IF NOT EXISTS trigger_rule_record (
    id                 TEXT PRIMARY KEY,
    trigger_id         TEXT NOT NULL,
    rule_id            TEXT NOT NULL,
    rule_name          TEXT NOT NULL,
    fired_at           TEXT NOT NULL,
    status             TEXT NOT NULL,
    condition_json     TEXT NOT NULL DEFAULT '',
    action_result_json TEXT NOT NULL DEFAULT '',
    duration_ms        INTEGER NOT NULL DEFAULT 0,
    error              TEXT NOT NULL DEFAULT '',
    created_at         TEXT DEFAULT (datetime('now','localtime'))
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_trigger_rule_record_trigger ON trigger_rule_record(trigger_id);
CREATE INDEX IF NOT EXISTS idx_trigger_rule_record_rule_time ON trigger_rule_record(rule_id, fired_at);
CREATE INDEX IF NOT EXISTS idx_trigger_rule_record_fired_at ON trigger_rule_record(fired_at);
