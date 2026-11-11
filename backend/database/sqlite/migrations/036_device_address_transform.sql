-- 点位工程量变换：device_address 新增一列。
--
-- transform_json 存一条有序算子流水线（JSON 数组），在采集侧 toRecords 里对驱动
-- 读回的值执行一次，之后实时快照、阈值报警、触发规则和所有推送通道（含时序库）
-- 看到的是同一个工程值。算子白名单只有 scale / round / trim / default，
-- 见 mappingdsl/normalization.go。
--
-- 空串表示未配置。出站 mapping-json 的 transform 是另一层（展示变换），
-- 两处不要重复配置同一个 scale。
ALTER TABLE device_address ADD COLUMN transform_json TEXT NOT NULL DEFAULT '';
