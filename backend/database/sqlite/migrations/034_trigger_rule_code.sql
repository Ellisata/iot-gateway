-- 触发规则编码：面向事件载荷、外部系统对账的业务稳定标识。
-- 与设备/采集组编码同一格式；创建后不可修改（服务层校验），规则改名不影响下游。
--
-- 存量行回填：用 id 派生一个必然唯一的编码，保证上线即可满足唯一索引。
-- 空串仍被允许存在（部分唯一索引排除空串）：旧数据/直接写库不应因一个尚未
-- 赋码的历史行阻塞整张表，新建规则由服务层强制要求编码。
ALTER TABLE trigger_rule ADD COLUMN code TEXT NOT NULL DEFAULT '';
UPDATE trigger_rule SET code = 'rule_' || replace(id, '-', '') WHERE code = '';
CREATE UNIQUE INDEX IF NOT EXISTS idx_trigger_rule_code ON trigger_rule(code) WHERE code <> '';

-- 触发历史保存编码快照：规则删除后仍能按业务编码追溯。
ALTER TABLE trigger_rule_record ADD COLUMN rule_code TEXT NOT NULL DEFAULT '';
UPDATE trigger_rule_record SET rule_code = COALESCE(
    (SELECT code FROM trigger_rule WHERE trigger_rule.id = trigger_rule_record.rule_id), '')
WHERE rule_code = '';
