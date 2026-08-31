package cip

import (
	"context"

	gilogging "github.com/iceisfun/goindustrial/logging"

	"iot-gateway/logger"
)

// giLevelBridge 把 goindustrial 日志桥接到项目 logger。
//
// goindustrial 的 do() 重试循环在传输错误时只通过内部 logger.Warn 输出真实原因
// （如 i/o timeout、响应解析失败），最终上抛的仅是 "operation failed after N retries"
// 兜底文案；默认 NopLogger 会把关键诊断信息全部丢弃。接入桥接后，库内 Warn/Error
// 直接进项目日志，可定位读标签失败的根因。
type giLevelBridge struct{}

// newGILevelBridge 创建 goindustrial → 项目 logger 的桥接器。
func newGILevelBridge() gilogging.Logger {
	return &giLevelBridge{}
}

func (b *giLevelBridge) Trace(ctx context.Context, format string, args ...any) {
	// 库的帧级 hexdump 跟踪日志，量太大，不转发
}

func (b *giLevelBridge) Debug(ctx context.Context, format string, args ...any) {
	logger.Debug("goindustrial: "+format, args...)
}

func (b *giLevelBridge) Info(ctx context.Context, format string, args ...any) {
	logger.Info("goindustrial: "+format, args...)
}

func (b *giLevelBridge) Warn(ctx context.Context, format string, args ...any) {
	logger.Warn("goindustrial: "+format, args...)
}

func (b *giLevelBridge) Error(ctx context.Context, format string, args ...any) {
	logger.Error("goindustrial: "+format, args...)
}

// WithFields 桥接器无结构化字段支持，返回自身（字段仅影响库内部展示格式）
func (b *giLevelBridge) WithFields(fields map[string]any) gilogging.Logger {
	return b
}

func (b *giLevelBridge) GetLevel() gilogging.Level {
	return gilogging.LevelDebug
}

func (b *giLevelBridge) SetLevel(level gilogging.Level) {
	// 级别由项目日志配置统一管理，不接受库内调整
}

// 编译期保证桥接器实现 goindustrial Logger 接口
var _ gilogging.Logger = (*giLevelBridge)(nil)
