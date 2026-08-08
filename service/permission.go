package service

import (
	"database/sql"
	"errors"

	"dwxcmt/model"
	"dwxcmt/pkg/utils"
)

// AdminInfo 账号管理列表项（站长审批 / 权限管理用）
type AdminInfo struct {
	ID         int64  `json:"id"`
	Username   string `json:"username"`
	IsOwner    bool   `json:"isOwner"`    // 站长（首个注册账号）
	CanDelete  bool   `json:"canDelete"`  // 是否有删除评论权限
	IsApproved bool   `json:"isApproved"` // 是否已通过站长审批
	IsDisabled bool   `json:"isDisabled"` // 是否被禁用
	CreateTime int64  `json:"createTime"`
}

// AdminPermissions 当前管理员权限信息
type AdminPermissions struct {
	IsOwner   bool `json:"isOwner"`
	CanDelete bool `json:"canDelete"`
}

// ListAdmins 返回全部管理员账号（按注册先后排序）
func (s *Service) ListAdmins() ([]AdminInfo, error) {
	rows, err := s.DB.Query(
		`SELECT id, username, can_delete, is_approved, is_disabled, is_owner, create_time FROM admins ORDER BY id ASC`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	list := make([]AdminInfo, 0, 8)
	for rows.Next() {
		var a AdminInfo
		var canDelete, isApproved, isDisabled, isOwner int
		if err := rows.Scan(&a.ID, &a.Username, &canDelete, &isApproved, &isDisabled, &isOwner, &a.CreateTime); err != nil {
			return nil, err
		}
		a.CanDelete = canDelete != 0
		a.IsApproved = isApproved != 0
		a.IsDisabled = isDisabled != 0
		a.IsOwner = isOwner != 0
		list = append(list, a)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return list, nil
}

// GetAdminPermissions 查询指定管理员的权限
func (s *Service) GetAdminPermissions(adminID int64) (AdminPermissions, error) {
	var canDelete, isOwner int
	err := s.DB.QueryRow(
		`SELECT can_delete, is_owner FROM admins WHERE id = ?`, adminID,
	).Scan(&canDelete, &isOwner)
	if errors.Is(err, sql.ErrNoRows) {
		return AdminPermissions{}, model.ErrNotFound
	}
	if err != nil {
		return AdminPermissions{}, err
	}
	return AdminPermissions{IsOwner: isOwner != 0, CanDelete: canDelete != 0}, nil
}

// IsOwner 判断指定管理员是否为站长
func (s *Service) IsOwner(adminID int64) (bool, error) {
	perms, err := s.GetAdminPermissions(adminID)
	if err != nil {
		return false, err
	}
	return perms.IsOwner, nil
}

// AdminCanDelete 判断指定管理员是否具有删除评论权限
func (s *Service) AdminCanDelete(adminID int64) (bool, error) {
	perms, err := s.GetAdminPermissions(adminID)
	if err != nil {
		return false, err
	}
	return perms.CanDelete, nil
}

// requireOwner 校验操作者为站长，否则返回权限不足
func (s *Service) requireOwner(adminID int64) error {
	ok, err := s.IsOwner(adminID)
	if err != nil {
		return err
	}
	if !ok {
		return &ErrValidation{Code: utils.CodeErrPermission, Msg: "仅站长可执行此操作"}
	}
	return nil
}

// ApproveAdmin 站长审批通过新注册的账号
func (s *Service) ApproveAdmin(ownerID, targetID int64) error {
	if err := s.requireOwner(ownerID); err != nil {
		return err
	}
	res, err := s.DB.Exec(`UPDATE admins SET is_approved = 1 WHERE id = ?`, targetID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return model.ErrNotFound
	}
	return nil
}

// SetAdminDelete 站长授予/收回指定账号的删除评论权限。
// 站长自身的删除权限不可修改（站长始终持有）。
func (s *Service) SetAdminDelete(ownerID, targetID int64, canDelete bool) error {
	if err := s.requireOwner(ownerID); err != nil {
		return err
	}
	if ownerID == targetID {
		return &ErrValidation{Code: utils.CodeErrInvalidParam, Msg: "站长自身的删除权限不可修改"}
	}
	v := 0
	if canDelete {
		v = 1
	}
	res, err := s.DB.Exec(`UPDATE admins SET can_delete = ? WHERE id = ?`, v, targetID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return model.ErrNotFound
	}
	return nil
}

// SetAdminDisabled 站长禁用/启用指定账号。
// 站长账号不可被禁用，且操作者不能禁用自己。
func (s *Service) SetAdminDisabled(ownerID, targetID int64, disabled bool) error {
	if err := s.requireOwner(ownerID); err != nil {
		return err
	}
	if ownerID == targetID {
		return &ErrValidation{Code: utils.CodeErrInvalidParam, Msg: "不能禁用当前登录账号"}
	}
	// 站长账号不可被禁用
	targetIsOwner, err := s.IsOwner(targetID)
	if err != nil {
		return err
	}
	if targetIsOwner {
		return &ErrValidation{Code: utils.CodeErrInvalidParam, Msg: "站长账号不可被禁用"}
	}
	v := 0
	if disabled {
		v = 1
	}
	res, err := s.DB.Exec(`UPDATE admins SET is_disabled = ? WHERE id = ?`, v, targetID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return model.ErrNotFound
	}
	return nil
}

// DeleteAdmin 站长删除指定管理员账号。
// 站长账号不可删除，且操作者不能删除自己。
func (s *Service) DeleteAdmin(ownerID, targetID int64) error {
	if err := s.requireOwner(ownerID); err != nil {
		return err
	}
	if ownerID == targetID {
		return &ErrValidation{Code: utils.CodeErrInvalidParam, Msg: "不能删除当前登录账号"}
	}
	targetIsOwner, err := s.IsOwner(targetID)
	if err != nil {
		return err
	}
	if targetIsOwner {
		return &ErrValidation{Code: utils.CodeErrInvalidParam, Msg: "站长账号不可删除"}
	}
	res, err := s.DB.Exec(`DELETE FROM admins WHERE id = ?`, targetID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return model.ErrNotFound
	}
	return nil
}

// RequireAdminActive 校验管理员是否可登录（已通过审批且未被禁用）。
func (s *Service) RequireAdminActive(admin *model.Admin) error {
	if admin.IsApproved == 0 {
		return newValidationErr(utils.CodeErrNotApproved)
	}
	if admin.IsDisabled != 0 {
		return newValidationErr(utils.CodeErrAccountDisabled)
	}
	return nil
}
