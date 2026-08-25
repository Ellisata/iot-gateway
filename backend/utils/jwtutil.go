package utils

import (
	"errors"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"iot-gateway/configFile"
)

// GenerateToken 生成 JWT 令牌
func GenerateToken(userId string) (string, error) {
	cfg, err := configFile.GetConfig()
	if err != nil || cfg == nil {
		return "", errors.New("config not loaded")
	}

	claims := jwt.RegisteredClaims{
		ID:        userId,
		Issuer:    cfg.Jwt.Issuer,
		IssuedAt:  jwt.NewNumericDate(time.Now()),
		ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Duration(cfg.Jwt.ExpireHour) * time.Hour)),
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString([]byte(cfg.Jwt.Secret))
}

// ValidateToken 验证令牌
func ValidateToken(tokenStr string) (*jwt.RegisteredClaims, error) {
	cfg, err := configFile.GetConfig()
	if err != nil || cfg == nil {
		return nil, errors.New("config not loaded")
	}

	token, err := jwt.ParseWithClaims(tokenStr, &jwt.RegisteredClaims{}, func(t *jwt.Token) (interface{}, error) {
		return []byte(cfg.Jwt.Secret), nil
	})
	if err != nil {
		return nil, err
	}

	claims, ok := token.Claims.(*jwt.RegisteredClaims)
	if !ok || !token.Valid {
		return nil, errors.New("invalid token")
	}

	return claims, nil
}

// GetUserIdFromToken 从令牌中提取用户 ID
func GetUserIdFromToken(tokenStr string) (string, error) {
	claims, err := ValidateToken(tokenStr)
	if err != nil {
		return "", err
	}
	return claims.ID, nil
}
