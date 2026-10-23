// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

// Package script 用 goja 在进程内执行用户自定义解析脚本。
//
// 脚本是「表单 + 声明式映射表达不了」时的逃生通道，但仍受严格约束：
//   - 只暴露 msg/ctx 两个普通对象，不注入 require、文件、网络、Go 对象；
//   - 每条报文有执行超时，超时后重建 VM（旧状态不可信）；
//   - 产出字段走 CollectedRecord 白名单，单报文记录数有上限；
//   - 实例与订阅一一对应，由单个 worker 顺序调用，不要求并发安全。
//
// 脚本必须定义 function parse(msg, ctx)。
package script

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	"github.com/dop251/goja"

	"iot-gateway/collector"
	"iot-gateway/logger"
	"iot-gateway/parser"
)

const (
	defaultTimeoutMs  = 50
	maxTimeoutMs      = 500
	defaultMaxRecords = 200
	maxScriptBytes    = 256 * 1024
	maxLogLines       = 100
)

func init() {
	parser.Register(parser.TypeScript, factory{})
}

// config 脚本解析器配置，存于 mqtt_subscription.parser_config。
type config struct {
	Script               string `json:"script"`
	TimeoutMs            int    `json:"timeoutMs,omitempty"`
	MaxRecordsPerMessage int    `json:"maxRecordsPerMessage,omitempty"`
}

func (c *config) normalize() (time.Duration, int, error) {
	if strings.TrimSpace(c.Script) == "" {
		return 0, 0, errors.New("脚本不能为空")
	}
	if len(c.Script) > maxScriptBytes {
		return 0, 0, fmt.Errorf("脚本超过 %d 字节上限", maxScriptBytes)
	}
	timeoutMs := c.TimeoutMs
	if timeoutMs == 0 {
		timeoutMs = defaultTimeoutMs
	}
	if timeoutMs < 1 || timeoutMs > maxTimeoutMs {
		return 0, 0, fmt.Errorf("timeoutMs %d 超出范围 [1,%d]", timeoutMs, maxTimeoutMs)
	}
	maxRecords := c.MaxRecordsPerMessage
	if maxRecords == 0 {
		maxRecords = defaultMaxRecords
	}
	if maxRecords < 1 || maxRecords > parser.HardMaxRecordsPerMessage {
		return 0, 0, fmt.Errorf("maxRecordsPerMessage %d 超出范围 [1,%d]", maxRecords, parser.HardMaxRecordsPerMessage)
	}
	return time.Duration(timeoutMs) * time.Millisecond, maxRecords, nil
}

type factory struct{}

func (factory) Validate(raw json.RawMessage) error {
	p, err := factory{}.New(raw)
	if err != nil {
		return err
	}
	return p.Close()
}

func (factory) New(raw json.RawMessage) (parser.Parser, error) {
	var cfg config
	dec := json.NewDecoder(bytes.NewReader(raw))
	if err := dec.Decode(&cfg); err != nil {
		return nil, fmt.Errorf("脚本配置 JSON 解析失败: %w", err)
	}
	timeout, maxRecords, err := cfg.normalize()
	if err != nil {
		return nil, err
	}
	p := &scriptParser{
		script:     cfg.Script,
		timeout:    timeout,
		maxRecords: maxRecords,
	}
	if err := p.resetVM(); err != nil {
		return nil, err
	}
	return p, nil
}

type scriptParser struct {
	script     string
	timeout    time.Duration
	maxRecords int

	vm      *goja.Runtime
	parseFn goja.Callable
	// state 跨报文保留；VM 重建（超时）时一并清空。
	state *goja.Object
	logs  atomic.Int64
}

func (p *scriptParser) resetVM() error {
	vm := goja.New()
	if _, err := vm.RunString(p.script); err != nil {
		return fmt.Errorf("脚本编译失败: %s", cleanJSError(err))
	}
	fnVal := vm.Get("parse")
	if fnVal == nil || goja.IsUndefined(fnVal) || goja.IsNull(fnVal) {
		return errors.New("脚本必须定义 function parse(msg, ctx)")
	}
	fn, ok := goja.AssertFunction(fnVal)
	if !ok {
		return errors.New("脚本的 parse 不是函数")
	}
	p.vm = vm
	p.parseFn = fn
	p.state = nil
	return nil
}

func (p *scriptParser) Close() error {
	p.vm = nil
	p.parseFn = nil
	return nil
}

func (p *scriptParser) Parse(ctx context.Context, batch []parser.Message) (parser.Result, error) {
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

		records, err := p.runOne(msg)
		if err != nil {
			out.Errors = append(out.Errors, parser.MessageError{Index: i, Topic: msg.Topic, Reason: err.Error()})
			continue
		}
		out.Records = append(out.Records, records...)
	}
	return out, nil
}

