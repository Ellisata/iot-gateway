// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package sqlite

import (
	"errors"
	"fmt"
	"os"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"iot-gateway/configFile"
	appLogger "iot-gateway/logger"
	"iot-gateway/model/po"
	"iot-gateway/utils"
)

// 名称与版本的兜底值：配置、环境变量、主机名三层都拿不到时使用。
// "dev" 与 release.sh 的 `git describe --tags ... || echo dev` 保持同一口径。
const (
	fallbackGatewayName    = "IoT Gateway"
	fallbackGatewayVersion = "dev"
)

// seedGatewayIdentity 启动时播种并同步网关自身身份行（单行表），见 migrations/012_iot_gateway.sql。
//
// 行不存在则插入；已存在则**只同步 firmware_version**：版本以配置（default.yaml 的
// gateway.version，可被 IOT_GATEWAY_VERSION 覆盖）为准，与库内值不一致就更新，
// 其余字段一概不动 —— 身份行其余部分是运维数据，让一次网关重启静默回滚运维的编辑
// 是不可接受的，与 pluginhost/provision.go 对 iot_protocol 行的取舍同源。
//
// 行建好之后各字段的归属：
//   - id：永不变，它是这台网关的唯一标识；
//   - name / description：归运维，通过「系统设置」页面改（GatewayService.UpdateGateway
//     直接写库）；配置项 gateway.name 只参与首启播种，之后再改不生效是预期行为；
//   - firmware_version：**归配置**，每次启动比对并刷新。它是二进制自身的属性，
//     交给页面改只会让库里的值与实际运行的二进制对不上，而广播出去的 firmwareVersion
//     正是管理平台据以判断「这台跑的是哪个版本」的字段（见 discovery/announcer.go）。
//     与 name 的取舍刻意相反：名字是部署方给这台机器起的，配置值只是一次性的初始值；
//   - port / status：只在建行时写入。
//
// 另外**每次启动都打一条 INFO**，这样运维才有机会发现「数据库被克隆导致
// 两台网关共用同一 ID」——这是唯一能察觉该情形的途径。
//
// 与 seedDefaultAdmin 的另一处差别是调用方不 Fatal：admin 播种失败等于没有账号可登录，
// 属致命；而身份行缺失只影响「系统设置」页（读取时返回「网关身份不存在」），
// 为它让整个网关起不来（Fatal 会 os.Exit(1)，运维只能删库恢复）不划算。
func seedGatewayIdentity(db *gorm.DB, cfg *configFile.Config) error {
	return seedGatewayIdentityWith(db, cfg, os.Hostname)
}

// seedGatewayIdentityWith 是 seedGatewayIdentity 的可测形态：主机名查找以参数注入，
// 否则「回退到主机名」这一支在测试里依赖跑测机器，必然不稳定。
func seedGatewayIdentityWith(db *gorm.DB, cfg *configFile.Config, hostname func() (string, error)) error {
	version := resolveGatewayVersion(cfg)

	var existing po.IotGateway
	err := db.First(&existing).Error
	switch {
	case err == nil:
		return syncGatewayVersion(db, existing, version)
	case !errors.Is(err, gorm.ErrRecordNotFound):
		return fmt.Errorf("load iot_gateway: %w", err)
	}

	now := time.Now().Format("2006-01-02 15:04:05")
	gw := &po.IotGateway{
		ID: utils.GenerateUUID(),
		// 名称只在建行时才解析：行已存在时它归运维（走系统设置页），
		// 没必要为此每次启动都查一遍主机名。
		Name:            resolveGatewayName(cfg, hostname),
		Port:            gatewayPort(cfg),
		FirmwareVersion: version,
		Status:          1,
		// 必须显式赋值：po.IotGateway 的时间字段没有 default 标签，GORM 会
		// 把 Go 零值 '' 显式写进去，DDL 里的 DEFAULT (datetime(...)) 不会生效。
		CreatedAt: now,
		UpdatedAt: now,
	}

	// OnConflict DoNothing：两个进程同时对同一个库跑 InitDB 时，后到的那个会撞上
	// name 的唯一索引。跳过即可 —— 对方的行才是该保留的那行。
	res := db.Clauses(clause.OnConflict{DoNothing: true}).Create(gw)
	if res.Error != nil {
		return fmt.Errorf("create gateway identity: %w", res.Error)
	}
	if res.RowsAffected == 0 {
		appLogger.Info("gateway identity already created by another process, keeping it")
		return nil
	}

	appLogger.Info("gateway identity created: id=%s name=%s version=%s", gw.ID, gw.Name, gw.FirmwareVersion)
	return nil
}

