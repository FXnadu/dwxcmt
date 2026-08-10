-- dwxComment v2.1 迁移：置顶评论按「置顶时间」排序，新增 pin_time 字段
-- 已置顶的旧数据用 update_time（置顶时写入）回填，迁移前后置顶顺序保持一致

ALTER TABLE comments ADD COLUMN pin_time INTEGER DEFAULT 0;
UPDATE comments SET pin_time = update_time WHERE is_pinned = 1;
CREATE INDEX IF NOT EXISTS idx_page_site_pin ON comments (page_id, site, is_audited, is_pinned, pin_time DESC);
