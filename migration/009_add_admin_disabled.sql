-- dwxComment 迁移 v9：管理员账号启用/禁用状态
--   is_disabled  账号是否被禁用（1=禁用，0=正常）。被禁用账号无法登录，需站长在后台手动启用。
ALTER TABLE admins ADD COLUMN is_disabled INTEGER NOT NULL DEFAULT 0;
