package database

import (
	"github.com/google/wire"

	"iot-gateway/database/sqlite"
)

// DatabaseProviderSet 数据库层依赖提供者集合
var DatabaseProviderSet = wire.NewSet(
	sqlite.InitDB,
)
