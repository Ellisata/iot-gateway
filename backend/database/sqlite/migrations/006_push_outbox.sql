-- 推送断网本地缓存(持久化 outbox)
-- 采集批次在通道断连/内存队列满时写入本表,重连后按 id(入队序)补发,发布成功删除。
-- 重启进程不丢:未发完的行保留,下次启动的补发协程继续。
CREATE TABLE IF NOT EXISTS push_outbox (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    channel_id   TEXT NOT NULL,
    device_id    TEXT NOT NULL,
    collected_at TEXT NOT NULL,
    records_json TEXT NOT NULL,
    created_at   TEXT NOT NULL DEFAULT (datetime('now','localtime'))
);
CREATE INDEX IF NOT EXISTS idx_outbox_channel ON push_outbox(channel_id, id);
