-- dwxComment 迁移 v6：补充长期数据增长后的查询索引
-- comments 表为永久数据（评论不删除），随数据量增长以下查询会退化，故提前补索引：
--   1) 最近评论:   WHERE site = ? AND is_audited = 1 ORDER BY create_time DESC
--   2) 待审列表:   WHERE is_audited = 0 ORDER BY create_time DESC
--   3) 重复检测:   WHERE ip = ? AND page_id = ? AND content = ?（提交评论时执行）
-- likes 表由每小时 CleanupStaleData 按 create_time 清理，无索引会全表扫描：
--   4) 清理语句:   DELETE FROM likes WHERE create_time < ?

CREATE INDEX IF NOT EXISTS idx_site_audited_time ON comments (site, is_audited, create_time DESC);
CREATE INDEX IF NOT EXISTS idx_audited_time      ON comments (is_audited, create_time DESC);
CREATE INDEX IF NOT EXISTS idx_ip_page_time      ON comments (ip, page_id, create_time);
CREATE INDEX IF NOT EXISTS idx_likes_time        ON likes (create_time);
