// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package mapping

import (
	"context"
	"encoding/json"
	"fmt"

	"iot-gateway/parser"
)

func init() {
	parser.Register(parser.TypeBuiltin, factory{})
}

// factory 是声明式映射在 parser 契约下的工厂。
//
// Validate 走保存路径的严格校验（主字段必填）；New 走加载路径的宽松校验，
// 保证新增必填项不会让历史订阅整体失效。
type factory struct{}

func (factory) Validate(raw json.RawMessage) error {
	_, err := ParseStrict(raw)
	return err
}

func (factory) New(raw json.RawMessage) (parser.Parser, error) {
	rule, err := Parse(raw)
	if err != nil {
		return nil, err
	}
	return &ruleParser{rule: rule}, nil
}

type ruleParser struct {
	rule *Rule
}

func (p *ruleParser) Parse(ctx context.Context, batch []parser.Message) (parser.Result, error) {
	var out parser.Result
	for i, msg := range batch {
		if err := ctx.Err(); err != nil {
			return out, err
		}
		if len(msg.Payload) > parser.DefaultMaxPayloadBytes {
			out.Errors = append(out.Errors, parser.MessageError{
				Index: i, Topic: msg.Topic,
				Reason: fmt.Sprintf("报文 %d 字节，超过上限 %d", len(msg.Payload), parser.DefaultMaxPayloadBytes),
			})
			continue
		}

		res, err := p.rule.ConvertWithTopic(msg.Topic, msg.Payload)
		out.Skipped += res.Skipped
		if err != nil {
			out.Errors = append(out.Errors, parser.MessageError{Index: i, Topic: msg.Topic, Reason: err.Error()})
			continue
		}
		if len(res.Records) > parser.DefaultMaxRecordsPerMessage {
			out.Errors = append(out.Errors, parser.MessageError{
				Index: i, Topic: msg.Topic,
				Reason: fmt.Sprintf("产出 %d 条记录，超过上限 %d", len(res.Records), parser.DefaultMaxRecordsPerMessage),
			})
			continue
		}
		out.Records = append(out.Records, res.Records...)
	}
	return out, nil
}

func (p *ruleParser) Close() error { return nil }
