-- MQTT 订阅的设备 / 点位信息索引
--
-- 订阅数据只进时序库，而时序库只保留 device_id / device_address_id 与数值：
-- 设备名称、点位说明等元信息在查询侧无处可查。本表把订阅报文里
-- 出现过的设备与点位按自然键 (device_id, device_address_id) 收敛成一份索引，
-- 供后续统计分析按名称/说明关联；它不存采样值，采样值仍然只进时序库。
--
-- 元信息更新采用「空值不覆盖已有非空值」，
-- 避免宽松加载的历史订阅（没有配置名称字段）把运维已看到的信息抹掉。
--
-- source 记录维护来源：auto = 订阅报文自动 upsert（默认值）；manual = 手工新增 /
-- 编辑 / Excel 导入。手工行是运维对元信息的最终确认，自动上报不得再覆盖它们
-- （保护逻辑见 subscribe/devicePointUpsert.go）。
CREATE TABLE IF NOT EXISTS mqtt_subscription_device_point (
    device_id            TEXT NOT NULL,
    device_address_id    TEXT NOT NULL,
    device_name          TEXT NOT NULL DEFAULT '',
    device_address_label TEXT NOT NULL DEFAULT '',
    kind                 TEXT NOT NULL DEFAULT '',
    source               TEXT NOT NULL DEFAULT 'auto',
    created_at           TEXT DEFAULT (datetime('now', 'localtime')),
    updated_at           TEXT DEFAULT (datetime('now', 'localtime')),
    PRIMARY KEY (device_id, device_address_id)
);
