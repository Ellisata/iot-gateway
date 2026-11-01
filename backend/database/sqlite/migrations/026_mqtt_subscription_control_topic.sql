-- MQTT 下行控制主题（可选）。
--
-- 该主题上的报文不经过映射/解析器，而是按约定 JSON 直接调用控制服务：
--   {"deviceId":"...","requestId":"...","verify":true,
--    "items":[{"deviceAddressId":"...","value":"42"}]}
--
-- requestId 必填：MQTT QoS1 会重投，没有幂等键就无法阻止重复写 PLC。
-- 为空表示该订阅只做数据采集，不接受控制指令。
ALTER TABLE mqtt_subscription ADD COLUMN control_topic TEXT NOT NULL DEFAULT '';
