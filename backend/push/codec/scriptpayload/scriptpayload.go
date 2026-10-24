// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

// Package scriptpayload 用 goja 执行用户脚本，把一批采集记录编码为一条或多条载荷。
//
// 脚本是声明式出站映射表达不了时的逃生通道，但仍受与 parser/script 相同口径的
// 约束：只暴露 batch/ctx，不注入 require、文件、网络与 Go 对象；有执行超时、
// 脚本大小与产出上限。脚本必须定义 function encode(batch, ctx)。
//
// 与订阅解析脚本的关键区别：
//   - 推送通道的 Encode 会被多个 worker 并发调用，因此每次调用都新建
//     goja.Runtime，只复用预编译的 Program；
//   - 出站批次可能从断网缓存重放，脚本必须对同一批次产出相同结果，故不提供
//     ctx.state，也不建议依赖 Date.now()/Math.random()；
//   - 返回值就是消息载荷：string 为一条，string[] 为多条，null/undefined 为空。
package scriptpayload

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	"github.com/dop251/goja"

	"iot-gateway/collector"
	"iot-gateway/logger"
	"iot-gateway/push"
)

const (
	defaultTimeoutMs   = 50
	maxTimeoutMs       = 500
	maxScriptBytes     = 256 * 1024
	defaultMaxMessages = 200
	hardMaxMessages    = 1000
	maxLogLines        = 100

	// 单条载荷上限与单批总产出上限：脚本在网关进程内执行，不能让一条错误脚本
	// 把内存或断网缓存（push_outbox）写爆。
	maxMessageBytes = 1 << 20
	maxTotalBytes   = 16 << 20
)

// config 脚本编码器配置，存于通道 config_json 的 mapping 字段：
//
//	{"format":"script","mapping":{"script":"function encode(batch, ctx){...}"}}
type config struct {
	Script      string `json:"script"`
	TimeoutMs   int    `json:"timeoutMs,omitempty"`
	MaxMessages int    `json:"maxMessages,omitempty"`
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
	maxMessages := c.MaxMessages
	if maxMessages == 0 {
		maxMessages = defaultMaxMessages
	}
	if maxMessages < 1 || maxMessages > hardMaxMessages {
		return 0, 0, fmt.Errorf("maxMessages %d 超出范围 [1,%d]", maxMessages, hardMaxMessages)
	}
	return time.Duration(timeoutMs) * time.Millisecond, maxMessages, nil
}

// Encoder 实现 push.Encoder。
//
// Program 编译一次后并发只读；每次 Encode 新建的 Runtime 互不共享，因此没有
// 跨批次状态，也能安全地被通道的多个 worker 同时调用。
type Encoder struct {
	program     *goja.Program
	timeout     time.Duration
	maxMessages int
	logs        atomic.Int64
}

// New 解析配置并预编译脚本；语法错误、顶层执行错误与缺少 encode 函数都在
// 保存/热加载阶段拒绝，不进入运行期。
func New(raw json.RawMessage) (*Encoder, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil, errors.New(`script 格式需要 mapping 配置，例如 {"script":"function encode(batch, ctx){...}"}`)
	}
	var cfg config
	if err := json.Unmarshal(trimmed, &cfg); err != nil {
		return nil, fmt.Errorf("脚本配置 JSON 解析失败: %w", err)
	}
	timeout, maxMessages, err := cfg.normalize()
	if err != nil {
		return nil, err
	}
	prog, err := goja.Compile("push-encode.js", cfg.Script, false)
	if err != nil {
		return nil, fmt.Errorf("脚本编译失败: %s", cleanJSError(err))
	}
	if err := validateProgram(prog); err != nil {
		return nil, err
	}
	return &Encoder{program: prog, timeout: timeout, maxMessages: maxMessages}, nil
}

// validateProgram 在独立的 Runtime 上跑一遍顶层代码并确认 encode 是函数。
func validateProgram(prog *goja.Program) error {
	vm := goja.New()
	if _, err := vm.RunProgram(prog); err != nil {
		return fmt.Errorf("脚本执行失败: %s", cleanJSError(err))
	}
	if _, err := encodeFunc(vm); err != nil {
		return err
	}
	return nil
}

func encodeFunc(vm *goja.Runtime) (goja.Callable, error) {
	fnVal := vm.Get("encode")
	if fnVal == nil || goja.IsUndefined(fnVal) || goja.IsNull(fnVal) {
		return nil, errors.New("脚本必须定义 function encode(batch, ctx)")
	}
	fn, ok := goja.AssertFunction(fnVal)
	if !ok {
		return nil, errors.New("脚本的 encode 不是函数")
	}
	return fn, nil
}

