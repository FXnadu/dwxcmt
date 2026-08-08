package controller

import (
	"errors"
	"html/template"
	"log"
	"net/http"
	"strings"

	"dwxcmt/model"
	"dwxcmt/service"
)

// oauthCallbackTmpl OAuth 回调页模板。
// 使用 html/template 的上下文自动转义：provider/msg 等第三方可控内容
// 在 HTML 上下文（<div>）与 JS 字符串上下文（<script> 内单引号串）均被正确转义，
// 防反射型 XSS（旧实现仅转义 \ 与 '，`<script>` 等标签可原样注入）。
var oauthCallbackTmpl = template.Must(template.New("oauth-callback").Parse(`<!DOCTYPE html>
<html lang="zh-CN">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>OAuth 绑定回调</title>
<style>
  body { font-family: -apple-system, "Segoe UI", "PingFang SC", "Microsoft YaHei", sans-serif; display: flex; align-items: center; justify-content: center; min-height: 100vh; margin: 0; background: #f5f5f5; color: #1a1a1a; }
  .box { text-align: center; padding: 40px; }
  .icon { font-size: 48px; margin-bottom: 16px; }
  .msg { font-size: 16px; margin-bottom: 20px; }
  .msg.err { color: #d32f2f; }
  a { color: #0066cc; text-decoration: none; }
  a:hover { text-decoration: underline; }
</style>
</head>
<body>
<div class="box">
  <div class="icon" id="icon">{{if .Success}}✅{{else}}❌{{end}}</div>
  <div class="msg {{if not .Success}}err{{end}}" id="msg">{{.Msg}}</div>
  <a href="/admin/admin.html?tab=profile">返回管理后台</a>
</div>
<script>
(function() {
  var data = { type: 'oauth-callback', provider: '{{.Provider}}', success: {{.SuccessBool}}, msg: '{{.Msg}}' };
  if (window.opener) {
    window.opener.postMessage(data, '*');
    setTimeout(function() { window.close(); }, 100);
  } else {
    setTimeout(function() {
      window.location.href = '/admin/admin.html?tab=profile&oauth=' + encodeURIComponent('{{.Provider}}') + '&status=' + '{{.Status}}';
    }, 1500);
  }
})();
</script>
</body>
</html>`))

// oauthCallbackHTML 返回 OAuth 绑定回调的 HTML 页面
// 该页面通过 postMessage 通知父窗口（弹窗模式），或直接跳转回管理后台（同窗口模式）
func oauthCallbackHTML(provider string, success bool, msg string) string {
	status := "fail"
	successBool := "false"
	if success {
		status = "ok"
		successBool = "true"
	}
	data := struct {
		Provider, Msg, Status, SuccessBool string
		Success                            bool
	}{
		Provider:    provider,
		Msg:         msg,
		Status:      status,
		SuccessBool: successBool,
		Success:     success,
	}
	var buf strings.Builder
	if err := oauthCallbackTmpl.Execute(&buf, data); err != nil {
		log.Printf("[oauth] 渲染回调页失败: %v", err)
		return "OAuth 回调页渲染失败"
	}
	return buf.String()
}

// writeOAuthCallbackHTML 写入 OAuth 回调 HTML 响应
func writeOAuthCallbackHTML(w http.ResponseWriter, provider string, success bool, msg string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(oauthCallbackHTML(provider, success, msg)))
}

// oauthNotConfiguredMsg 通用未配置提示
func oauthNotConfiguredMsg(provider string) string {
	return provider + " OAuth 未配置，请在 config.yaml 中填写相关参数"
}

// writeErr2OAuthHTML 将服务层错误转换为 OAuth 回调 HTML 响应
// 用于 OAuth 回调场景（无法返回 JSON，因为浏览器是重定向过来的）
func writeErr2OAuthHTML(w http.ResponseWriter, provider string, err error) {
	if err == nil {
		writeOAuthCallbackHTML(w, provider, true, "绑定成功")
		return
	}
	var ve *service.ErrValidation
	if errors.As(err, &ve) {
		writeOAuthCallbackHTML(w, provider, false, ve.Msg)
		return
	}
	if errors.Is(err, model.ErrNotFound) {
		writeOAuthCallbackHTML(w, provider, false, "管理员账号不存在")
		return
	}
	log.Printf("[oauth-error] %v", err)
	writeOAuthCallbackHTML(w, provider, false, "服务器内部错误，请稍后重试")
}
