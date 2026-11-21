// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package tsdb

import (
	"fmt"
	"log/slog"
)

// Logger 组件日志接口。
//
// 组件不引用宿主的全局 logger，宿主按需注入适配器；不注入时使用 NopLogger。
type Logger interface {
	Debugf(format string, args ...any)
	Infof(format string, args ...any)
	Warnf(format string, args ...any)
	Errorf(format string, args ...any)
}

type nopLogger struct{}

func (nopLogger) Debugf(string, ...any) {}
func (nopLogger) Infof(string, ...any)  {}
func (nopLogger) Warnf(string, ...any)  {}
func (nopLogger) Errorf(string, ...any) {}

// NopLogger 返回一个静默日志器。
func NopLogger() Logger { return nopLogger{} }

type slogLogger struct{ l *slog.Logger }

func (s slogLogger) Debugf(format string, args ...any) { s.l.Debug(fmt.Sprintf(format, args...)) }
func (s slogLogger) Infof(format string, args ...any)  { s.l.Info(fmt.Sprintf(format, args...)) }
func (s slogLogger) Warnf(format string, args ...any)  { s.l.Warn(fmt.Sprintf(format, args...)) }
func (s slogLogger) Errorf(format string, args ...any) { s.l.Error(fmt.Sprintf(format, args...)) }

// SlogLogger 把标准库 slog 适配为组件 Logger。
func SlogLogger(l *slog.Logger) Logger {
	if l == nil {
		return NopLogger()
	}
	return slogLogger{l: l}
}

func orNop(l Logger) Logger {
	if l == nil {
		return NopLogger()
	}
	return l
}
