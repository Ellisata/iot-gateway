// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package sqlite

import "testing"

// TestMigration016MqttSubscriptionSchema 钉住 016 迁移在真实迁移路径上建出的订阅表。
//
// 生产只跑 migrations/*.sql（没有 AutoMigrate），迁移写错时其余单测仍会全绿，
// 这条用例是唯一能替生产挡住它的地方。
func TestMigration016MqttSubscriptionSchema(t *testing.T) {
	db := newTestDB(t)

	assertTableExists(t, db, "mqtt_subscription", []string{
		"id", "name", "description",
		"broker", "port", "client_id", "username", "password",
		"topic", "qos", "mapping_json", "sample_json",
		"sort", "status", "created_at", "updated_at",
	})
}
