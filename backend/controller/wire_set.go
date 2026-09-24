// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

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
	NewAlarmWebhookController,
	NewAlarmWebhookFormController,
	NewLogFileController,
	NewOpenApiSecretController,
	NewOpenApiController,
)