// syncGatewayVersion 让库内版本跟上配置：一致时只打日志，不一致才写库。
//
// 只更新 firmware_version / updated_at 两列，id / name / description / port / status
// 原样不动（同 GatewayService.UpdateGateway 只认自己那几列的道理）。
// 名称不做同样的同步 —— 它可以通过「系统设置」页改，与配置不一致是正常的。
//
// 写失败不致命：版本旧一点只影响广播出去的那一个字段，不该因此让网关起不来
// （调用方本就不 Fatal，这里再包一层错误只为让日志指得出是哪个库操作失败）。
func syncGatewayVersion(db *gorm.DB, existing po.IotGateway, version string) error {
	if existing.FirmwareVersion == version {
		appLogger.Info("gateway identity loaded: id=%s name=%s version=%s",
			existing.ID, existing.Name, existing.FirmwareVersion)
		return nil
	}

	now := time.Now().Format("2006-01-02 15:04:05")
	if err := db.Model(&po.IotGateway{}).Where("id = ?", existing.ID).Updates(map[string]any{
		"firmware_version": version,
		"updated_at":       now,
	}).Error; err != nil {
		return fmt.Errorf("update gateway version: %w", err)
	}

	appLogger.Info("gateway version updated: id=%s name=%s version=%s -> %s",
		existing.ID, existing.Name, existing.FirmwareVersion, version)
	return nil
}

// resolveGatewayName 解析网关名称，优先级由高到低：
//  1. 环境变量 IOT_GATEWAY_NAME：容器 / 批量部署逐台注入名称的首选方式
//  2. 配置项 gateway.name：部署方显式固定的名称
//  3. 主机名：未配置时的默认值，多台网关同时接入平台时天然可区分
//  4. fallbackGatewayName：主机名也取不到时的兜底
//
// 与 resolveAdminPassword 同一套优先级。第 3 步必须**同时判空**：
// os.Hostname 可能返回 ("", nil)，只判 err == nil 会把空名字写进库
// ——空串满足 name 的 NOT NULL 约束，整条回退链会被静默废掉。
func resolveGatewayName(cfg *configFile.Config, hostname func() (string, error)) string {
	if s := os.Getenv(configFile.EnvGatewayName); s != "" {
		return s
	}
	if cfg != nil && cfg.Gateway.Name != "" {
		return cfg.Gateway.Name
	}
	if hostname != nil {
		if h, err := hostname(); err == nil && h != "" {
			return h
		}
	}
	return fallbackGatewayName
}

// resolveGatewayVersion 解析网关软件版本，优先级由高到低：
// 环境变量 IOT_GATEWAY_VERSION > 配置项 gateway.version > fallbackGatewayVersion。
func resolveGatewayVersion(cfg *configFile.Config) string {
	if s := os.Getenv(configFile.EnvGatewayVersion); s != "" {
		return s
	}
	if cfg != nil && cfg.Gateway.Version != "" {
		return cfg.Gateway.Version
	}
	return fallbackGatewayVersion
}

// gatewayPort 取网关自己的监听端口（与 main.go 绑定的是同一个值）。
// 手搓 Config 的场景（负载测试的 harness）没有 server 段，此时为 0。
func gatewayPort(cfg *configFile.Config) int {
	if cfg == nil {
		return 0
	}
	return cfg.Server.Port
}
