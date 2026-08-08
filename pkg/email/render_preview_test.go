//go:build manual
// +build manual

package email

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"dwxcmt/model"
)

// TestRenderEmailPreview 用本地测试数据渲染两套邮件模板并打开浏览器预览。
// 运行：go test -tags manual -run TestRenderEmailPreview ./pkg/email -v
func TestRenderEmailPreview(t *testing.T) {
	siteName := "dwx Blog"
	now := time.Now().Unix()

	newComment := &model.Comment{
		PageID:     "/post/hello-world.html",
		Site:       "default",
		Nick:       "Alice",
		Email:      "alice@example.com",
		Content:    "这是一封本地预览的新评论测试。\n\n看看渲染出来的样式是否和评论区一致：低饱和背景、2px 圆角、紫灰点缀。",
		CreateTime: now,
	}

	reply := &model.Comment{
		PageID:     "/post/hello-world.html",
		Site:       "default",
		Nick:       "Bob",
		Email:      "bob@example.com",
		Content:    "感谢分享，我也遇到了同样的问题，已经解决了！",
		CreateTime: now,
	}
	parent := &model.Comment{
		PageID:     "/post/hello-world.html",
		Site:       "default",
		Nick:       "Alice",
		Email:      "alice@example.com",
		Content:    "请问有人知道怎么配置 SMTP 吗？",
		CreateTime: now - 3600,
	}

	dir := filepath.Join("..", "..", "tmp_email_preview")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("create preview dir: %v", err)
	}
	newPath := filepath.Join(dir, "new_comment.html")
	replyPath := filepath.Join(dir, "reply.html")

	newHTML, err := renderNewCommentHTML(siteName, newComment)
	if err != nil {
		t.Fatalf("renderNewCommentHTML: %v", err)
	}
	if err := os.WriteFile(newPath, []byte(newHTML), 0644); err != nil {
		t.Fatalf("write new_comment.html: %v", err)
	}

	replyHTML, err := renderReplyHTML(siteName, reply, parent)
	if err != nil {
		t.Fatalf("renderReplyHTML: %v", err)
	}
	if err := os.WriteFile(replyPath, []byte(replyHTML), 0644); err != nil {
		t.Fatalf("write reply.html: %v", err)
	}

	fmt.Println("新评论预览:", newPath)
	fmt.Println("回复预览 :", replyPath)

	openBrowser(newPath)
	openBrowser(replyPath)
}

func openBrowser(path string) {
	var cmd string
	var args []string
	switch runtime.GOOS {
	case "windows":
		cmd = "cmd"
		args = []string{"/c", "start", "", path}
	case "darwin":
		cmd = "open"
		args = []string{path}
	default:
		cmd = "xdg-open"
		args = []string{path}
	}
	if err := exec.Command(cmd, args...).Start(); err != nil {
		fmt.Printf("打开浏览器失败 %s: %v\n", path, err)
	}
}
