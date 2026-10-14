-- 报警 Webhook 通知渠道（钉钉 / 企业微信 / 飞书 / 通用自定义）
--
-- 报警除落库外，同时推送到配置的群机器人。URL 本身即凭证
-- （钉钉 access_token / 企微 key / 飞书 hook uuid），故不加脱敏地保存与返回，
-- 与 push_channel.config_json 中保存 MQTT 口令的既有做法一致；
-- secret（加签密钥）则由 API 层保证绝不回显。
--
-- 注意：迁移器按 ASCII 分号朴素切分 SQL（见 database/sqlite/migrator.go 的 splitSQL），
-- 因此下面 form_json 的字符串值内不得出现 ASCII 分号，也不得出现单引号。
-- 中文全角标点不受影响。
--
-- 用 INSERT OR REPLACE：在已有库上重放该迁移时（清掉 schema_migrations 记录后），
-- 普通 INSERT 会因主键冲突让整个迁移失败。

CREATE TABLE IF NOT EXISTS alarm_webhook (
    id          TEXT    PRIMARY KEY,
    name        TEXT    NOT NULL UNIQUE,
    type        TEXT    NOT NULL,
    description TEXT,
    url         TEXT    NOT NULL,
    secret      TEXT,
    msg_type    TEXT    NOT NULL DEFAULT 'markdown',
    at_all      INTEGER NOT NULL DEFAULT 0,
    at_list     TEXT,
    status      INTEGER NOT NULL DEFAULT 1,
    created_at  TEXT    DEFAULT (datetime('now', 'localtime')),
    updated_at  TEXT    DEFAULT (datetime('now', 'localtime'))
);

CREATE INDEX IF NOT EXISTS idx_alarm_webhook_status ON alarm_webhook(status);

-- 报警通知表单：与 push_channel_form 同构，由前端按 name 拉取后动态渲染配置页。
-- 独立于 push_channel_form，避免 push 通道选择器把 webhook 类型也枚举出来。
CREATE TABLE IF NOT EXISTS alarm_webhook_form (
    id          TEXT    PRIMARY KEY,
    name        TEXT    NOT NULL UNIQUE,
    form_json   TEXT    NOT NULL,
    created_at  TEXT    DEFAULT (datetime('now', 'localtime')),
    updated_at  TEXT    DEFAULT (datetime('now', 'localtime'))
);

INSERT OR REPLACE INTO "main"."alarm_webhook_form" ("id", "name", "form_json", "created_at", "updated_at") VALUES ('01a0200bd7e17f2a8b3c4d5e6f700001', 'dingtalk', '{"rule":[{"type":"input","field":"url","title":"Webhook地址","info":"钉钉群机器人地址，形如 https://oapi.dingtalk.com/robot/send?access_token=xxx。机器人安全设置选「自定义关键词」时，请把「断联报警」加为关键词","$required":true,"display":true,"hidden":false},{"type":"input","field":"secret","title":"签名密钥","info":"机器人安全设置选「加签」时必填（SEC 开头）。未开启加签请留空","$required":false,"display":true,"hidden":false},{"type":"select","field":"msgType","title":"消息格式","info":"默认 markdown","$required":false,"options":[{"label":"markdown","value":"markdown"},{"label":"text","value":"text"}],"display":true,"hidden":false},{"type":"select","field":"atAll","title":"@所有人","info":"","$required":false,"options":[{"label":"是","value":true},{"label":"否","value":false}],"display":true,"hidden":false},{"type":"input","field":"atList","title":"@手机号","info":"多个手机号用英文逗号分隔。留空表示不@具体人","$required":false,"display":true,"hidden":false}],"options":{"form":{"inline":false,"hideRequiredAsterisk":false,"labelPosition":"right","size":"default","labelWidth":"125px"},"resetBtn":{"show":false,"innerText":"重置"},"submitBtn":{"show":false,"innerText":"提交"}}}', '2026-09-24 10:00:00', '2026-09-24 10:00:00');

INSERT OR REPLACE INTO "main"."alarm_webhook_form" ("id", "name", "form_json", "created_at", "updated_at") VALUES ('01a0200bd7e17f2a8b3c4d5e6f700002', 'wecom', '{"rule":[{"type":"input","field":"url","title":"Webhook地址","info":"企业微信群机器人地址，形如 https://qyapi.weixin.qq.com/cgi-bin/webhook/send?key=xxx","$required":true,"display":true,"hidden":false},{"type":"select","field":"msgType","title":"消息格式","info":"markdown 正文上限 4096 字节，text 上限 2048 字节，超出自动截断","$required":false,"options":[{"label":"markdown","value":"markdown"},{"label":"text","value":"text"}],"display":true,"hidden":false}],"options":{"form":{"inline":false,"hideRequiredAsterisk":false,"labelPosition":"right","size":"default","labelWidth":"125px"},"resetBtn":{"show":false,"innerText":"重置"},"submitBtn":{"show":false,"innerText":"提交"}}}', '2026-09-24 10:00:00', '2026-09-24 10:00:00');

INSERT OR REPLACE INTO "main"."alarm_webhook_form" ("id", "name", "form_json", "created_at", "updated_at") VALUES ('01a0200bd7e17f2a8b3c4d5e6f700003', 'feishu', '{"rule":[{"type":"input","field":"url","title":"Webhook地址","info":"飞书群机器人地址，形如 https://open.feishu.cn/open-apis/bot/v2/hook/xxx","$required":true,"display":true,"hidden":false},{"type":"input","field":"secret","title":"签名密钥","info":"机器人开启「签名校验」时必填。未开启请留空","$required":false,"display":true,"hidden":false},{"type":"select","field":"msgType","title":"消息格式","info":"默认 text，card 为带标题色块的交互式卡片","$required":false,"options":[{"label":"text","value":"text"},{"label":"card","value":"card"}],"display":true,"hidden":false},{"type":"select","field":"atAll","title":"@所有人","info":"","$required":false,"options":[{"label":"是","value":true},{"label":"否","value":false}],"display":true,"hidden":false},{"type":"input","field":"atList","title":"@用户open_id","info":"多个 open_id 用英文逗号分隔。留空表示不@具体人","$required":false,"display":true,"hidden":false}],"options":{"form":{"inline":false,"hideRequiredAsterisk":false,"labelPosition":"right","size":"default","labelWidth":"125px"},"resetBtn":{"show":false,"innerText":"重置"},"submitBtn":{"show":false,"innerText":"提交"}}}', '2026-09-24 10:00:00', '2026-09-24 10:00:00');

INSERT OR REPLACE INTO "main"."alarm_webhook_form" ("id", "name", "form_json", "created_at", "updated_at") VALUES ('01a0200bd7e17f2a8b3c4d5e6f700004', 'custom', '{"rule":[{"type":"input","field":"url","title":"Webhook地址","info":"接收报警 JSON 的任意 HTTP 端点，请求方法为 POST","$required":true,"display":true,"hidden":false},{"type":"input","field":"secret","title":"Bearer Token","info":"可选。填写后以 Authorization: Bearer token 形式发送","$required":false,"display":true,"hidden":false}],"options":{"form":{"inline":false,"hideRequiredAsterisk":false,"labelPosition":"right","size":"default","labelWidth":"125px"},"resetBtn":{"show":false,"innerText":"重置"},"submitBtn":{"show":false,"innerText":"提交"}}}', '2026-09-24 10:00:00', '2026-09-24 10:00:00');
