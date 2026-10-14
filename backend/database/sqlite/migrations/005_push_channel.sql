-- 数据推送通道
CREATE TABLE IF NOT EXISTS push_channel (
    id          TEXT    PRIMARY KEY,
    name        TEXT    NOT NULL UNIQUE,
    description TEXT,
    config_json   TEXT NOT NULL,
    status      INTEGER NOT NULL DEFAULT 1,
    created_at  TEXT    DEFAULT (datetime('now', 'localtime')),
    updated_at  TEXT    DEFAULT (datetime('now', 'localtime'))
);

-- 数据推送通道表单
CREATE TABLE IF NOT EXISTS push_channel_form (
    id          TEXT    PRIMARY KEY,
    name        TEXT    NOT NULL UNIQUE,
    form_json   TEXT NOT NULL,
    created_at  TEXT    DEFAULT (datetime('now', 'localtime')),
    updated_at  TEXT    DEFAULT (datetime('now', 'localtime'))
);