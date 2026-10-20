-- MQTT 订阅管理
-- 一行 = 一个外部 broker 上的一个 topic 订阅 + 一套「JSON 报文 → CollectedRecord」映射规则。
-- 订阅数据不做本地缓存，转换成功后交给推送引擎，只投递到时序数据库类通道。
CREATE TABLE IF NOT EXISTS mqtt_subscription (
    id           TEXT    PRIMARY KEY,
    name         TEXT    NOT NULL UNIQUE,
    description  TEXT,
    broker       TEXT    NOT NULL,
    port         TEXT,
    client_id    TEXT,
    username     TEXT,
    password     TEXT,
    topic        TEXT    NOT NULL,
    qos          INTEGER NOT NULL DEFAULT 1,
    mapping_json TEXT    NOT NULL DEFAULT '{}',
    sample_json  TEXT,
    sort         INTEGER NOT NULL DEFAULT 1,
    status       INTEGER NOT NULL DEFAULT 1,
    created_at   TEXT    DEFAULT (datetime('now', 'localtime')),
    updated_at   TEXT    DEFAULT (datetime('now', 'localtime'))
);
