-- dwxComment 迁移 v8：多管理员与权限模型（站长审批制）
-- 注册入口不再关闭（首个注册后仍可继续注册）：
--   is_owner    站长标记（首个注册账号），可审批新账号、授予/收回删除权限
--   can_delete  删除评论权限（站长默认持有，其余账号由站长授予）
--   is_approved 是否已通过站长审批（首个账号默认通过；后续注册默认待审批，通过后方可登录）
ALTER TABLE admins ADD COLUMN can_delete INTEGER NOT NULL DEFAULT 0;
ALTER TABLE admins ADD COLUMN is_approved INTEGER NOT NULL DEFAULT 1;
ALTER TABLE admins ADD COLUMN is_owner INTEGER NOT NULL DEFAULT 0;

-- 存量数据库：最早上注册的账号自动成为站长（已存在的账号直接可用）
UPDATE admins SET can_delete = 1, is_owner = 1 WHERE id = (SELECT MIN(id) FROM admins);
