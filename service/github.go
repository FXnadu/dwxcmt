package service

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

// ErrGitHubNotConfigured GitHub OAuth 未配置
var ErrGitHubNotConfigured = errors.New("github oauth not configured")

// GitHubAuthorizeURL 生成 GitHub 授权跳转地址
func (s *Service) GitHubAuthorizeURL(state string) (string, error) {
	cfg := s.Cfg.GitHubOAuth
	if cfg.ClientID == "" || cfg.RedirectURI == "" {
		return "", ErrGitHubNotConfigured
	}
	return "https://github.com/login/oauth/authorize?" + url.Values{
		"client_id":    {cfg.ClientID},
		"redirect_uri": {cfg.RedirectURI},
		"state":        {state},
		"scope":        {"read:user"},
	}.Encode(), nil
}

// GitHubUserInfo GitHub 用户信息
type GitHubUserInfo struct {
	ID    int64  `json:"id"`
	Login string `json:"login"`
	Name  string `json:"name"`
}

// GitHubGetUser 用授权 code 换取 GitHub 用户信息（两步：code → access_token → user info）
func (s *Service) GitHubGetUser(code string) (*GitHubUserInfo, error) {
	cfg := s.Cfg.GitHubOAuth
	if cfg.ClientID == "" || cfg.ClientSecret == "" || cfg.RedirectURI == "" {
		return nil, ErrGitHubNotConfigured
	}

	// Step 1: code → access_token
	tokenURL := "https://github.com/login/oauth/access_token"
	form := url.Values{
		"client_id":     {cfg.ClientID},
		"client_secret": {cfg.ClientSecret},
		"code":          {code},
		"redirect_uri":  {cfg.RedirectURI},
	}
	req, err := http.NewRequest("POST", tokenURL, nil)
	if err != nil {
		return nil, err
	}
	req.URL.RawQuery = form.Encode()
	req.Header.Set("Accept", "application/json")
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("github token api status %d: %s", resp.StatusCode, string(body))
	}
	var tok struct {
		AccessToken string `json:"access_token"`
		Error       string `json:"error"`
		ErrorDesc   string `json:"error_description"`
	}
	if err := json.Unmarshal(body, &tok); err != nil {
		return nil, err
	}
	if tok.AccessToken == "" {
		return nil, fmt.Errorf("github token error: %s %s", tok.Error, tok.ErrorDesc)
	}

	// Step 2: access_token → user info
	userURL := "https://api.github.com/user"
	req2, err := http.NewRequest("GET", userURL, nil)
	if err != nil {
		return nil, err
	}
	req2.Header.Set("Authorization", "Bearer "+tok.AccessToken)
	req2.Header.Set("Accept", "application/vnd.github+json")
	resp2, err := client.Do(req2)
	if err != nil {
		return nil, err
	}
	defer resp2.Body.Close()
	body2, err := io.ReadAll(io.LimitReader(resp2.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	if resp2.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("github user api status %d: %s", resp2.StatusCode, string(body2))
	}
	var user GitHubUserInfo
	if err := json.Unmarshal(body2, &user); err != nil {
		return nil, err
	}
	if user.ID == 0 {
		return nil, fmt.Errorf("github user id is empty")
	}
	// 优先使用 name，为空时用 login
	displayName := user.Name
	if displayName == "" {
		displayName = user.Login
	}
	return &GitHubUserInfo{
		ID:    user.ID,
		Login: user.Login,
		Name:  displayName,
	}, nil
}
