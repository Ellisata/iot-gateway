package service

import "github.com/google/wire"

// ServiceProviderSet 业务逻辑层依赖提供者集合
var ServiceProviderSet = wire.NewSet(
	NewUserService,
	NewDeviceService,
	NewDeviceAddressService,
	NewCollectionService,
	NewProtocolService,
	NewPushChannelService,
	NewPushChannelFormService,
	NewAlarmService,
	NewLogFileService,
	NewOpenApiSecretService,
	NewOpenApiService,
)
