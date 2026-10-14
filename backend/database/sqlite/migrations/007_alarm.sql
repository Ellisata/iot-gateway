-- 断联报警（设备 / 推送通道统一落库）
-- target_type: device=设备断联, channel=推送通道断联, point=点位质量异常
--
-- device_id 在 target_type='point' 与 'point_threshold' 的行上填写（质量报警与
-- 超限报警都要能反查回设备），含两者写下的 recover 历史行；device / channel 行恒为空串。
-- 点位质量报警的 target_id 是**点位自身**（device_address.id），看不回设备，
-- 于是"删除设备 / 停用设备时连同清掉其点位的活跃报警"无从下手 —— 删除设备时
-- 点位行已在同一事务里删除，事后再按 device_id 反查点位已经查不到了。
-- 采集仍在进行时，停用/删除设备会让那些点位再没有任何信号来恢复它们的活跃报警，
-- 于是永久挂在"未恢复"上。这一列是这条清理路径的唯一反查依据。
-- （设备与推送通道的 target_id 就是自己，不需要它。）
--
-- 注意：迁移器按 ASCII 分号朴素切分 SQL（见 database/sqlite/migrator.go 的 splitSQL），
-- 因此下面语句的字符串字面量内不得出现 ASCII 分号。
CREATE TABLE IF NOT EXISTS alarm (
    id               TEXT PRIMARY KEY,
    target_id        TEXT NOT NULL,
    target_name      TEXT NOT NULL,
    target_type      TEXT NOT NULL DEFAULT 'device',
    alarm_type       TEXT NOT NULL,
    device_id        TEXT NOT NULL DEFAULT '',
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
CREATE INDEX IF NOT EXISTS idx_alarm_device ON alarm(device_id, target_type, status);
CREATE INDEX IF NOT EXISTS idx_alarm_time   ON alarm(first_occur_time);
