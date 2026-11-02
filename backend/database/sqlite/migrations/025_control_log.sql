-- 控制下发审计：每个点位一条记录。
--
-- 控制是高风险动作，必须能事后回答"谁、从哪来、对哪台设备的哪个点、请求写什么、
-- 实际写成什么、是否回读确认"。采集数据不落本地库，但审计记录必须留在本地。
--
-- request_id 是调用方提供的幂等键：非空时网关先查这张表，已有记录就直接返回
-- 上次结果，避免 MQTT QoS1 / 前端重试造成重复下发。为空表示不做幂等去重。
--
-- 不建外键：审计记录必须比设备/点位活得更久，设备删除后仍要能追溯。device_id /
-- address_id 冗余保存文本 id，同时把当时的名称、协议、类型、权限一并快照下来。
CREATE TABLE IF NOT EXISTS control_log (
    id              TEXT PRIMARY KEY,
    request_id      TEXT NOT NULL DEFAULT '',
    source          TEXT NOT NULL DEFAULT '',
    operator        TEXT NOT NULL DEFAULT '',

    device_id       TEXT NOT NULL,
    device_name     TEXT NOT NULL DEFAULT '',
    protocol_name   TEXT NOT NULL DEFAULT '',

    address_id      TEXT NOT NULL,
    address_name    TEXT NOT NULL DEFAULT '',
    address_label   TEXT NOT NULL DEFAULT '',
    data_type       TEXT NOT NULL DEFAULT '',
    rw_permission   TEXT NOT NULL DEFAULT '',

    request_value   TEXT NOT NULL DEFAULT '',
    result_value    TEXT NOT NULL DEFAULT '',
    readback_value  TEXT NOT NULL DEFAULT '',
    verified        INTEGER NOT NULL DEFAULT 0,

    code            INTEGER NOT NULL DEFAULT 0,
    message         TEXT NOT NULL DEFAULT '',
    created_at      TEXT DEFAULT (datetime('now', 'localtime'))
);
CREATE INDEX IF NOT EXISTS idx_control_log_request ON control_log(request_id);
CREATE INDEX IF NOT EXISTS idx_control_log_device ON control_log(device_id, created_at);
CREATE INDEX IF NOT EXISTS idx_control_log_address ON control_log(address_id, created_at);
CREATE INDEX IF NOT EXISTS idx_control_log_time ON control_log(created_at);
