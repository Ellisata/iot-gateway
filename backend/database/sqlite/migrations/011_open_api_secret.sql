-- 用户表
CREATE TABLE IF NOT EXISTS open_api_secret (
    id         TEXT    PRIMARY KEY,
    name   TEXT    NOT NULL,                
    key   TEXT    NOT NULL,                       
    created_at TEXT    DEFAULT (datetime('now', 'localtime')), -- 创建时间
    updated_at TEXT    DEFAULT (datetime('now', 'localtime'))  -- 更新时间
);
