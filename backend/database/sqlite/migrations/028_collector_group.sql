-- 采集组：设备下的点位分组，组持有采集周期，是采集/上报/订阅的基本单位。
-- group_id 为空的点位继续按自身 scan_frequency 采集（升级前的行为），
-- 只有显式建组并绑定点位后才按组周期采集。
CREATE TABLE IF NOT EXISTS collector_group (
    id          TEXT PRIMARY KEY,
    device_id   TEXT NOT NULL,
    name        TEXT NOT NULL,
    interval_ms INTEGER NOT NULL DEFAULT 1000,
    description TEXT NOT NULL DEFAULT '',
    status      INTEGER NOT NULL DEFAULT 1,
    created_at  TEXT DEFAULT (datetime('now','localtime')),
    updated_at  TEXT DEFAULT (datetime('now','localtime'))
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_collector_group_device_name
    ON collector_group(device_id, name);
CREATE INDEX IF NOT EXISTS idx_collector_group_device_status
    ON collector_group(device_id, status);

ALTER TABLE device_address ADD COLUMN group_id TEXT NOT NULL DEFAULT '';
CREATE INDEX IF NOT EXISTS idx_device_address_group ON device_address(group_id);
