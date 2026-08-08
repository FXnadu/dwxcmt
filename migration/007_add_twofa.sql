-- dwxComment 迁移 v7：管理员两步验证（邮箱验证码 2FA）
-- 新增 twofa_enabled 开关列，默认关闭；开启前置条件为已绑定邮箱（见 service/twofa.go）
ALTER TABLE admins ADD COLUMN twofa_enabled INTEGER NOT NULL DEFAULT 0;
