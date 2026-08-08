-- dwxComment 迁移 v5：站长回复标记字段
-- 为 comments 表添加 is_admin 字段：1=站长（管理员）发布的回复，0=普通用户
-- 站长回复在前台展示「站长」身份徽章

ALTER TABLE comments ADD COLUMN is_admin INTEGER DEFAULT 0;
