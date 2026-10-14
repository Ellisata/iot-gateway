// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package utils

import (
	"errors"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"iot-gateway/configFile"
)

// UserClaims JWT 声明：标准声明之外携带用户名。
//
// 用户名放进签名载荷，下游（如修改密码）便可直接按用户名定位当前用户，
// 不必再依赖客户端提交的用户名 —— 客户端提交的值可被伪造，签名中的不能。
type UserClaims struct {
	Username string `json:"username"`
	jwt.RegisteredClaims
}

// GenerateToken 生成 JWT 令牌，同时返回令牌过期时刻
func GenerateToken(userId, username string) (string, time.Time, error) {
	cfg, err := configFile.GetConfig()
	if err != nil || cfg == nil {
		return "", time.Time{}, errors.New("config not loaded")
	}

	now := time.Now()
	expiresAt := now.Add(time.Duration(cfg.Jwt.ExpireHour) * time.Hour)

	claims := UserClaims{
		Username: username,
		RegisteredClaims: jwt.RegisteredClaims{
			ID:        userId,
			Issuer:    cfg.Jwt.Issuer,
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(expiresAt),
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := token.SignedString([]byte(cfg.Jwt.Secret))
	if err != nil {
		return "", time.Time{}, err
	}
	return signed, expiresAt, nil
}

// ValidateToken 验证令牌
func ValidateToken(tokenStr string) (*UserClaims, error) {
	cfg, err := configFile.GetConfig()
	if err != nil || cfg == nil {
		return nil, errors.New("config not loaded")
	}

	token, err := jwt.ParseWithClaims(tokenStr, &UserClaims{}, func(t *jwt.Token) (interface{}, error) {
		return []byte(cfg.Jwt.Secret), nil
	})
	if err != nil {
		return nil, err
	}

	claims, ok := token.Claims.(*UserClaims)
	if !ok || !token.Valid {
		return nil, errors.New("invalid token")
	}

	return claims, nil
}

// GetUserFromToken 从令牌中提取用户 ID 与用户名
func GetUserFromToken(tokenStr string) (userId, username string, err error) {
	claims, err := ValidateToken(tokenStr)
	if err != nil {
		return "", "", err
	}
	return claims.ID, claims.Username, nil
}