// Encode 实现 push.Encoder。
func (e *Encoder) Encode(b push.PushBatch) ([]push.EncodedMessage, error) {
	// 每次调用使用全新的 Runtime：goja 非并发安全，且断网补发会重放同一批次，
	// 不允许上一次调用留下的全局变量影响结果。
	vm := goja.New()
	if _, err := vm.RunProgram(e.program); err != nil {
		return nil, fmt.Errorf("脚本执行失败: %s", cleanJSError(err))
	}
	fn, err := encodeFunc(vm)
	if err != nil {
		return nil, err
	}

	batchObj := vm.NewObject()
	_ = batchObj.Set("deviceId", b.DeviceID)
	_ = batchObj.Set("collectedAt", b.CollectedAt)
	records := make([]map[string]any, len(b.Records))
	for i := range b.Records {
		records[i] = recordMap(b.Records[i])
	}
	_ = batchObj.Set("records", vm.ToValue(records))

	ctxObj := vm.NewObject()
	_ = ctxObj.Set("log", func(call goja.FunctionCall) goja.Value {
		n := e.logs.Add(1)
		if n <= maxLogLines {
			args := make([]string, 0, len(call.Arguments))
			for _, a := range call.Arguments {
				args = append(args, a.String())
			}
			logger.Info("push script: %s", strings.Join(args, " "))
			if n == maxLogLines {
				logger.Warn("push script: 日志超过 %d 条，后续输出已抑制", maxLogLines)
			}
		}
		return goja.Undefined()
	})

	interrupted := make(chan struct{})
	timer := time.AfterFunc(e.timeout, func() {
		vm.Interrupt(errTimeout)
		close(interrupted)
	})
	ret, callErr := fn(goja.Undefined(), batchObj, ctxObj)
	if !timer.Stop() {
		<-interrupted
	}
	vm.ClearInterrupt()

	if callErr != nil {
		var ie *goja.InterruptedError
		if errors.As(callErr, &ie) && ie.Value() == errTimeout {
			return nil, fmt.Errorf("脚本执行超时（%s）", e.timeout)
		}
		return nil, fmt.Errorf("脚本执行失败: %s", cleanJSError(callErr))
	}
	return e.messages(ret)
}

// recordMap 暴露给脚本的记录字段与 CollectedRecord 的 JSON 口径一致。
// value 保持字符串，脚本要做算术需自行 Number(...)，避免经 float64 丢精度。
func recordMap(r collector.CollectedRecord) map[string]any {
	return map[string]any{
		"deviceId":           r.DeviceID,
		"deviceName":         r.DeviceName,
		"deviceAddressId":    r.DeviceAddressID,
		"deviceAddressName":  r.DeviceAddressName,
		"deviceAddressLabel": r.DeviceAddressLabel,
		"value":              r.Value,
		"dataType":           r.DataType,
		"kind":               r.Kind,
		"protocol":           r.Protocol,
		"quality":            int64(r.Quality),
		"collectedAt":        r.CollectedAt,
	}
}

func (e *Encoder) messages(ret goja.Value) ([]push.EncodedMessage, error) {
	if ret == nil || goja.IsNull(ret) || goja.IsUndefined(ret) {
		return nil, nil
	}
	switch v := ret.Export().(type) {
	case string:
		if err := checkMessageSize(v); err != nil {
			return nil, err
		}
		return []push.EncodedMessage{{Payload: []byte(v)}}, nil
	case []any:
		if len(v) > e.maxMessages {
			return nil, fmt.Errorf("单批产出 %d 条消息，超过上限 %d", len(v), e.maxMessages)
		}
		total := 0
		out := make([]push.EncodedMessage, 0, len(v))
		for i, item := range v {
			s, ok := item.(string)
			if !ok {
				return nil, fmt.Errorf("encode 返回的第 %d 项不是字符串", i+1)
			}
			if err := checkMessageSize(s); err != nil {
				return nil, fmt.Errorf("第 %d 条消息: %w", i+1, err)
			}
			total += len(s)
			if total > maxTotalBytes {
				return nil, fmt.Errorf("单批产出超过 %d 字节上限", maxTotalBytes)
			}
			out = append(out, push.EncodedMessage{Payload: []byte(s)})
		}
		return out, nil
	default:
		return nil, errors.New("encode 必须返回 string、string[] 或 null/undefined")
	}
}

func checkMessageSize(s string) error {
	if len(s) > maxMessageBytes {
		return fmt.Errorf("单条载荷 %d 字节，超过上限 %d", len(s), maxMessageBytes)
	}
	return nil
}

var errTimeout = errors.New("script timeout")

// cleanJSError 去掉 goja 异常里的超长堆栈，只保留首行与位置信息。
// 与 parser/script 保持同一口径。
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
