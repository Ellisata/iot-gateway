// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package notify

import (
	"encoding/json"
	"net/http"
	"unicode/utf8"

	"iot-gateway/alarm"
	"iot-gateway/model/po"
)

const (
	// wecomMarkdownMaxBytes 企微群机器人 markdown 正文上限（UTF-8 字节）。
	wecomMarkdownMaxBytes = 4096
	// wecomTextMaxBytes 企微群机器人 text 正文上限（UTF-8 字节）。
	wecomTextMaxBytes = 2048
	// truncSuffix 截断标记，参与长度预算。
	truncSuffix = "\n…（内容过长已截断）"
)

// weComFormatter 企业微信群机器人报文格式化器。
//
// 不支持加签与 @（配置保存时已拒绝），报文格式 markdown（默认）或 text。
// 企微对正文长度有硬限制且超限会报错，故发送前按字节截断。
type weComFormatter struct {
	webhookURL string
	msgType    string
}

func newWeComFormatter(cfg *po.AlarmWebhook) (Formatter, error) {
	meta := typeMetas[TypeWeCom]
	if err := validateCommon(cfg, meta); err != nil {
		return nil, err
	}
	msgType, err := resolveMsgType(cfg, meta)
	if err != nil {
		return nil, err
	}
	return &weComFormatter{webhookURL: cfg.URL, msgType: msgType}, nil
}

// Format 实现 Formatter。
func (f *weComFormatter) Format(ev alarm.Event) (*Request, error) {
	// 企微群机器人不支持 @，at 参数恒为空
	msg := buildMessage(ev, false, nil)

	var payload map[string]any
	if f.msgType == MsgTypeText {
		payload = map[string]any{
			"msgtype": "text",
			"text": map[string]string{
				"content": truncateUTF8Bytes(msg.PlainText(), wecomTextMaxBytes),
			},
		}
	} else {
		payload = map[string]any{
			"msgtype": "markdown",
			"markdown": map[string]string{
				// 截断必须在 markdown 装饰完成之后做，否则会切断语法标记
				"content": truncateUTF8Bytes("## "+msg.Title+"\n"+msg.MarkdownLines("> "), wecomMarkdownMaxBytes),
			},
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
		Platform: TypeWeCom,
	}, nil
}

// truncateUTF8Bytes 把 s 截断到不超过 max 字节，并保证不切断多字节字符。
//
// 截断时预留截断标记的长度，因此返回值长度 <= max。s 超长时追加标记，
// 便于群里的值班人员知道内容不完整（而不是以为报警信息就这么点）。
func truncateUTF8Bytes(s string, max int) string {
	if len(s) <= max {
		return s
	}
	limit := max - len(truncSuffix)
	if limit < 0 {
		limit = 0
	}
	// 从 limit 向前回退到合法 rune 起始字节，避免切出半个汉字
	cut := limit
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + truncSuffix
}
