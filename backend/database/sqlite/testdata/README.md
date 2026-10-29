# protocol_forms_v0.1.0.json

`iot_protocol.form_json` 在 v0.1.0 插件里的原始表单快照，供
`024_seed_protocol_form_i18n.sql` 的 guard 与回归测试使用。

- 来源：`iot-gateway-drivers` 仓库 tag `v0.1.0`
- 采集命令：`git show v0.1.0:<main.go>` 后提取各 `Protocol{Name, FormJSON}` 配对
- SHA-256：`12495f2bb36d146e71f759ef15f176dc349d51a47521fcc7cae74c5b83170d45`
- 不要手改：它是「未被运维定制过的旧行」的判据；与迁移里的
  `replace(replace(form_json, char(13), ''), char(10), '')` 归一化后比较。
