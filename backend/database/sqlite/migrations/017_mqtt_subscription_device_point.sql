-- MQTT 订阅的设备 / 点位信息索引
--
-- 订阅数据只进时序库，而时序库只保留 device_id / device_address_id 与数值：
-- 设备名称、点位名称、说明、协议类型等元信息在查询侧无处可查。本表把订阅报文里
-- 出现过的设备与点位按自然键 (device_id, device_address_id) 收敛成一份索引，
-- 供后续统计分析按名称/说明关联；它不存采样值，采样值仍然只进时序库。
--
-- first_seen_at 首次出现时间；last_seen_at 最近一次出现时间（运行期按分钟级刷新，
-- 见 subscribe/devicePointStore.go）。元信息更新采用「空值不覆盖已有非空值」，
-- 避免宽松加载的历史订阅（没有配置名称字段）把运维已看到的信息抹掉。
CREATE TABLE IF NOT EXISTS mqtt_subscription_device_point (
    device_id            TEXT NOT NULL,
    device_address_id    TEXT NOT NULL,
    device_name          TEXT NOT NULL DEFAULT '',
    device_address_name  TEXT NOT NULL DEFAULT '',
    device_address_label TEXT NOT NULL DEFAULT '',
    data_type            TEXT NOT NULL DEFAULT '',
    kind                 TEXT NOT NULL DEFAULT '',
    protocol             TEXT NOT NULL DEFAULT '',
    first_seen_at        TEXT NOT NULL,
    last_seen_at         TEXT NOT NULL,
    created_at           TEXT DEFAULT (datetime('now', 'localtime')),
    updated_at           TEXT DEFAULT (datetime('now', 'localtime')),
    PRIMARY KEY (device_id, device_address_id)
);
