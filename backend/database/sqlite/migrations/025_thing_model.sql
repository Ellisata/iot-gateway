-- 设备物模型：协议无关的语义模板，供同一类设备复用。
-- 属性只描述“这是什么数据”，具体寄存器地址、协议内部类型、采集频率与报警阈值
-- 仍留在 device_address。物模型是配置元数据，不参与采集与推送执行。
-- 一台设备绑定一个模型，同一设备内一个属性最多绑定一个点位。

CREATE TABLE IF NOT EXISTS thing_model (
    id          TEXT PRIMARY KEY,                                -- UUIDv7 主键
    code        TEXT NOT NULL UNIQUE,                            -- 模型标识，如 th_sensor，创建后不可修改
    name        TEXT NOT NULL UNIQUE,                            -- 模型显示名，如“温湿度传感器”
    category    TEXT NOT NULL DEFAULT '',                        -- 分类，如传感器/仪表/机器人，仅用于筛选展示
    version     TEXT NOT NULL DEFAULT '1.0.0',                   -- 版本号，仅展示，v1 不做多版本绑定
    description TEXT NOT NULL DEFAULT '',                        -- 模型描述
    status      INTEGER NOT NULL DEFAULT 1,                      -- 1 启用 / 0 停用，停用不影响已绑定的采集
    created_at  TEXT DEFAULT (datetime('now', 'localtime')),     -- 创建时间（本地时间）
    updated_at  TEXT DEFAULT (datetime('now', 'localtime'))      -- 更新时间（本地时间）
);

CREATE TABLE IF NOT EXISTS thing_model_property (
    id          TEXT PRIMARY KEY,                                -- UUIDv7 主键
    model_id    TEXT NOT NULL,                                   -- 所属模型 ID，对应 thing_model.id
    identifier  TEXT NOT NULL,                                   -- 属性标识符，如 temperature，模型内唯一，创建后不可改
    name        TEXT NOT NULL,                                   -- 属性显示名，如“温度”
    kind        TEXT NOT NULL,                                   -- 数据类型类别：bool/int/uint/float/string/time
    access_mode TEXT NOT NULL DEFAULT 'R',                       -- 读写模式：R 只读 / W 只写 / RW 读写
    unit        TEXT NOT NULL DEFAULT '',                        -- 工程单位，如 ℃、%、A，仅展示不做换算
    precision   INTEGER NOT NULL DEFAULT -1,                     -- 展示小数位，-1 表示不指定，不参与推送值格式化
    min_value   REAL,                                            -- 数值下界，仅用于写入校验与界面提示，不截断采集值
    max_value   REAL,                                            -- 数值上界，需不小于 min_value
    step        REAL,                                            -- 数值步长，需大于 0
    max_length  INTEGER,                                         -- 字符串最大长度，仅 kind=string 时使用
    enum_spec   TEXT NOT NULL DEFAULT '',                        -- 枚举定义 JSON，如 [{"value":"0","label":"停止"}]
    description TEXT NOT NULL DEFAULT '',                        -- 业务含义说明
    sort_order  INTEGER NOT NULL DEFAULT 0,                      -- 展示顺序，越小越靠前
    status      INTEGER NOT NULL DEFAULT 1,                      -- 1 启用 / 0 停用，停用不解除已有点位绑定
    created_at  TEXT DEFAULT (datetime('now', 'localtime')),     -- 创建时间（本地时间）
    updated_at  TEXT DEFAULT (datetime('now', 'localtime'))      -- 更新时间（本地时间）
);

-- 属性标识符在模型内唯一，是下游映射与开放接口的稳定键。
CREATE UNIQUE INDEX IF NOT EXISTS idx_thing_model_property_model_identifier
    ON thing_model_property(model_id, identifier);

-- 按模型的展示顺序取属性。
CREATE INDEX IF NOT EXISTS idx_thing_model_property_model_sort
    ON thing_model_property(model_id, sort_order);

-- 设备绑定模型。空串 = 未绑定，存量设备无需迁移。
ALTER TABLE device ADD COLUMN model_id TEXT NOT NULL DEFAULT '';
CREATE INDEX IF NOT EXISTS idx_device_model ON device(model_id);

-- 点位绑定属性。空串 = 未绑定，存量点位无需迁移。
ALTER TABLE device_address ADD COLUMN property_id TEXT NOT NULL DEFAULT '';

-- 同一设备内一个属性只能绑一个点位。用部分唯一索引把空串排除在外，
-- 否则同一设备的多个未绑定点位会在这条索引上互相冲突。
CREATE UNIQUE INDEX IF NOT EXISTS idx_device_address_device_property
    ON device_address(device_id, property_id) WHERE property_id <> '';

-- 按属性反查绑定到它的点位。
CREATE INDEX IF NOT EXISTS idx_device_address_property ON device_address(property_id);
