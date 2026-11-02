-- 断网缓存按采集组补发：批次携带组身份，补发时才能重建正确的 topic/帧。
-- 旧行组列为空，按 legacy 回放（未分组点位）。
ALTER TABLE push_outbox ADD COLUMN group_id TEXT NOT NULL DEFAULT '';
ALTER TABLE push_outbox ADD COLUMN group_name TEXT NOT NULL DEFAULT '';
ALTER TABLE push_outbox ADD COLUMN group_interval_ms INTEGER NOT NULL DEFAULT 0;
