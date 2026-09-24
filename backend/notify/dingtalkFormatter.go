// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package notify

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"iot-gateway/alarm"
	"iot-gateway/model/po"
)

// dingTalkFormatter 钉钉群机器人报文格式化器。
//
// 报文格式：markdown（默认）或 text；加签开启时 timestamp/sign 拼在查询串上。
type dingTalkFormatter struct {
	webhookURL string
	secret     string
	msgType    string
	atAll      bool
	atList     []string
}

func newDingTalkFormatter(cfg *po.AlarmWebhook) (Formatter, error) {
	meta := typeMetas[TypeDingTalk]
	if err := validateCommon(cfg, meta); err != nil {
		return nil, err
	}
	msgType, err := resolveMsgType(cfg, meta)
	if err != nil {
		return nil, err
	}
	return &dingTalkFormatter{
		webhookURL: cfg.URL,
		secret:     cfg.Secret,
		msgType:    msgType,
		atAll:      cfg.AtAll == 1,
		atList:     parseAtList(cfg),
	}, nil
}

// Format 实现 Formatter。加签需在发送时刻生成，故每次调用都重算。
func (f *dingTalkFormatter) Format(ev alarm.Event) (*Request, error) {
	msg := buildMessage(ev, f.atAll, f.atList)

	target, err := url.Parse(f.webhookURL)
	if err != nil {
		return nil, fmt.Errorf("invalid url: %w", err)
	}
	if f.secret != "" {
		ts := time.Now().UnixMilli()
		q := target.Query()
		q.Set("timestamp", strconv.FormatInt(ts, 10))
		// url.Values.Encode 会把 base64 里的 + = 正确转义为 %2B %3D，平台要求如此
		q.Set("sign", dingTalkSign(f.secret, ts))
		target.RawQuery = q.Encode()
	}

	// @ 状态字段：钉钉要求正文里出现字面量 @手机号 的同时，这里也带上号码
	at := map[string]any{
		"atMobiles": nonNilStrings(f.atList),
		"atUserIds": []string{},
		"isAtAll":   f.atAll,
	}

	var payload map[string]any
	if f.msgType == MsgTypeText {
		payload = map[string]any{
			"msgtype": "text",
			"text": map[string]string{
				"content": dingTalkMentions(msg.PlainText(), msg.AtAll, msg.AtList),
			},
			"at": at,
		}
	} else {
		payload = map[string]any{
			"msgtype": "markdown",
			"markdown": map[string]string{
				"title": msg.Title,
				"text":  dingTalkMentions("### "+msg.Title+"\n\n"+msg.MarkdownLines("- "), msg.AtAll, msg.AtList),
			},
			"at": at,
		}
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	return &Request{
		Method:   http.MethodPost,
		URL:      target.String(),
		Headers:  jsonHeaders(),
		Body:     body,
		Platform: TypeDingTalk,
	}, nil
}

// dingTalkMentions 在正文末尾追加 @ 文案。
//
// 钉钉的 @ 是「双写」要求：正文里必须有字面量 @手机号，at.atMobiles 也必须带上，
// 缺任何一个都不会真正提醒到人。
func dingTalkMentions(text string, atAll bool, atList []string) string {
	mentions := make([]string, 0, len(atList)+1)
	for _, m := range atList {
		mentions = append(mentions, "@"+m)
	}
	if atAll {
		mentions = append(mentions, "@所有人")
	}
	if len(mentions) == 0 {
		return text
	}
	return text + "\n\n" + strings.Join(mentions, " ")
}
