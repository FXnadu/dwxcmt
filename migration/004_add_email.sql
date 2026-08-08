-- dwxComment 迁移 v4：管理员邮箱登录绑定字段
-- 为 admins 表添加 email 字段，并建立唯一部分索引（空串不参与唯一约束）

ALTER TABLE admins ADD COLUMN email TEXT DEFAULT '';

CREATE UNIQUE INDEX IF NOT EXISTS idx_admins_email ON admins (email) WHERE email <> '';
