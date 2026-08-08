package utils

// 统一业务错误码（对应 docs/04-API接口文档.md §1.2）
const (
	CodeOK                = 0
	CodeErrInvalidParam   = 1001
	CodeErrContentInvalid = 1002
	CodeErrNickInvalid    = 1003
	CodeErrEmailInvalid   = 1004
	CodeErrURLInvalid     = 1005
	CodeErrPageIDRequired = 1006
	CodeErrIPRateLimit    = 2001
	CodeErrDailyLimit     = 2002
	CodeErrDuplicate      = 2003
	CodeErrNotFound       = 3001
	CodeErrLoginFailed    = 3002
	CodeErrTokenExpired   = 3003
	CodeErrTokenInvalid   = 3004
	CodeErrPermission     = 3005
	CodeErrInternal       = 5001
	// OAuth 绑定（7001~7002）
	CodeErrOAuthAlreadyBound = 7001
	CodeErrOAuthNotBound     = 7002
	// 邮箱验证码登录（7003~7008）
	CodeErrSMTPNotConfigured = 7003
	CodeErrEmailCodeInvalid  = 7004
	CodeErrEmailCooldown     = 7005
	CodeErrEmailAlreadyBound = 7006
	CodeErrEmailNotBound     = 7007
	CodeErrEmailDailyLimit   = 7008
	// 多管理员与审批（7009~7011）
	CodeErrUsernameTaken   = 7009 // 用户名已存在
	CodeErrNotApproved     = 7010 // 账号待站长审批，暂不可登录
	CodeErrAccountDisabled = 7011 // 账号已被禁用，暂不可登录
)

// 默认错误提示文案
var codeMessages = map[int]string{
	CodeOK:                   "success",
	CodeErrInvalidParam:      "参数校验失败",
	CodeErrContentInvalid:    "评论内容长度不符合要求（1~1000 字符）",
	CodeErrNickInvalid:       "昵称长度不符合要求（1~20 字符）",
	CodeErrEmailInvalid:      "邮箱格式无效",
	CodeErrURLInvalid:        "URL 格式无效",
	CodeErrPageIDRequired:    "pageId 必填",
	CodeErrIPRateLimit:       "请求过于频繁，请稍后再试",
	CodeErrDailyLimit:        "今日评论已达上限",
	CodeErrDuplicate:         "短时间内重复内容，请勿重复提交",
	CodeErrNotFound:          "评论不存在",
	CodeErrLoginFailed:       "用户名或密码错误",
	CodeErrTokenExpired:      "登录已过期，请重新登录",
	CodeErrTokenInvalid:      "登录凭证无效",
	CodeErrPermission:        "权限不足",
	CodeErrInternal:          "服务器内部错误",
	CodeErrOAuthAlreadyBound: "该第三方账号已绑定其他管理员",
	CodeErrOAuthNotBound:     "未绑定该第三方账号",
	CodeErrSMTPNotConfigured: "SMTP 未配置，无法发送验证码邮件",
	CodeErrEmailCodeInvalid:  "验证码错误或已过期",
	CodeErrEmailCooldown:     "发送过于频繁，请稍后再试",
	CodeErrEmailAlreadyBound: "该邮箱已绑定其他管理员",
	CodeErrEmailNotBound:     "该邮箱未绑定管理员账号",
	CodeErrEmailDailyLimit:   "该邮箱今日发送验证码次数已达上限",
	CodeErrUsernameTaken:     "用户名已存在",
	CodeErrNotApproved:       "账号待站长审批，暂无法登录",
	CodeErrAccountDisabled:   "账号已被禁用，暂无法登录",
}

// Message 返回错误码对应文案
func Message(code int) string {
	if m, ok := codeMessages[code]; ok {
		return m
	}
	return "未知错误"
}
