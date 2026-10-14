-- 点位报警条件：device_address 新增三列（开关 + 比较符 + 操作数）。
--
-- 比较符与操作数把三类数据类型统一到同一套模型上（矩阵的真源见 driver/thresholdOp.go）：
--   数值  range / gt / ge / lt / le / eq / ne
--   布尔  eq / ne
--   文本  eq / ne / in
--   时间  不支持（那类需求其实比的是"多久没更新"，不是配置边界）
-- 不为数值单独留上限下限两列，是因为布尔与文本要的根本不是区间 ——
-- 三种形状塞进两列会让数据模型和界面都在说谎。
--
-- threshold_value 是操作数的 **JSON 数组字符串**（如 ["10","40"] / ["FAULT"] / ["A","B"]）。
-- 不用逗号分隔：故障码里出现逗号是可能的，分隔符方案会在那种值上静默错切。
ALTER TABLE device_address ADD COLUMN threshold_enabled INTEGER NOT NULL DEFAULT 0;
ALTER TABLE device_address ADD COLUMN threshold_op TEXT NOT NULL DEFAULT '';
ALTER TABLE device_address ADD COLUMN threshold_value TEXT NOT NULL DEFAULT '';

-- 越限记录：条件命中瞬间持久化的样本。
--
-- 网关刻意不在本地保存采集数据（历史趋势由外部时序库承担），本表是窄范围例外：
-- 只记越限样本、只为排障与对账，且带自动保留清理（见 alarm/threshold.go 的惰性清理）。
-- threshold_op / threshold_value 是写入时的快照，运维改配置后历史记录仍可自证。
-- bound 记录是条件里的哪一部分命中：range 为 high / low，其余算子即算子名本身。
CREATE TABLE IF NOT EXISTS point_threshold_record (
    id             TEXT PRIMARY KEY,
    device_id      TEXT NOT NULL,
    device_name    TEXT NOT NULL,
    address_id     TEXT NOT NULL,
    address_name   TEXT NOT NULL,
    value          TEXT NOT NULL,
    kind           TEXT NOT NULL DEFAULT '',
    bound          TEXT NOT NULL,
    threshold_op   TEXT NOT NULL DEFAULT '',
    threshold_value TEXT NOT NULL DEFAULT '',
    occurred_at    TEXT NOT NULL,
    created_at     TEXT DEFAULT (datetime('now', 'localtime'))
);
CREATE INDEX IF NOT EXISTS idx_point_threshold_record_device ON point_threshold_record(device_id, occurred_at);
CREATE INDEX IF NOT EXISTS idx_point_threshold_record_address ON point_threshold_record(address_id, occurred_at);
CREATE INDEX IF NOT EXISTS idx_point_threshold_record_time ON point_threshold_record(occurred_at);
