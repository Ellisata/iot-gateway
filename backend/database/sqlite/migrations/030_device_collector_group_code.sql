-- 设备与采集组编码：面向 MQTT 主题、导入导出与外部引用的稳定标识。
-- 编码创建后不可修改（服务层校验）；主题模板后续使用 code 而不是显示名称。
--
-- 存量行回填：用 id 派生一个必然唯一的编码，保证上线即可满足唯一索引。
-- 空串仍被允许存在（部分唯一索引排除空串）：单测/旧数据/直接写库不应因
-- 一个尚未赋码的历史行阻塞整张表，新建设备与采集组由服务层强制要求编码。
ALTER TABLE device ADD COLUMN code TEXT NOT NULL DEFAULT '';
UPDATE device SET code = 'dev_' || replace(id, '-', '') WHERE code = '';
CREATE UNIQUE INDEX IF NOT EXISTS idx_device_code ON device(code) WHERE code <> '';

ALTER TABLE collector_group ADD COLUMN code TEXT NOT NULL DEFAULT '';
UPDATE collector_group SET code = 'grp_' || replace(id, '-', '') WHERE code = '';
-- 采集组编码只需设备内唯一：完整主题仍会带上设备编码（或设备 ID）。
CREATE UNIQUE INDEX IF NOT EXISTS idx_collector_group_device_code
    ON collector_group(device_id, code) WHERE code <> '';
