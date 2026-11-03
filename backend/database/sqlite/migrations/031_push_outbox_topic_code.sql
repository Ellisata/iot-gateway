-- 断网缓存补发要重建与在线时完全相同的主题。设备编码 / 物模型编码 / 组编码
-- 随批次落盘，避免只能从 records_json 里推导（空批次或旧记录会丢失编码）。
-- 旧行默认空串，回放时由 SpoolBatch 从首条记录回退推导。
ALTER TABLE push_outbox ADD COLUMN device_code TEXT NOT NULL DEFAULT '';
ALTER TABLE push_outbox ADD COLUMN thing_model_code TEXT NOT NULL DEFAULT '';
ALTER TABLE push_outbox ADD COLUMN group_code TEXT NOT NULL DEFAULT '';
