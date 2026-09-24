// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package notify

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"iot-gateway/alarm"
	"iot-gateway/model/po"
)

// customFormatter 通用自定义 webhook 格式化器。
//
// 发送固定结构的 JSON 信封（字段稳定，作为对外契约），接收方可自行渲染。
// secret 非空时作为 Bearer Token 放在 Authorization 头。
type customFormatter struct {
	webhookURL string
	secret     string
}

func newCustomFormatter(cfg *po.AlarmWebhook) (Formatter, error) {
	meta := typeMetas[TypeCustom]
	if err := validateCommon(cfg, meta); err != nil {
		return nil, err
	}
	return &customFormatter{webhookURL: cfg.URL, secret: cfg.Secret}, nil
}

// Format 实现 Formatter。
func (f *customFormatter) Format(ev alarm.Event) (*Request, error) {
	// 信封是结构化行的超集：接收方既能直接展示 content，也能自行组装文案
	payload := map[string]any{
		"source":         "iot-gateway",
		"event":          "alarm",
		"alarmId":        ev.AlarmID,
		"alarmType":      ev.AlarmType,
		"status":         ev.Status,
		"targetType":     ev.TargetType,
		"targetId":       ev.TargetID,
		"targetName":     ev.TargetName,
		"level":          ev.Level,
		"content":        ev.Content,
		"firstOccurTime": ev.FirstOccurTime,
		"lastOccurTime":  ev.LastOccurTime,
		"clearTime":      ev.ClearTime,
		"notifyTime":     time.Now().Format("2006-01-02 15:04:05"),
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal custom payload: %w", err)
	}

	headers := jsonHeaders()
	if f.secret != "" {
		headers["Authorization"] = "Bearer " + f.secret
	}
	return &Request{
		Method:   http.MethodPost,
		URL:      f.webhookURL,
		Headers:  headers,
		Body:     body,
		Platform: TypeCustom,
	}, nil
}
