-- MQTT 订阅的可定制解析器
--
-- parser_type 选择解析方式：builtin（默认）= 声明式映射，沿用 mapping_json；
-- script = goja 脚本，配置存 parser_config。同一类型只有一处配置真源：
-- builtin 读 mapping_json，其它类型读 parser_config。
--
-- 现有行不迁移、不复制配置：默认值 builtin 让历史订阅继续按 mapping_json 运行。
ALTER TABLE mqtt_subscription ADD COLUMN parser_type TEXT NOT NULL DEFAULT 'builtin';
ALTER TABLE mqtt_subscription ADD COLUMN parser_config TEXT;
-- 样例主题只供管理界面预览 topicPattern，运行期不参与解析。
ALTER TABLE mqtt_subscription ADD COLUMN sample_topic TEXT;
