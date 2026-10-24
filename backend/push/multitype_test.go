// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package push

import (
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"iot-gateway/model/po"
)

func TestMultipleInstancesOfSameType(t *testing.T) {
	RegisterType(ChannelTypeMeta{
		Type:         "multistub",
		Label:        "Multi Stub",
		MessageBased: true,
	}, func(_ *gorm.DB, ch *po.PushChannel) (Channel, error) {
		return &stubChannel{id: ch.ID}, nil
	})

	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("get sql.DB: %v", err)
	}
	sqlDB.SetMaxOpenConns(1)
	if err := db.AutoMigrate(&po.PushChannel{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	rows := []po.PushChannel{
		{ID: "c1", Type: "multistub", Name: "实例一", ConfigJSON: `{}`, Status: 1},
		{ID: "c2", Type: "multistub", Name: "实例二", ConfigJSON: `{}`, Status: 1},
	}
	if err := db.Create(&rows).Error; err != nil {
		t.Fatalf("insert: %v", err)
	}

	e := NewEngine(db, nil)
	e.refresh()
	e.mu.RLock()
	defer e.mu.RUnlock()
	if len(e.channels) != 2 {
		t.Fatalf("channels = %d, want 2", len(e.channels))
	}
}

func TestTemporaryChannelAndTypeMetas(t *testing.T) {
	RegisterType(ChannelTypeMeta{Type: "tmpstub", Label: "Temp Stub"}, func(_ *gorm.DB, ch *po.PushChannel) (Channel, error) {
		return &stubChannel{id: ch.ID}, nil
	})

	ch, err := NewTemporaryChannel(nil, "tmpstub", "x", `{}`)
	if err != nil {
		t.Fatalf("NewTemporaryChannel: %v", err)
	}
	if ch.ChannelID() != "" {
		t.Errorf("temporary channel ID = %q, want empty", ch.ChannelID())
	}

	var found bool
	for _, meta := range SupportedChannelTypeMetas() {
		if meta.Type == "tmpstub" {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("tmpstub missing from SupportedChannelTypeMetas")
	}

	if _, ok := RegisteredTypeMeta("TMPSTUB"); !ok {
		t.Fatal("RegisteredTypeMeta should be case-insensitive")
	}
}
