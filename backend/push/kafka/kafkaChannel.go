// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package kafka

import (
	"strings"
	"time"

	"gorm.io/gorm"

	"iot-gateway/model/po"
	"iot-gateway/push"
	"iot-gateway/push/codec"
	"iot-gateway/push/messagechannel"
)

func init() {
	push.RegisterType(push.ChannelTypeMeta{
		Type:             "kafka",
		Label:            "Kafka",
		MessageBased:     true,
		Formats:          push.SupportedFormats(),
		DefaultFormat:    push.FormatNativeJSON,
		SupportsPreview:  true,
		SupportsTestSend: true,
	}, func(db *gorm.DB, ch *po.PushChannel) (push.Channel, error) {
		return newKafkaChannel(db, ch)
	})
}

func newKafkaChannel(db *gorm.DB, ch *po.PushChannel) (push.Channel, error) {
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
		channelType = "kafka"
	}
	return messagechannel.New(messagechannel.Config{
		Tag:               "kafka",
		ID:                ch.ID,
		Name:              ch.Name,
		Type:              channelType,
		Target:            strings.Join(cfg.brokerAddrs, ","),
		Detail:            cfg.Topic,
		ConfigSig:         ch.ID + "|" + ch.Name + "|" + channelType + "|" + ch.ConfigJSON,
		DB:                db,
		Encoder:           enc,
		Transport:         newTransport(cfg),
		GroupIDs:          cfg.GroupIDs,
		Workers:           cfg.Workers,
		Timeout:           time.Duration(cfg.TimeoutSeconds) * time.Second,
		SpoolEnabled:      cfg.spoolEnabled(),
		SpoolCap:          cfg.SpoolMaxBatches,
		ReconnectInterval: 5 * time.Second,
	}), nil
}
