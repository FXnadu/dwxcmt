package utils

import (
	"encoding/json"
	"net/http"
)

// Response 统一响应格式 {code, msg, data}
type Response struct {
	Code int         `json:"code"`
	Msg  string      `json:"msg"`
	Data interface{} `json:"data"`
}

// OK 成功响应
func OK(w http.ResponseWriter, data interface{}) {
	WriteJSON(w, http.StatusOK, Response{Code: CodeOK, Msg: "success", Data: data})
}

// OKMsg 成功响应（自定义提示语）
func OKMsg(w http.ResponseWriter, msg string, data interface{}) {
	WriteJSON(w, http.StatusOK, Response{Code: CodeOK, Msg: msg, Data: data})
}

// Fail 业务失败响应（HTTP 200 + 业务错误码）
func Fail(w http.ResponseWriter, code int) {
	FailMsg(w, code, Message(code))
}

// FailMsg 业务失败响应（自定义提示语）
func FailMsg(w http.ResponseWriter, code int, msg string) {
	WriteJSON(w, http.StatusOK, Response{Code: code, Msg: msg, Data: map[string]interface{}{}})
}

// Error 系统错误响应（HTTP 状态码 + 业务错误码）
func Error(w http.ResponseWriter, status, code int) {
	WriteJSON(w, status, Response{Code: code, Msg: Message(code), Data: map[string]interface{}{}})
}

// WriteJSON 写 JSON 响应
func WriteJSON(w http.ResponseWriter, status int, resp Response) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(resp)
}
