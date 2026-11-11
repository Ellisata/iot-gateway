// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package collector

import (
	"time"

	"github.com/Ellisata/iot-gateway-sdk/driver"

	"iot-gateway/logger"
	"iot-gateway/mappingdsl"
	"iot-gateway/model/po"
)

// 点位工程量变换在采集侧执行一次，见 toRecords。
//
// 时序库通道（TDengine / InfluxDB）与实时快照、阈值报警、触发规则都直接读
// CollectedRecord.Value；把变换放在这里，所有下游看到的是同一个工程值，
// 不需要每个通道各配一遍。出站 mapping-json 的 transform 仍然存在，那是
// 展示层，作用在已归一化的值上。

// transformErrLogInterval 同一文案的变换失败日志最短间隔。
const transformErrLogInterval = time.Minute

// compiledTransform 任务构建期编译好的点位变换流水线。
type compiledTransform struct {
	ops         []mappingdsl.Transform
	forcesFloat bool
	hasDefault  bool
}

// compileAddressTransform 解析库里的 transform_json。空串返回零值（无变换）。
// 非法配置返回 error，由调用方记一次日志后忽略该点位（fail-open：不影响采集）。
func compileAddressTransform(raw string) (compiledTransform, error) {
	ts, err := mappingdsl.ParseNormalization(raw)
	if err != nil {
		return compiledTransform{}, err
	}
	if len(ts) == 0 {
		return compiledTransform{}, nil
	}
	return compiledTransform{
		ops:         ts,
		forcesFloat: mappingdsl.NormalizationForcesFloat(ts),
		hasDefault:  hasDefaultTransform(ts),
	}, nil
}

func hasDefaultTransform(ts []mappingdsl.Transform) bool {
	for _, t := range ts {
		if t.Op == mappingdsl.OpDefault {
			return true
		}
	}
	return false
}

// apply 执行变换并返回 (值, 生效 Kind)。
//
// Kind 的规则：scale、或保留小数位的 round 会让整数点位产生小数，此时把
// KindInt/KindUInt/KindFloat 统一提升为 KindFloat。不提升的话 InfluxDB 会把
// "25.3" 落到 value_str，而 TDengine 回落到浮点列 —— 同一条记录两个库两种列。
// Kind 只按配置决定，不随单个值是否变换成功而变，列选择因此稳定。
//
// 跳过变换：质量码不可用（值是占位串）、空值且没有 default。变换失败时保留
// 原始值并把 error 交给调用方限频记录，不改质量码 —— 采集到的数据不应因配置
// 错误而消失。
func (ct compiledTransform) apply(raw, kind string, quality int) (string, string, error) {
	outKind := kind
	if ct.forcesFloat && driver.IsNumericKind(kind) {
		outKind = driver.KindFloat
	}
	if len(ct.ops) == 0 || !driver.QualityUsable(quality) {
		return raw, outKind, nil
	}
	if raw == "" && !ct.hasDefault {
		return raw, outKind, nil
	}
	v, err := mappingdsl.ApplyTransforms(raw, ct.ops)
	if err != nil {
		return raw, outKind, err
	}
	return mappingdsl.ToString(v), outKind, nil
}

// transformErrState 单个点位的变换失败日志限流状态。
type transformErrState struct {
	text string
	at   time.Time
}

// shouldLogAddressTransformError 判断这次变换失败该不该记一行。
//
// 与 logReadError 的“原因变了才记”不同，这里带 1 分钟时间窗：同一个坏值会
// 随轮询反复出现，但一个配置错误的点位不该每分钟刷屏；文案变了（换了一种
// 失败原因）立即记。
func (t *gatewayTask) shouldLogAddressTransformError(addressID, text string, now time.Time) bool {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.transformErrLog == nil {
		t.transformErrLog = make(map[string]transformErrState)
	}
	prev, seen := t.transformErrLog[addressID]
	if seen && prev.text == text && now.Sub(prev.at) < transformErrLogInterval {
		return false
	}
	t.transformErrLog[addressID] = transformErrState{text: text, at: now}
	return true
}

func (t *gatewayTask) logAddressTransformError(dev *po.Device, addressID, label string, err error) {
	if !t.shouldLogAddressTransformError(addressID, err.Error(), time.Now()) {
		return
	}
	point := label
	if point == "" {
		point = addressID
	}
	logger.Warn("collector: transform point %s/%s failed: %v", dev.Name, point, err)
}
