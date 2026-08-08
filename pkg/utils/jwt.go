package utils

import (
	"errors"
	"strconv"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// JWT 签名/校验器（HS256）
type JWT struct {
	Secret []byte
	TTL    int64 // 秒
}

// Claims JWT 载荷
type Claims struct {
	Username string `json:"username"`
	Purpose  string `json:"purpose,omitempty"` // ""=常规登录凭证；"2fa"=二次验证预授权凭证（不可直接访问接口）
	jwt.RegisteredClaims
}

// NewJWT 构造 JWT 工具
func NewJWT(secret string, ttl int64) *JWT {
	return &JWT{Secret: []byte(secret), TTL: ttl}
}

// Sign 签发常规登录 token（Purpose 为空），返回 (token, ttl)
func (j *JWT) Sign(adminID int64, username string) (string, error) {
	return j.SignWithTTL(adminID, username, "", j.TTL)
}

// SignWithTTL 签发指定 Purpose 与有效期的 token
func (j *JWT) SignWithTTL(adminID int64, username, purpose string, ttl int64) (string, error) {
	now := time.Now()
	claims := Claims{
		Username: username,
		Purpose:  purpose,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   strconv.FormatInt(adminID, 10),
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(time.Duration(ttl) * time.Second)),
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString(j.Secret)
}

// Parse 校验并解析 token，返回 Claims
func (j *JWT) Parse(tokenStr string) (*Claims, error) {
	token, err := jwt.ParseWithClaims(tokenStr, &Claims{}, func(t *jwt.Token) (interface{}, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, errors.New("unexpected signing method")
		}
		return j.Secret, nil
	})
	if err != nil {
		return nil, err
	}
	claims, ok := token.Claims.(*Claims)
	if !ok || !token.Valid {
		return nil, errors.New("invalid token")
	}
	return claims, nil
}
