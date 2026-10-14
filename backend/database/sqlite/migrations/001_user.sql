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