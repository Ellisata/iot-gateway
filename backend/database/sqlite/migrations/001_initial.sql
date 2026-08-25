-- 用户表
CREATE TABLE IF NOT EXISTS sys_user (
    id         TEXT    PRIMARY KEY,                    -- 用户ID（UUID）
    username   TEXT    NOT NULL UNIQUE,                -- 用户名
    password   TEXT    NOT NULL,                       -- 密码（bcrypt加密）
    email      TEXT    NOT NULL DEFAULT '',            -- 邮箱
    phone      TEXT    NOT NULL DEFAULT '',            -- 手机号
    avatar     TEXT    NOT NULL DEFAULT '',            -- 头像URL
    status     INTEGER NOT NULL DEFAULT 1,             -- 状态：1-启用 0-禁用
    created_at TEXT    DEFAULT (datetime('now', 'localtime')), -- 创建时间
    updated_at TEXT    DEFAULT (datetime('now', 'localtime'))  -- 更新时间
);

CREATE INDEX IF NOT EXISTS idx_sys_user_username ON sys_user(username);

INSERT OR IGNORE INTO sys_user(id, username, password)
VALUES ('416d87afd48b41c9848d321d0e6ca829', 'admin', '$2b$10$ebGIkDcE9KNau4jfdoUETuR3pBlRVinWclKjgYSocurfvOEamYaQW');
INSERT OR IGNORE INTO sys_user(id, username, password)
VALUES ('01a0234d5e7977aca8e0f9c0a7c60250', 'hand', '$2b$10$ebGIkDcE9KNau4jfdoUETuR3pBlRVinWclKjgYSocurfvOEamYaQW');
