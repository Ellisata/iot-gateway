// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

// Package canonicaljson 提供跨系统配置同步所需的规范化 JSON 与密钥处理。
//
// 管理平台（iot-gateway-admin）与本网关各自实现同一套算法，靠
// testdata/vectors.json 这组共享测试向量保证两端逐字节一致。改动本包时
// 必须同步另一端的副本与向量文件，否则漂移检测会得出错误结论。
//
// 契约版本：sha256-canonical-json-v1
//   - 对象键按字节序排序（encoding/json 对 map 的默认行为）
//   - 数字统一为最简十进制写法（1.0 -> 1）
//   - 不转义 HTML 字符
//   - 密钥字段只保留「是否存在」，不参与内容比较
//   - hash = sha256(cfgType + "\n" + canonicalJSON)
package canonicaljson

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
)

// Mask 是密钥字段在读接口与期望态中的占位符。
// 写入时遇到该值表示「保留现有密钥」，由 KeepMasked 处理。
const Mask = "***"

// HashVersion 是当前规范化算法的版本号。两端不一致时禁止比较 hash。
const HashVersion = 1

// IsSecretKey 判断一个 JSON 键名是否承载密钥。
//
// 大小写不敏感。这里刻意不把 url / key / mapping 当密钥：URL 与 mapping 是
// 配置的语义部分，参与漂移比较对用户有价值。告警 Webhook 的 URL 本身含
// access_token，属于网关既有回显行为的已知风险，不在此处扩大处理。
func IsSecretKey(key string) bool {
	k := strings.ToLower(strings.TrimSpace(key))
	if k == "" {
		return false
	}
	switch k {
	case "password", "passwd", "pwd",
		"secret", "clientsecret", "client_secret",
		"privatekey", "private_key",
		"token", "accesstoken", "access_token", "refreshtoken", "refresh_token",
		"apikey", "api_key",
		"authorization", "auth", "sign", "signature",
		"signingkey", "signing_key",
		"saslpassword", "sasl_password",
		"opcuapassword", "opcua_password":
		return true
	}
	for _, suffix := range []string{"password", "passwd", "secret", "token", "apikey", "api_key"} {
		if strings.HasSuffix(k, suffix) {
			return true
		}
	}
	return false
}

// Decode 用 UseNumber 解析 JSON，避免大整数被 float64 截断。
func Decode(raw []byte) (any, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, err
	}
	return v, nil
}

// Canonicalize 输出紧凑、键有序、数字规范的 JSON。
func Canonicalize(v any) ([]byte, error) {
	normalized, err := normalize(v)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(normalized); err != nil {
		return nil, err
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}

// HashWithType 计算某一配置域的 contentHash。
//
// 先 MaskSecrets 再规范化：密钥的真实值不参与 hash，只区分「有/没有」。
// 这样管理平台即使拿不到网关密钥，也能与网关算出一致的 hash。
func HashWithType(cfgType string, v any) (string, error) {
	raw, err := Canonicalize(MaskSecrets(v))
	if err != nil {
		return "", err
	}
	h := sha256.Sum256(append([]byte(cfgType+"\n"), raw...))
	return hex.EncodeToString(h[:]), nil
}

// MaskSecrets 递归地把密钥字段替换为 Mask（非空）或空串（空值）。
// 返回新结构，不修改入参。
func MaskSecrets(v any) any {
	switch t := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, val := range t {
			if IsSecretKey(k) {
				out[k] = secretPresence(val)
				continue
			}
			out[k] = MaskSecrets(val)
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i := range t {
			out[i] = MaskSecrets(t[i])
		}
		return out
	default:
		return v
	}
}

// KeepMasked 把入参中的 Mask 占位符替换为 existing 同路径的值。
//
// 用于「导入 → 再次下发」：读接口拿不到密钥，回写时用 Mask 表示保留。
// existing 不存在时返回空串，绝不把字面量 *** 落库。
func KeepMasked(existing, incoming any) any {
	if s, ok := incoming.(string); ok && s == Mask {
		if existing == nil {
			return ""
		}
		return existing
	}
	switch inc := incoming.(type) {
	case map[string]any:
		ex, _ := existing.(map[string]any)
		out := make(map[string]any, len(inc))
		for k, val := range inc {
			var ev any
			if ex != nil {
				ev = ex[k]
			}
			out[k] = KeepMasked(ev, val)
		}
		return out
	case []any:
		ex, _ := existing.([]any)
		out := make([]any, len(inc))
		for i, val := range inc {
			var ev any
			if i < len(ex) {
				ev = ex[i]
			}
			out[i] = KeepMasked(ev, val)
		}
		return out
	default:
		return incoming
	}
}

// IsMasked 判断一个值是否是密钥占位符。
func IsMasked(v any) bool {
	s, ok := v.(string)
	return ok && s == Mask
}

func secretPresence(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		if t == "" {
			return ""
		}
		return Mask
	case bool:
		if !t {
			return ""
		}
		return Mask
	case json.Number:
		f, err := t.Float64()
		if err != nil || f == 0 {
			return ""
		}
		return Mask
	case float64:
		if t == 0 {
			return ""
		}
		return Mask
	case float32:
		if t == 0 {
			return ""
		}
		return Mask
	case int:
		return maskIfNonZero(t)
	case int64:
		return maskIfNonZero(t)
	case int32:
		return maskIfNonZero(int64(t))
	case uint:
		return maskIfNonZero(t)
	case uint64:
		return maskIfNonZero(t)
	case uint32:
		return maskIfNonZero(uint64(t))
	default:
		return Mask
	}
}

