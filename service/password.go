package service

import (
	"database/sql"
	"errors"

	"golang.org/x/crypto/bcrypt"

	"dwxcmt/pkg/utils"
)

// ChangePassword 修改管理员密码：校验旧密码并写入新密码 hash。
// 新密码不合规 → 1001；旧密码错误 → 3002。
func (s *Service) ChangePassword(adminID int64, oldPwd, newPwd string) error {
	if err := ValidatePassword(newPwd); err != nil {
		return err
	}
	var hash string
	err := s.DB.QueryRow(`SELECT password_hash FROM admins WHERE id = ?`, adminID).Scan(&hash)
	if errors.Is(err, sql.ErrNoRows) {
		return newValidationErr(utils.CodeErrLoginFailed)
	}
	if err != nil {
		return err
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte(oldPwd)) != nil {
		return newValidationErr(utils.CodeErrLoginFailed)
	}
	newHash, err := bcrypt.GenerateFromPassword([]byte(newPwd), bcryptCost) // cost=12，与注册一致
	if err != nil {
		return err
	}
	_, err = s.DB.Exec(`UPDATE admins SET password_hash = ? WHERE id = ?`, string(newHash), adminID)
	return err
}
