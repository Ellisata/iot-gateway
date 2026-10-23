// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package script_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	_ "iot-gateway/parser/script"

	"iot-gateway/parser"
)

func newParser(t *testing.T, script string, timeoutMs, maxRecords int) parser.Parser {
	t.Helper()
	cfg, err := json.Marshal(map[string]any{
		"script":               script,
		"timeoutMs":            timeoutMs,
		"maxRecordsPerMessage": maxRecords,
	})
	if err != nil {
		t.Fatalf("marshal config: %v", err)
	}
	p, err := parser.New(parser.Config{Type: parser.TypeScript, Config: cfg})
	if err != nil {
		t.Fatalf("parser.New: %v", err)
	}
	t.Cleanup(func() { _ = p.Close() })
	return p
}

func msg(payload string) parser.Message {
	return parser.Message{
		Topic:      "factory/line1/telemetry",
		Payload:    []byte(payload),
		ReceivedAt: time.Unix(1700000000, 0),
		QoS:        1,
	}
}

func TestScriptParseBasic(t *testing.T) {
	p := newParser(t, `
function parse(msg, ctx) {
  var d = ctx.json();
  ctx.emit({
    deviceId: d.id,
    deviceAddressId: d.tag,
    value: d.v,
    kind: d.kind
  });
}`, 50, 10)

	res, err := p.Parse(context.Background(), []parser.Message{
		msg(`{"id":"d1","tag":"a1","v":1.5,"kind":"float"}`),
		msg(`{"id":"d1","tag":"a2","v":"x","kind":"string"}`),
	})
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(res.Errors) != 0 {
		t.Fatalf("unexpected errors: %+v", res.Errors)
	}
	if len(res.Records) != 2 {
		t.Fatalf("records = %d, want 2", len(res.Records))
	}
	if res.Records[0].Value != "1.5" || res.Records[0].Kind != "float" {
		t.Errorf("first record = %+v", res.Records[0])
	}
	if res.Records[1].Value != "x" || res.Records[1].Kind != "string" {
		t.Errorf("second record = %+v", res.Records[1])
	}
}

func TestScriptStateAcrossMessages(t *testing.T) {
	p := newParser(t, `
function parse(msg, ctx) {
  var n = (ctx.state.n || 0) + 1;
  ctx.state.n = n;
  ctx.emit({deviceId:"d1", deviceAddressId:"count", value:String(n), kind:"int"});
}`, 50, 10)

	for i := 0; i < 3; i++ {
		res, err := p.Parse(context.Background(), []parser.Message{msg(`{}`)})
		if err != nil {
			t.Fatalf("Parse #%d: %v", i, err)
		}
		if len(res.Records) != 1 || res.Records[0].Value != string(rune('1'+i)) {
			t.Fatalf("Parse #%d records = %+v", i, res.Records)
		}
	}
}

func TestScriptTimeoutRecoversNextMessage(t *testing.T) {
	p := newParser(t, `
function parse(msg, ctx) {
  if (msg.payload === "boom") { while (true) {} }
  ctx.emit({deviceId:"d1", deviceAddressId:"a1", value:msg.payload, kind:"string"});
}`, 20, 10)

	res, err := p.Parse(context.Background(), []parser.Message{msg("boom")})
	if err != nil {
		t.Fatalf("Parse(boom) returned error: %v", err)
	}
	if len(res.Errors) != 1 || !strings.Contains(res.Errors[0].Reason, "超时") {
		t.Fatalf("timeout errors = %+v", res.Errors)
	}

	res, err = p.Parse(context.Background(), []parser.Message{msg("ok")})
	if err != nil {
		t.Fatalf("Parse(ok): %v", err)
	}
	if len(res.Errors) != 0 || len(res.Records) != 1 || res.Records[0].Value != "ok" {
		t.Fatalf("after timeout records=%+v errors=%+v", res.Records, res.Errors)
	}
}

func TestScriptPreservesLargeInteger(t *testing.T) {
	p := newParser(t, `
function parse(msg, ctx) {
  ctx.emit({
    deviceId: ctx.get("id"),
    deviceAddressId: "a1",
    value: ctx.get("v"),
    kind: "int"
  });
}`, 50, 10)

	res, err := p.Parse(context.Background(), []parser.Message{
		msg(`{"id":510637000000000001,"v":9223372036854775807}`),
	})
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(res.Errors) != 0 || len(res.Records) != 1 {
		t.Fatalf("records=%+v errors=%+v", res.Records, res.Errors)
	}
	if res.Records[0].DeviceID != "510637000000000001" {
		t.Errorf("deviceId = %q, want exact large integer", res.Records[0].DeviceID)
	}
	if res.Records[0].Value != "9223372036854775807" {
		t.Errorf("value = %q, want exact int64", res.Records[0].Value)
	}
}

func TestScriptRejectsUnknownField(t *testing.T) {
	p := newParser(t, `
function parse(msg, ctx) {
  ctx.emit({deviceId:"d1", deviceAddressId:"a1", value:"1", typoField:"x"});
}`, 50, 10)

	res, err := p.Parse(context.Background(), []parser.Message{msg(`{}`)})
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(res.Errors) != 1 || !strings.Contains(res.Errors[0].Reason, "未知的目标字段") {
		t.Fatalf("errors = %+v", res.Errors)
	}
}

func TestScriptEnforcesRecordLimit(t *testing.T) {
	p := newParser(t, `
function parse(msg, ctx) {
  for (var i = 0; i < 5; i++) {
    ctx.emit({deviceId:"d1", deviceAddressId:"a"+i, value:"1", kind:"int"});
  }
}`, 50, 2)

	res, err := p.Parse(context.Background(), []parser.Message{msg(`{}`)})
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(res.Errors) != 1 || !strings.Contains(res.Errors[0].Reason, "超过 2 条") {
		t.Fatalf("errors = %+v", res.Errors)
	}
}

func TestScriptValidateRejectsBadConfig(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want string
	}{
		{"empty", `{}`, "脚本不能为空"},
		{"syntax", `{"script":"function parse( {"}`, "编译失败"},
		{"noParse", `{"script":"var a = 1;"}`, "必须定义"},
		{"timeout", `{"script":"function parse(){}","timeoutMs":9999}`, "timeoutMs"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := parser.Validate(parser.Config{Type: parser.TypeScript, Config: json.RawMessage(tc.raw)})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Validate() error = %v, want %q", err, tc.want)
			}
		})
	}
}
