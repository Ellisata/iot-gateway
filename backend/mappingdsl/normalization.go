// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package mappingdsl

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// 点位工程量变换的算子白名单与校验。
//
// 与出站映射共用 Transform/ApplyTransforms，但**不是同一组算子**：点位侧只做
// 原始值 → 工程值的归一（缩放、取整、去空白、空值回落），不做 hexDecode /
// base64Decode / bitfield / regex —— 那类算子会改变值的类别，采集侧已经定下了
// Kind，改类别会让时序库列、报警比较与 OPC UA 节点类型同时失配。
//
// 三个调用方必须共用这份规则：服务保存时校验、采集任务构建时编译、OPC UA
// 建树时判断是否需要把整数节点提升为浮点。各自复制一份必然会漂移。

// MaxNormalizationTransforms 单个点位变换流水线的最大步数。
const MaxNormalizationTransforms = 16

// NormalizationOps 点位工程量变换允许的算子。
var NormalizationOps = []string{OpScale, OpRound, OpTrim, OpDefault}

// IsNormalizationOp 判断算子是否属于点位工程量变换白名单。
func IsNormalizationOp(op string) bool {
	op = strings.TrimSpace(op)
	for _, allowed := range NormalizationOps {
		if allowed == op {
			return true
		}
	}
	return false
}

// ValidateNormalization 校验一条点位变换流水线。
//
// default 必须放在第一位：采集侧遇到空值且流水线没有 default 时会直接跳过；
// 有 default 时按顺序执行。若 default 排在 scale/round 之后，空值会先被
// scale/round 判成"不是数字"，永远轮不到 default —— 那等于配置了一个永不生效的回落。
func ValidateNormalization(ts []Transform) error {
	if len(ts) > MaxNormalizationTransforms {
		return fmt.Errorf("变换流水线最多 %d 步", MaxNormalizationTransforms)
	}
	for i := range ts {
		op := strings.TrimSpace(ts[i].Op)
		if !IsNormalizationOp(op) {
			return fmt.Errorf("点位变换不支持算子 %q", ts[i].Op)
		}
		if err := ts[i].Validate(); err != nil {
			return fmt.Errorf("第 %d 个变换: %w", i+1, err)
		}
		if op == OpDefault && i != 0 {
			return errors.New("default 变换必须放在第一位")
		}
	}
	return nil
}

// ParseNormalization 解析并校验库里的 transform_json。
// 空串与 "null" 都表示未配置。
func ParseNormalization(raw string) ([]Transform, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "null" {
		return nil, nil
	}
	var ts []Transform
	if err := json.Unmarshal([]byte(raw), &ts); err != nil {
		return nil, fmt.Errorf("变换配置解析失败: %w", err)
	}
	if err := ValidateNormalization(ts); err != nil {
		return nil, err
	}
	return ts, nil
}

// NormalizationForcesFloat 报告流水线是否可能把整数结果变成小数。
//
// scale 一定会经过浮点运算；round 只有在保留小数位时才可能产生小数点。命中时
// 采集侧把数值型记录的 Kind 提升为 float，否则 InfluxDB 会把 "25.3" 落到
// value_str，而 TDengine 回落到浮点列 —— 两个时序库对同一条记录给出不同列。
func NormalizationForcesFloat(ts []Transform) bool {
	for _, t := range ts {
		switch strings.TrimSpace(t.Op) {
		case OpScale:
			return true
		case OpRound:
			if t.Digits > 0 {
				return true
			}
		}
	}
	return false
}
