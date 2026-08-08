-- dwxComment 初始迁移：建表 + 索引
-- 幂等（IF NOT EXISTS），可安全重复执行

CREATE TABLE IF NOT EXISTS comments (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    page_id     TEXT    NOT NULL,
    site        TEXT    DEFAULT '',
    nick        TEXT    NOT NULL,
    email       TEXT    DEFAULT '',
    link        TEXT    DEFAULT '',
    content     TEXT    NOT NULL,
    image_url   TEXT    DEFAULT '',
    parent_id   INTEGER DEFAULT 0,
    root_id     INTEGER DEFAULT 0,
    like_count  INTEGER DEFAULT 0,
    is_audited  INTEGER DEFAULT 0,
    is_pinned   INTEGER DEFAULT 0,
    ip          TEXT    DEFAULT '',
    user_agent  TEXT    DEFAULT '',
    create_time INTEGER NOT NULL,
    update_time INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS admins (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    username      TEXT    NOT NULL UNIQUE,
    password_hash TEXT    NOT NULL,
    qq_openid     TEXT    DEFAULT '',
    create_time   INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS settings (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    site        TEXT    NOT NULL DEFAULT 'default',
    key         TEXT    NOT NULL,
    value       TEXT    DEFAULT '',
    update_time INTEGER NOT NULL,
    UNIQUE (site, key)
);

CREATE TABLE IF NOT EXISTS likes (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    comment_id  INTEGER NOT NULL,
    ip          TEXT    NOT NULL,
    create_time INTEGER NOT NULL,
    UNIQUE (comment_id, ip)
);

CREATE TABLE IF NOT EXISTS rate_limits (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    identifier TEXT    NOT NULL,
    date       TEXT    NOT NULL,
    count      INTEGER DEFAULT 0,
    UNIQUE (identifier, date)
);

CREATE TABLE IF NOT EXISTS migration_versions (
    version    INTEGER PRIMARY KEY,
    applied_at INTEGER NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_page_site_time ON comments (page_id, site, is_audited, is_pinned, create_time DESC);
CREATE INDEX IF NOT EXISTS idx_root_id       ON comments (root_id);
CREATE INDEX IF NOT EXISTS idx_audited        ON comments (is_audited);
CREATE INDEX IF NOT EXISTS idx_parent_id     ON comments (parent_id);
CREATE INDEX IF NOT EXISTS idx_email          ON comments (email);
CREATE INDEX IF NOT EXISTS idx_site_single   ON comments (site);
