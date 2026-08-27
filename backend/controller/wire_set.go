package controller

import "github.com/google/wire"

// ControllerProviderSet 控制器层依赖提供者集合
var ControllerProviderSet = wire.NewSet(
	NewUserController,
	NewDeviceController,
	NewDeviceAddressController,
	NewCollectionController,
	NewProtocolController,
	NewPushChannelController,
	NewPushChannelFormController,
	NewPushController,
	NewAlarmController,
	NewLogFileController,
	NewOpenApiSecretController,
	NewOpenApiController,
)
