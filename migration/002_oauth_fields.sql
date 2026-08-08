-- dwxComment 迁移 v2：扩展管理员 OAuth 绑定字段
-- 为 admins 表添加 GitHub 绑定字段及 QQ 昵称字段

ALTER TABLE admins ADD COLUMN github_openid TEXT DEFAULT '';
ALTER TABLE admins ADD COLUMN github_name   TEXT DEFAULT '';
ALTER TABLE admins ADD COLUMN qq_name       TEXT DEFAULT '';
