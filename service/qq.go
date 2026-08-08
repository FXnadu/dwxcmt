package service

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"dwxcmt/model"
)

// ErrQQNotConfigured QQ 互联未配置
var ErrQQNotConfigured = errors.New("qq oauth not configured")

// QQAuthorizeURL 生成 QQ 授权跳转地址（未配置时返回 ErrQQNotConfigured）
func (s *Service) QQAuthorizeURL(state string) (string, error) {
	cfg := s.Cfg.QQOAuth
	if cfg.AppID == "" || cfg.RedirectURI == "" {
		return "", ErrQQNotConfigured
	}
	return "https://graph.qq.com/oauth2.0/authorize?" + url.Values{
		"response_type": {"code"},
		"client_id":     {cfg.AppID},
		"redirect_uri":  {cfg.RedirectURI},
		"state":         {state},
		"scope":         {"get_user_info"},
	}.Encode(), nil
}

// QQGetOpenIDAndName 用授权 code 换取 openid + 昵称（三步：code → access_token → openid → user_info）
// 用于个人中心绑定流程，需要昵称用于展示
func (s *Service) QQGetOpenIDAndName(code string) (openid, nickname string, err error) {
	cfg := s.Cfg.QQOAuth
	if cfg.AppID == "" || cfg.AppKey == "" || cfg.RedirectURI == "" {
		return "", "", ErrQQNotConfigured
	}
	tokenURL := "https://graph.qq.com/oauth2.0/token?" + url.Values{
		"grant_type":    {"authorization_code"},
		"client_id":     {cfg.AppID},
		"client_secret": {cfg.AppKey},
		"code":          {code},
		"redirect_uri":  {cfg.RedirectURI},
		"fmt":           {"json"},
	}.Encode()
	var tok struct {
		AccessToken string `json:"access_token"`
		Error       string `json:"error"`
		ErrorDesc   string `json:"error_description"`
	}
	if err := qqGetJSON(tokenURL, &tok); err != nil {
		return "", "", err
	}
	if tok.AccessToken == "" {
		return "", "", fmt.Errorf("qq token error: %s %s", tok.Error, tok.ErrorDesc)
	}
	meURL := "https://graph.qq.com/oauth2.0/me?" + url.Values{
		"access_token": {tok.AccessToken},
		"fmt":          {"json"},
	}.Encode()
	var me struct {
		OpenID string `json:"openid"`
		Error  string `json:"error"`
	}
	if err := qqGetJSON(meURL, &me); err != nil {
		return "", "", err
	}
	if me.OpenID == "" {
		return "", "", fmt.Errorf("qq me error: %s", me.Error)
	}
	// 获取 QQ 昵称
	infoURL := "https://graph.qq.com/user/get_user_info?" + url.Values{
		"access_token":       {tok.AccessToken},
		"oauth_consumer_key": {cfg.AppID},
		"openid":             {me.OpenID},
	}.Encode()
	var info struct {
		Nickname string `json:"nickname"`
	}
	_ = qqGetJSON(infoURL, &info) // 昵称获取失败不阻断流程
	return me.OpenID, info.Nickname, nil
}

// qqGetJSON 请求 QQ 接口并解析 JSON 响应
func qqGetJSON(rawURL string, v interface{}) error {
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(rawURL)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("qq api status %d: %s", resp.StatusCode, string(body))
	}
	return json.Unmarshal(body, v)
}

// FindAdminByOpenID 按 qq_openid 查找管理员；未绑定时返回 (nil, nil)
func (s *Service) FindAdminByOpenID(openid string) (*model.Admin, error) {
	var a model.Admin
	err := s.DB.QueryRow(
		`SELECT id, username, password_hash, email, qq_openid, qq_name, github_openid, github_name, create_time FROM admins WHERE qq_openid = ?`,
		openid,
	).Scan(&a.ID, &a.Username, &a.PasswordHash, &a.Email, &a.QQOpenID, &a.QQName, &a.GitHubOpenID, &a.GitHubName, &a.CreateTime)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &a, nil
}
