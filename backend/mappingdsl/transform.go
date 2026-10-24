// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package mappingdsl

import (
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// 值变换算子。白名单形式：配置文件是外部输入，不允许执行任意表达式。
const (
	OpScale        = "scale"
	OpRound        = "round"
	OpTrim         = "trim"
	OpDefault      = "default"
	OpHexDecode    = "hexDecode"
	OpBase64Decode = "base64Decode"
	OpBitfield     = "bitfield"
	OpRegex        = "regex"
)

// Transform 单个值变换。
//
//	{"op":"scale","factor":0.1}          值 × 0.1（factor 缺省 1，offset 缺省 0）
//	{"op":"round","digits":2}            保留 2 位小数
//	{"op":"trim"}                        去首尾空白
//	{"op":"default","value":"0"}         空值时回落
//	{"op":"hexDecode"}                   十六进制文本 → 字节串
//	{"op":"base64Decode"}                Base64 文本 → 字节串
//	{"op":"bitfield","offset":4,"length":8} 取整数 bit[4,12)
//	{"op":"regex","pattern":"(\\d+)","group":1} 正则提取
type Transform struct {
	Op      string   `json:"op"`
	Factor  *float64 `json:"factor,omitempty"`
	Offset  *float64 `json:"offset,omitempty"`
	Digits  int      `json:"digits,omitempty"`
	Value   string   `json:"value,omitempty"`
	Pattern string   `json:"pattern,omitempty"`
	Group   int      `json:"group,omitempty"`
	Length  int      `json:"length,omitempty"`

	re *regexp.Regexp
}

// Validate 校验算子与参数，并缓存编译后的正则。
func (t *Transform) Validate() error {
	op := strings.TrimSpace(t.Op)
	switch op {
	case OpScale, OpRound, OpTrim, OpDefault, OpHexDecode, OpBase64Decode:
		if op == OpDefault && t.Value == "" {
			return errors.New("default 变换需要填写回落值")
		}
		if op == OpRound && (t.Digits < 0 || t.Digits > 12) {
			return fmt.Errorf("round 的小数位数 %d 超出范围 [0,12]", t.Digits)
		}
	case OpBitfield:
		if t.Offset == nil {
			return errors.New("bitfield 变换需要填写 offset")
		}
		off := int(*t.Offset)
		if off < 0 || off > 63 {
			return fmt.Errorf("bitfield 的 offset %d 超出范围 [0,63]", off)
		}
		if t.Length < 1 || off+t.Length > 64 {
			return fmt.Errorf("bitfield 的 length %d 超出范围 [1,%d]", t.Length, 64-off)
		}
	case OpRegex:
		if strings.TrimSpace(t.Pattern) == "" {
			return errors.New("regex 变换需要填写 pattern")
		}
		re, err := regexp.Compile(t.Pattern)
		if err != nil {
			return fmt.Errorf("regex 变换的 pattern 无效: %w", err)
		}
		if t.Group < 0 || t.Group > re.NumSubexp() {
			return fmt.Errorf("regex 的分组号 %d 超出范围 [0,%d]", t.Group, re.NumSubexp())
		}
		t.re = re
	default:
		return fmt.Errorf("未知的变换 %q", t.Op)
	}
	return nil
}

// ApplyTransforms 依次执行变换流水线。
func ApplyTransforms(v any, ts []Transform) (any, error) {
	for i := range ts {
		nv, err := ts[i].apply(v)
		if err != nil {
			return nil, fmt.Errorf("第 %d 个变换(%s): %w", i+1, ts[i].Op, err)
		}
		v = nv
	}
	return v, nil
}

func (t *Transform) apply(v any) (any, error) {
	switch strings.TrimSpace(t.Op) {
	case OpTrim:
		return strings.TrimSpace(ToString(v)), nil
	case OpDefault:
		if strings.TrimSpace(ToString(v)) == "" {
			return t.Value, nil
		}
		return v, nil
	case OpScale:
		f, ok := ToFloat64(v)
		if !ok {
			return nil, fmt.Errorf("值 %q 不是数字", ToString(v))
		}
		factor, offset := 1.0, 0.0
		if t.Factor != nil {
			factor = *t.Factor
		}
		if t.Offset != nil {
			offset = *t.Offset
		}
		return strconv.FormatFloat(f*factor+offset, 'f', -1, 64), nil
	case OpRound:
		f, ok := ToFloat64(v)
		if !ok {
			return nil, fmt.Errorf("值 %q 不是数字", ToString(v))
		}
		return strconv.FormatFloat(f, 'f', t.Digits, 64), nil
	case OpHexDecode:
		s := strings.TrimSpace(ToString(v))
		s = strings.TrimPrefix(strings.TrimPrefix(s, "0x"), "0X")
		b, err := hex.DecodeString(s)
		if err != nil {
			return nil, fmt.Errorf("十六进制解码失败: %w", err)
		}
		return string(b), nil
	case OpBase64Decode:
		s := strings.TrimSpace(ToString(v))
		b, err := base64.StdEncoding.DecodeString(s)
		if err != nil {
			return nil, fmt.Errorf("Base64 解码失败: %w", err)
		}
		return string(b), nil
	case OpBitfield:
		n, ok := ToInt(ToString(v))
		if !ok {
			return nil, fmt.Errorf("值 %q 不是整数", ToString(v))
		}
		off := int(*t.Offset)
		// length=64 时 1<<64 在 uint64 上回绕为 0，减 1 正好得到全 1 掩码。
		mask := (uint64(1) << uint(t.Length)) - 1
		return strconv.FormatUint((uint64(n)>>uint(off))&mask, 10), nil
	case OpRegex:
		re := t.re
		if re == nil {
			compiled, err := regexp.Compile(t.Pattern)
			if err != nil {
				return nil, fmt.Errorf("regex 的 pattern 无效: %w", err)
			}
			re = compiled
		}
		m := re.FindStringSubmatch(ToString(v))
		if m == nil {
			return nil, fmt.Errorf("正则 %q 未匹配值 %q", t.Pattern, ToString(v))
		}
		if t.Group >= len(m) {
			return nil, fmt.Errorf("正则分组 %d 不存在", t.Group)
		}
		return m[t.Group], nil
	default:
		return nil, fmt.Errorf("未知的变换 %q", t.Op)
	}
}
