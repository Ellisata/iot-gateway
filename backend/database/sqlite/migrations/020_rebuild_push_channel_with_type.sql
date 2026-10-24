-- 推送通道从「名称即类型」改为「类型 + 实例名」。
-- SQLite 无法直接去掉 name 的内联 UNIQUE，因此重建表。
-- 旧行迁移为 type = name，旧配置与旧接口继续可用。
CREATE TABLE push_channel_new (
    id          TEXT PRIMARY KEY,
    name        TEXT NOT NULL,
    type        TEXT NOT NULL DEFAULT '',
    description TEXT,
    config_json TEXT NOT NULL,
    status      INTEGER NOT NULL DEFAULT 1,
    created_at  TEXT DEFAULT (datetime('now', 'localtime')),
    updated_at  TEXT DEFAULT (datetime('now', 'localtime'))
);

INSERT INTO push_channel_new (id, name, type, description, config_json, status, created_at, updated_at)
SELECT id, name, name, description, config_json, status, created_at, updated_at
FROM push_channel;

DROP TABLE push_channel;

ALTER TABLE push_channel_new RENAME TO push_channel;

CREATE UNIQUE INDEX idx_push_channel_type_name ON push_channel(type, name);

CREATE INDEX idx_push_channel_type ON push_channel(type);

CREATE INDEX idx_push_channel_name ON push_channel(name);