func (p *scriptParser) runOne(msg parser.Message) ([]collector.CollectedRecord, error) {
	if p.vm == nil || p.parseFn == nil {
		if err := p.resetVM(); err != nil {
			return nil, err
		}
	}
	vm := p.vm

	emitted := make([]collector.CollectedRecord, 0, 4)
	var emitErr error

	var doc any
	var docErr error
	docLoaded := false
	loadDoc := func() (any, error) {
		if docLoaded {
			return doc, docErr
		}
		doc, docErr = decodeJSON(msg.Payload)
		docLoaded = true
		return doc, docErr
	}

	msgObj := vm.NewObject()
	_ = msgObj.Set("topic", msg.Topic)
	_ = msgObj.Set("payload", string(msg.Payload))
	_ = msgObj.Set("receivedAt", msg.ReceivedAt.UnixMilli())
	_ = msgObj.Set("receivedAtText", msg.ReceivedAt.Format("2006-01-02 15:04:05.000"))
	_ = msgObj.Set("qos", int64(msg.QoS))
	_ = msgObj.Set("retained", msg.Retained)

	ctxObj := vm.NewObject()
	_ = ctxObj.Set("emit", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) < 1 {
			panic(vm.NewTypeError("emit(record) 需要一个对象参数"))
		}
		if len(emitted) >= p.maxRecords {
			emitErr = fmt.Errorf("单条报文产出超过 %d 条记录", p.maxRecords)
			panic(vm.NewTypeError(emitErr.Error()))
		}
		exported := call.Arguments[0].Export()
		fields, ok := exported.(map[string]any)
		if !ok {
			emitErr = errors.New("emit 的参数必须是普通对象")
			panic(vm.NewTypeError(emitErr.Error()))
		}
		rec, err := parser.RecordFromMap(fields)
		if err != nil {
			emitErr = err
			panic(vm.NewTypeError(err.Error()))
		}
		emitted = append(emitted, rec)
		return goja.Undefined()
	})
	_ = ctxObj.Set("get", func(call goja.FunctionCall) goja.Value {
		path := call.Argument(0).String()
		d, err := loadDoc()
		if err != nil {
			panic(vm.NewTypeError(err.Error()))
		}
		v, ok := parser.LookupPath(d, path)
		if !ok {
			return goja.Null()
		}
		return vm.ToValue(parser.ScalarString(v))
	})
	_ = ctxObj.Set("json", func(goja.FunctionCall) goja.Value {
		d, err := loadDoc()
		if err != nil {
			panic(vm.NewTypeError(err.Error()))
		}
		return toGojaValue(vm, d)
	})
	_ = ctxObj.Set("log", func(call goja.FunctionCall) goja.Value {
		n := p.logs.Add(1)
		if n <= maxLogLines {
			args := make([]string, 0, len(call.Arguments))
			for _, a := range call.Arguments {
				args = append(args, a.String())
			}
			logger.Info("parser script: %s", strings.Join(args, " "))
			if n == maxLogLines {
				logger.Warn("parser script: 日志超过 %d 条，后续输出已抑制", maxLogLines)
			}
		}
		return goja.Undefined()
	})

	if p.state == nil {
		p.state = vm.NewObject()
	}
	_ = ctxObj.Set("state", p.state)

	// 超时：timer 负责中断 VM；若 timer 已触发，等它完成后重建 VM，
	// 避免迟到的中断落到下一条报文上。
	interrupted := make(chan struct{})
	timer := time.AfterFunc(p.timeout, func() {
		vm.Interrupt(errTimeout)
		close(interrupted)
	})

	_, callErr := p.parseFn(goja.Undefined(), msgObj, ctxObj)

	if !timer.Stop() {
		<-interrupted
		_ = p.resetVM()
	}
	vm.ClearInterrupt()

	if callErr != nil {
		if emitErr != nil {
			return nil, emitErr
		}
		var ie *goja.InterruptedError
		if errors.As(callErr, &ie) && ie.Value() == errTimeout {
			return nil, fmt.Errorf("脚本执行超时（%s）", p.timeout)
		}
		return nil, fmt.Errorf("脚本执行失败: %s", cleanJSError(callErr))
	}
	return emitted, nil
}

var errTimeout = errors.New("script timeout")

func decodeJSON(payload []byte) (any, error) {
	dec := json.NewDecoder(bytes.NewReader(payload))
	dec.UseNumber()
	var raw any
	if err := dec.Decode(&raw); err != nil {
		return nil, fmt.Errorf("JSON 解析失败: %w", err)
	}
	return raw, nil
}

// toGojaValue 把 UseNumber 解析出的文档转成 goja 值。
//
// 整数能放进 int64 就用 int64，放不下的保持字符串 —— 脚本里不经 float64
// 中转，避免设备 ID/纳秒时间戳失真。
func toGojaValue(vm *goja.Runtime, v any) goja.Value {
	switch t := v.(type) {
	case map[string]any:
		obj := vm.NewObject()
		for k, val := range t {
			_ = obj.Set(k, toGojaValue(vm, val))
		}
		return obj
	case []any:
		arr := make([]any, len(t))
		for i, val := range t {
			arr[i] = toGojaValue(vm, val)
		}
		return vm.ToValue(arr)
	case json.Number:
		if i, err := t.Int64(); err == nil {
			return vm.ToValue(i)
		}
		if f, err := t.Float64(); err == nil {
			return vm.ToValue(f)
		}
		return vm.ToValue(t.String())
	default:
		return vm.ToValue(v)
	}
}

// cleanJSError 去掉 goja 异常里的超长堆栈，只保留首行与位置信息。
func cleanJSError(err error) string {
	if err == nil {
		return ""
	}
	s := err.Error()
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	return strings.TrimSpace(s)
}
