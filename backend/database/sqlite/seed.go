// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package sqlite

import (
	"crypto/rand"
	"fmt"
	"os"
	"time"

	"gorm.io/gorm"

	"iot-gateway/configFile"
	appLogger "iot-gateway/logger"
	"iot-gateway/model/po"
	"iot-gateway/utils"
)

// defaultAdminUsername 初始账号用户名。
const defaultAdminUsername = "admin"

// seedDefaultAdmin 在用户表为空时播种初始 admin 账号。
//
// 播种刻意放在启动期而不是迁移脚本里，原因有两条：
//
//  1. 迁移由 schema_migrations 按文件名去重、一个库只跑一次，口令一旦落库就
//     改不动了 —— 想换只能再追加一条迁移，而已部署库里那条旧记录还在。
//  2. 更要紧的是，迁移脚本里写死的 bcrypt 哈希会随源码进仓库、随 go:embed 进
//     二进制，等于全部部署共享同一个可离线爆破的已知口令。放在这里则每次部署
//     单独算哈希（salt 不同），口令既可配置、也可随机。
//
// 幂等性由「表非空即返回」保证，与 schema_migrations 无关：运维已经建过账号
// （或改过密码）的库不会被覆盖，删掉数据文件重来也能照常重建。
func seedDefaultAdmin(db *gorm.DB, cfg *configFile.Config) error {
	var count int64
	if err := db.Model(&po.User{}).Count(&count).Error; err != nil {
		return fmt.Errorf("count sys_user: %w", err)
	}
	if count > 0 {
		return nil
	}

	plain, generated, err := resolveAdminPassword(cfg)
	if err != nil {
		return err
	}

	hashed, err := utils.HashPassword(plain)
	if err != nil {
		return fmt.Errorf("hash admin password: %w", err)
	}

	now := time.Now().Format("2006-01-02 15:04:05")
	admin := &po.User{
		ID:        utils.GenerateUUID(),
		Username:  defaultAdminUsername,
		Password:  hashed,
		Status:    1,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := db.Create(admin).Error; err != nil {
		return fmt.Errorf("create default admin: %w", err)
	}

	if generated {
		// 库里存的是哈希，日志是这条随机口令的唯一出口 —— 且只此一次。
		appLogger.Warn("created default admin account, password (shown once, change it after login): %s", plain)
	} else {
		appLogger.Info("created default admin account, password taken from configuration")
	}
	return nil
}

// resolveAdminPassword 解析初始 admin 口令，优先级由高到低：
//  1. 环境变量 IOT_GATEWAY_ADMIN_PASSWORD：容器 / 自动化部署注入口令的首选方式
//  2. 配置项 auth.defaultAdminPassword：部署方显式固定的口令
//  3. 随机生成：未配置时的安全默认值，每次部署都不同
//
// 与 JWT 密钥的解析（configFile.resolveJwtSecret）保持同一套优先级；区别在于
// 随机生成的那一支必须打到日志告知运维 —— 密钥不需要人知道，口令需要。
// 随机口令在这里生成而不是在配置加载期，也正是因为那时 logger 还没初始化。
func resolveAdminPassword(cfg *configFile.Config) (plain string, generated bool, err error) {
	if s := os.Getenv(configFile.EnvAdminPassword); s != "" {
		return s, false, nil
	}
	if cfg != nil && cfg.Auth.DefaultAdminPassword != "" {
		return cfg.Auth.DefaultAdminPassword, false, nil
	}

	// crypto/rand.Text 返回 26 位 RFC 4648 base32 随机串（至少 128 位熵），
	// 字母表不含 0/1，人工从日志抄进登录框时没有 O/0、I/1 这类歧义。
	return rand.Text(), true, nil
}
