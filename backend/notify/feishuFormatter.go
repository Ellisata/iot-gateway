// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package notify

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"iot-gateway/alarm"
	"iot-gateway/model/po"
)

// feishuFormatter 飞书群机器人报文格式化器。
//
// 报文格式：text（默认）或 card（交互式卡片，带标题色块）。
// 加签与钉钉不同：timestamp 为**秒**，sign/timestamp 放在 **body 顶层**。
type feishuFormatter struct {
	webhookURL string
	secret     string
	msgType    string
	atAll      bool
	atList     []string
}

func newFeishuFormatter(cfg *po.AlarmWebhook) (Formatter, error) {
	meta := typeMetas[TypeFeishu]
	if err := validateCommon(cfg, meta); err != nil {
		return nil, err
	}
	msgType, err := resolveMsgType(cfg, meta)
	if err != nil {
		return nil, err
	}
	return &feishuFormatter{
		webhookURL: cfg.URL,
		secret:     cfg.Secret,
		msgType:    msgType,
		atAll:      cfg.AtAll == 1,
		atList:     parseAtList(cfg),
	}, nil
}

// Format 实现 Formatter。加签需在发送时刻生成，故每次调用都重算。
func (f *feishuFormatter) Format(ev alarm.Event) (*Request, error) {
	msg := buildMessage(ev, f.atAll, f.atList)

	payload := make(map[string]any, 5)
	if f.secret != "" {
		// 飞书要求 timestamp 为秒级；放 URL 查询串上验签不通过，必须在 body 里
		ts := time.Now().Unix()
		payload["timestamp"] = strconv.FormatInt(ts, 10)
		payload["sign"] = feishuSign(f.secret, ts)
	}

	if f.msgType == MsgTypeCard {
		payload["msg_type"] = "interactive"
		payload["card"] = f.buildCard(msg)
	} else {
		payload["msg_type"] = "text"
		payload["content"] = map[string]string{
			"text": feishuMentions(msg.PlainText(), msg.AtAll, msg.AtList),
		}
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	return &Request{
		Method:   http.MethodPost,
		URL:      f.webhookURL,
		Headers:  jsonHeaders(),
		Body:     body,
		Platform: TypeFeishu,
	}, nil
}

// buildCard 组装交互式卡片：标题色块按报警/恢复区分，正文用 lark_md。
func (f *feishuFormatter) buildCard(msg Message) map[string]any {
	template := "red"
	if msg.IsRecover {
		template = "green"
	}

	content := "**" + msg.Title + "**\n" + msg.MarkdownLines("")
	if tags := feishuMentionTags(msg.AtAll, msg.AtList); tags != "" {
		content += "\n" + tags
	}

	return map[string]any{
		"config": map[string]any{"wide_screen_mode": true},
		"header": map[string]any{
			"title":    map[string]string{"tag": "plain_text", "content": msg.Title},
			"template": template,
		},
		"elements": []any{
			map[string]any{
				"tag":  "div",
				"text": map[string]string{"tag": "lark_md", "content": content},
			},
			map[string]any{"tag": "hr"},
			map[string]any{
				"tag":      "note",
				"elements": []any{map[string]string{"tag": "plain_text", "content": "iot-gateway 报警通知"}},
			},
		},
	}
}

// feishuMentionTags 渲染飞书 @ 标签，无 @ 时返回空串。
// 人员在飞书侧是 open_id，全量 @ 用固定的 user_id="all"。
func feishuMentionTags(atAll bool, atList []string) string {
	parts := make([]string, 0, len(atList)+1)
	for _, id := range atList {
		parts = append(parts, `<at user_id="`+id+`"></at>`)
	}
	if atAll {
		parts = append(parts, `<at user_id="all"></at>`)
	}
	return strings.Join(parts, " ")
}

// feishuMentions 在正文末尾追加飞书 @ 标签。
func feishuMentions(text string, atAll bool, atList []string) string {
	if tags := feishuMentionTags(atAll, atList); tags != "" {
		return text + "\n" + tags
	}
	return text
}
