-- dwxComment 迁移 v3：移除图床功能残留的 image_url 列
-- 需求变更：不再支持图床上传，评论仅支持 Markdown 图片 URL（存于 content），删除 image_url 列
-- SQLite 3.35+ 支持 DROP COLUMN；image_url 无索引/约束/CHECK，可安全删除

ALTER TABLE comments DROP COLUMN image_url;
