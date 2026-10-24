// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

// Package webhook 实现 HTTP/Webhook 推送通道（注册类型名 "http"）。
package webhook

import (
	"time"

	"gorm.io/gorm"

	"iot-gateway/model/po"
	"iot-gateway/push"
	"iot-gateway/push/codec"
	"iot-gateway/push/messagechannel"
)

func init() {
	push.RegisterType(push.ChannelTypeMeta{
		Type:             "http",
		Label:            "HTTP / Webhook",
		MessageBased:     true,
		Formats:          push.SupportedFormats(),
		DefaultFormat:    push.FormatNativeJSON,
		SupportsPreview:  true,
		SupportsTestSend: true,
	}, func(db *gorm.DB, ch *po.PushChannel) (push.Channel, error) {
		return newHTTPChannel(db, ch)
	})
}

func newHTTPChannel(db *gorm.DB, ch *po.PushChannel) (push.Channel, error) {
	cfg, err := parseConfig(ch.ConfigJSON)
	if err != nil {
		return nil, err
	}
	enc, err := codec.New(cfg.Format, cfg.Mapping)
	if err != nil {
		return nil, err
	}
	channelType := ch.Type
	if channelType == "" {
		channelType = "http"
	}
	return messagechannel.New(messagechannel.Config{
		Tag:               "http",
		ID:                ch.ID,
		Name:              ch.Name,
		Type:              channelType,
		Target:            cfg.URL,
		Detail:            cfg.Method + " " + cfg.ContentType,
		ConfigSig:         ch.ID + "|" + ch.Name + "|" + channelType + "|" + ch.ConfigJSON,
		DB:                db,
		Encoder:           enc,
		Transport:         newTransport(cfg),
		Workers:           cfg.Workers,
		Timeout:           time.Duration(cfg.timeoutSeconds()) * time.Second,
		SpoolEnabled:      cfg.spoolEnabled(),
		SpoolCap:          cfg.SpoolMaxBatches,
		ReconnectInterval: 5 * time.Second,
	}), nil
}