func maskIfNonZero[T comparable](v T) string {
	var zero T
	if v == zero {
		return ""
	}
	return Mask
}

func normalize(v any) (any, error) {
	switch t := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, val := range t {
			nv, err := normalize(val)
			if err != nil {
				return nil, err
			}
			out[k] = nv
		}
		return out, nil
	case []any:
		out := make([]any, len(t))
		for i := range t {
			nv, err := normalize(t[i])
			if err != nil {
				return nil, err
			}
			out[i] = nv
		}
		return out, nil
	case json.Number:
		return normalizeNumber(t.String())
	case float64:
		return normalizeFloat(t)
	case float32:
		return normalizeFloat(float64(t))
	case int:
		return json.Number(strconv.FormatInt(int64(t), 10)), nil
	case int8:
		return json.Number(strconv.FormatInt(int64(t), 10)), nil
	case int16:
		return json.Number(strconv.FormatInt(int64(t), 10)), nil
	case int32:
		return json.Number(strconv.FormatInt(int64(t), 10)), nil
	case int64:
		return json.Number(strconv.FormatInt(t, 10)), nil
	case uint:
		return json.Number(strconv.FormatUint(uint64(t), 10)), nil
	case uint8:
		return json.Number(strconv.FormatUint(uint64(t), 10)), nil
	case uint16:
		return json.Number(strconv.FormatUint(uint64(t), 10)), nil
	case uint32:
		return json.Number(strconv.FormatUint(uint64(t), 10)), nil
	case uint64:
		return json.Number(strconv.FormatUint(t, 10)), nil
	case string, bool, nil:
		return t, nil
	default:
		raw, err := json.Marshal(v)
		if err != nil {
			return nil, err
		}
		decoded, err := Decode(raw)
		if err != nil {
			return nil, err
		}
		return normalize(decoded)
	}
}

func normalizeNumber(raw string) (any, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return json.Number("0"), nil
	}
	if i, err := strconv.ParseInt(s, 10, 64); err == nil {
		return json.Number(strconv.FormatInt(i, 10)), nil
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return nil, fmt.Errorf("canonicaljson: invalid number %q: %w", s, err)
	}
	return normalizeFloat(f)
}

func normalizeFloat(f float64) (any, error) {
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return nil, errors.New("canonicaljson: non-finite number")
	}
	if f == 0 {
		f = 0 // 归一 -0
	}
	return json.Number(strconv.FormatFloat(f, 'f', -1, 64)), nil
}
