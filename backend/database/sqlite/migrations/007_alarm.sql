-- 断联报警（设备 / 推送通道统一落库）
-- target_type: device=设备断联, channel=推送通道断联
CREATE TABLE IF NOT EXISTS alarm (
    id               TEXT PRIMARY KEY,
    target_id        TEXT NOT NULL,
    target_name      TEXT NOT NULL,
    target_type      TEXT NOT NULL DEFAULT 'device',
    alarm_type       TEXT NOT NULL,
    level            TEXT NOT NULL DEFAULT 'warning',
    content          TEXT NOT NULL,
    status           TEXT NOT NULL DEFAULT 'active',
    first_occur_time TEXT NOT NULL,
    last_occur_time  TEXT NOT NULL,
    clear_time       TEXT,
    created_at       TEXT DEFAULT (datetime('now', 'localtime')),
    updated_at       TEXT DEFAULT (datetime('now', 'localtime'))
);

CREATE INDEX IF NOT EXISTS idx_alarm_target ON alarm(target_id, target_type, status);
CREATE INDEX IF NOT EXISTS idx_alarm_time   ON alarm(first_occur_time);
