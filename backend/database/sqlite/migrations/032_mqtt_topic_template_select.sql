-- MQTT 主题字段改为“模板下拉 + 手动输入”。
-- 只改仍然是 input 的 topic 规则（rule[2] 是 broker/port 之后的主题字段），
-- 已由运维改成其他控件的表单保持原样。选项直接展示编码模板，选中即写入 config_json.topic。
UPDATE push_channel_form
SET form_json = json_set(
        form_json,
        '$.rule[2].type', 'select',
        '$.rule[2].props', json('{"allowCreate":true,"filterable":true,"defaultFirstOption":true,"placeholder":"{{$t.topic_placeholder}}"}'),
        '$.rule[2].options', json('[{"label":"iot/${thingModelCode}/${deviceCode}/${groupCode}","value":"iot/${thingModelCode}/${deviceCode}/${groupCode}"},{"label":"iot/${deviceCode}/${groupCode}","value":"iot/${deviceCode}/${groupCode}"}]'),
        '$.options.language."zh-cn".topic_placeholder', '选择模板或手动输入；多个主题用英文逗号分隔',
        '$.options.language.en.topic_placeholder', 'Pick a template or type a topic, separate multiple topics with commas'
    ),
    updated_at = datetime('now','localtime')
WHERE name = 'mqtt'
  AND json_extract(form_json, '$.rule[2].field') = 'topic'
  AND json_extract(form_json, '$.rule[2].type') = 'input';
