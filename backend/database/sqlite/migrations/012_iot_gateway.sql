-- 网关自身身份表（单行）。
--
-- 该行由启动期的 seedGatewayIdentity 播种并同步（见 database/sqlite/seedGateway.go）：
-- 首次启动生成整行；此后每次启动**只同步 firmware_version**，其余字段不再覆盖。
-- 建行之后各字段的归属：
--
--   id               永不变，这台网关的唯一标识（克隆数据库会连它一起克隆）
--   name             归运维，走「系统设置」页改（GatewayService.UpdateGateway）
--   description      同上
--   firmware_version 归配置（default.yaml 的 gateway.version / IOT_GATEWAY_VERSION），
--                    每次启动比对，不一致即刷新 —— 升级二进制后自动跟上
--   port             取网关自己的监听端口 server.port，只在建行时写入
--
-- 所以 default.yaml 里首次启动之后再改 gateway.name 是不生效的，
-- 那是预期行为：改名请走页面；而改 gateway.version 每次启动都会生效。
--
-- firmware_version 的语义是「网关自身的软件版本」，不是所连设备的固件版本。
CREATE TABLE IF NOT EXISTS iot_gateway (
    id               TEXT    PRIMARY KEY,                          -- 网关唯一标识（UUID，首次启动生成；克隆数据库会连它一起克隆）
    name             TEXT    NOT NULL DEFAULT '',                  -- 网关名称（gateway.name → 主机名 → IoT Gateway）
    port             INTEGER NOT NULL DEFAULT 0,                   -- 网关服务端口（server.port）
    firmware_version TEXT    NOT NULL DEFAULT '',                  -- 网关软件版本（gateway.version）
    description      TEXT,                                         -- 备注
    status           INTEGER NOT NULL DEFAULT 1,                   -- 状态：1-启用 0-禁用
    created_at       TEXT    DEFAULT (datetime('now', 'localtime')),
    updated_at       TEXT    DEFAULT (datetime('now', 'localtime'))
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_iot_gateway_name ON iot_gateway(name);
