// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package service

import "github.com/google/wire"

// ServiceProviderSet 业务逻辑层依赖提供者集合
var ServiceProviderSet = wire.NewSet(
	NewUserService,
	NewDeviceService,
	NewDeviceAddressService,
	NewCollectionService,
	NewRealtimeService,
	NewProtocolService,
	NewPushChannelService,
	NewPushChannelFormService,
	NewMqttSubscriptionService,
	NewAlarmService,
	NewAlarmWebhookService,
	NewAlarmWebhookFormService,
	NewLogFileService,
	NewOpenApiSecretService,
	NewOpenApiService,
	NewGatewayService,
	NewPluginService,
	NewDeviceScanService,
	NewSystemConfigService,
	NewDashboardService,
)
