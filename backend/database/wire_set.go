// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package database

import (
	"github.com/google/wire"

	"iot-gateway/database/sqlite"
)

// DatabaseProviderSet 数据库层依赖提供者集合
var DatabaseProviderSet = wire.NewSet(
	sqlite.InitDB,
)
