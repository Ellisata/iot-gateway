-- 物联网设备表
CREATE TABLE IF NOT EXISTS device (
    id          TEXT PRIMARY KEY,
    name        TEXT NOT NULL UNIQUE,
    protocol_id TEXT NOT NULL,
    protocol_json    TEXT NOT NULL,
    description TEXT,
    status      INTEGER NOT NULL DEFAULT 1,
    created_at  TEXT    DEFAULT (datetime('now', 'localtime')),
    updated_at  TEXT    DEFAULT (datetime('now', 'localtime'))
);
CREATE INDEX IF NOT EXISTS idx_device_name ON device(name);


-- 物联网设备地址位表
CREATE TABLE IF NOT EXISTS device_address (
    id TEXT PRIMARY KEY,
    device_id  TEXT NOT NULL,
    name        TEXT NOT NULL,
    label        TEXT NOT NULL DEFAULT '',
    common_data_type   TEXT NOT NULL,
    data_type   TEXT NOT NULL,
    rw_permission TEXT NOT NULL,
    scan_frequency  INTEGER NOT NULL DEFAULT 1000,
    description TEXT,
    status      INTEGER NOT NULL DEFAULT 1,
    created_at  TEXT    DEFAULT (datetime('now', 'localtime')),
    updated_at  TEXT    DEFAULT (datetime('now', 'localtime'))
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_device_address_device_name ON device_address(device_id, name);
CREATE INDEX IF NOT EXISTS idx_device_address_label ON device_address(label);