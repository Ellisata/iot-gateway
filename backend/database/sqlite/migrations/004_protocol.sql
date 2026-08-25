-- 物联网协议表
CREATE TABLE IF NOT EXISTS iot_protocol (
    id          TEXT    PRIMARY KEY,
    name        TEXT    NOT NULL UNIQUE,
    description TEXT,
    form_json   TEXT,
    sort        INTEGER DEFAULT 1,
    status      INTEGER NOT NULL DEFAULT 1,
    created_at  TEXT    DEFAULT (datetime('now', 'localtime')),
    updated_at  TEXT    DEFAULT (datetime('now', 'localtime'))
);
CREATE INDEX IF NOT EXISTS idx_iot_protocol_name ON iot_protocol(name);
