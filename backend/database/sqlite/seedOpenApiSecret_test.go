// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package sqlite

import (
	"testing"

	"gorm.io/gorm"

	"iot-gateway/model/po"
)

// soleSecret 取唯一一行密钥，顺带断言只有一行。
func soleSecret(t *testing.T, db *gorm.DB) po.OpenApiSecret {
	t.Helper()

	var records []po.OpenApiSecret
	if err := db.Find(&records).Error; err != nil {
		t.Fatalf("find open api secrets: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("expected exactly 1 secret, got %d", len(records))
	}
	return records[0]
}

func TestSeedDefaultOpenApiSecret(t *testing.T) {
	db := newTestDB(t)

	if err := seedDefaultOpenApiSecret(db); err != nil {
		t.Fatalf("seed: %v", err)
	}

	record := soleSecret(t, db)
	if record.Name != defaultOpenApiSecretName {
		t.Errorf("name = %q, want %q", record.Name, defaultOpenApiSecretName)
	}
	// 32 字节 hex 编码即 64 个字符。
	if len(record.Key) != 64 {
		t.Errorf("key length = %d, want 64", len(record.Key))
	}
	if record.ID == "" || record.CreatedAt == "" || record.UpdatedAt == "" {
		t.Errorf("id/created_at/updated_at 不能为空, got %+v", record)
	}
}

func TestSeedDefaultOpenApiSecretIsIdempotent(t *testing.T) {
	db := newTestDB(t)

	if err := seedDefaultOpenApiSecret(db); err != nil {
		t.Fatalf("seed: %v", err)
	}
	first := soleSecret(t, db)

	if err := seedDefaultOpenApiSecret(db); err != nil {
		t.Fatalf("seed again: %v", err)
	}
	// 密钥是已分发给管理平台的凭据，重启必须原样保留 —— 换掉它等于让对方静默失效。
	if again := soleSecret(t, db); again.Key != first.Key {
		t.Errorf("重复播种换了密钥: %q -> %q", first.Key, again.Key)
	}
}

// TestSeedDefaultOpenApiSecretAddsDefaultWhenOthersExist 守住播种判据是**按名称**
// 而不是"表非空即返回"。
//
// 这一条与 seedDefaultAdmin 刻意相反：admin 是"表里有账号就不播种"，因为多一个
// 账号无意义；而默认密钥是有名字、有身份的，运维先手工建了几条密钥之后再重启，
// 恰恰是这条 default 最需要被补上的时候。
func TestSeedDefaultOpenApiSecretAddsDefaultWhenOthersExist(t *testing.T) {
	db := newTestDB(t)

	existing := &po.OpenApiSecret{Name: "生产密钥", Key: "x", CreatedAt: "2026-01-01 00:00:00"}
	if err := db.Create(existing).Error; err != nil {
		t.Fatalf("create existing secret: %v", err)
	}

	if err := seedDefaultOpenApiSecret(db); err != nil {
		t.Fatalf("seed: %v", err)
	}

	var count int64
	if err := db.Model(&po.OpenApiSecret{}).Where("name = ?", defaultOpenApiSecretName).
		Count(&count).Error; err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 1 {
		t.Fatalf("表里已有其它密钥时仍应播种 default, got %d", count)
	}
}
