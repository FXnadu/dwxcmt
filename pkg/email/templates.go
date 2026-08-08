package email

import (
	"bytes"
	"embed"
	"fmt"
	"html/template"
	"time"

	"dwxcmt/model"
)

//go:embed templates/*.html
var templateFS embed.FS

var (
	newCommentTpl *template.Template
	replyTpl      *template.Template
)

func init() {
	var err error
	newCommentTpl, err = template.ParseFS(templateFS, "templates/new_comment.html")
	if err != nil {
		panic(fmt.Sprintf("email: 解析新评论邮件模板失败: %v", err))
	}
	replyTpl, err = template.ParseFS(templateFS, "templates/reply.html")
	if err != nil {
		panic(fmt.Sprintf("email: 解析回复邮件模板失败: %v", err))
	}
}

// newCommentData 新评论通知 HTML 模板数据
type newCommentData struct {
	SiteName   string
	Nick       string
	Email      string
	PageID     string
	CreateTime string
	Content    string
}

// replyData 回复通知 HTML 模板数据
type replyData struct {
	SiteName      string
	ParentContent string
	Nick          string
	CreateTime    string
	Content       string
}

// renderNewCommentHTML 渲染新评论通知 HTML 正文
func renderNewCommentHTML(siteName string, c *model.Comment) (string, error) {
	var buf bytes.Buffer
	data := newCommentData{
		SiteName:   siteName,
		Nick:       c.Nick,
		Email:      c.Email,
		PageID:     c.PageID,
		CreateTime: time.Unix(c.CreateTime, 0).Format("2006-01-02 15:04"),
		Content:    c.Content,
	}
	if err := newCommentTpl.Execute(&buf, data); err != nil {
		return "", err
	}
	return buf.String(), nil
}

// renderReplyHTML 渲染回复通知 HTML 正文
func renderReplyHTML(siteName string, c *model.Comment, parent *model.Comment) (string, error) {
	var buf bytes.Buffer
	data := replyData{
		SiteName:      siteName,
		ParentContent: parent.Content,
		Nick:          c.Nick,
		CreateTime:    time.Unix(c.CreateTime, 0).Format("2006-01-02 15:04"),
		Content:       c.Content,
	}
	if err := replyTpl.Execute(&buf, data); err != nil {
		return "", err
	}
	return buf.String(), nil
}
