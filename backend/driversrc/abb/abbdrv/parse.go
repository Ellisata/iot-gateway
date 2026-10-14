// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package abb

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// errNotJSON 响应体不是可用的 RWS JSON。
//
// 这是**服务级**错误（该点位/该类列表本轮作废，但连接状态不动），
// 不是传输级 —— 它最常见的两个成因都是配置问题，不是控制器失联：
//   - 忘了加 ?json=1，RWS 1.0 于是回了 XML（默认表示）；
//   - 版本探测判错了（1.0 的固件收到了 2.0 的 Accept 头）。
//
// 两种情况下连接都是好的，把它判成断连会让采集引擎白白重连一轮。
var errNotJSON = errors.New("响应体不是 RWS JSON")

// snippet 截取响应体片段用于错误消息。
// 长度取 120 字节：够看清 "<HTML>" / "<?xml" 这类开头，又不至于把日志刷屏。
func snippet(b []byte) string {
	const maxLen = 120
	b = bytes.TrimSpace(b)
	if len(b) > maxLen {
		return string(b[:maxLen]) + "..."
	}
	return string(b)
}

// decodeJSON 解码 RWS 的 HAL 风格 JSON。
//
// 一律解成 any（map/slice/标量），**不钉 struct**：RWS 的字段名随 RobotWare
// 版本与资源类型漂移（1.0 用 lvalue、2.0 可能是 value；value-type 与 valuetype
// 都出现过）。钉 struct 的失败方式是"某个版本下所有点位静默变成空值" ——
// 零值不会报错，也没人会去看日志。
//
// UseNumber 而非 float64：RWS 的值几乎都是字符串，但描述符里有真数字
// （索引、长度），float64 往返会让大整数变形。
func decodeJSON(body []byte) (any, error) {
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 {
		return nil, fmt.Errorf("abb: %w：响应体为空", errNotJSON)
	}
	// 先看首字符再交给 Decoder：XML 开头是 '<'，HTML 也是，能让错误消息
	// 一眼指向"没拿到 JSON"。Decoder 报的 "invalid character '<'" 也行，
	// 但不会附上响应体片段。
	if trimmed[0] != '{' && trimmed[0] != '[' {
		return nil, fmt.Errorf("abb: %w（前 120 字节：%s）", errNotJSON, snippet(trimmed))
	}
	dec := json.NewDecoder(bytes.NewReader(trimmed))
	dec.UseNumber()
	var root any
	if err := dec.Decode(&root); err != nil {
		return nil, fmt.Errorf("abb: %w：JSON 解析失败: %v（前 120 字节：%s）", errNotJSON, err, snippet(trimmed))
	}
	return root, nil
}

// statesOf 从响应根对象里取出资源状态条目列表。
//
// RWS 的 JSON 表示是 HAL 风格，正常形态是
// {"_embedded":{"_state":[{...},{...}]},"_links":{...}}，但**不同版本、不同
// 资源会缺层或多层**（有的直接给 {"_state":[...]}，有的把数组挂在别的键下）。
//
// 因此这里走的是一条**回退链**，命中即返回，绝不因为形状不熟悉就放弃 ——
// 放弃的代价是"某类资源在某版本固件上全部采不到值"。链的末端是"把根对象
// 自身当作唯一一条状态"，这让单资源响应（如 /rw/panel/ctrl-state）不需要
// 任何特殊处理。
func statesOf(root any) []map[string]any {
	switch v := root.(type) {
	case map[string]any:
		// 1. 标准形态：_embedded._state[]
		if emb, ok := v["_embedded"].(map[string]any); ok {
			if st, ok := emb["_state"].([]any); ok {
				if objs := objectsOf(st); len(objs) > 0 {
					return objs
				}
			}
		}
		// 2. 少一层的形态：_state[]
		if st, ok := v["_state"].([]any); ok {
			if objs := objectsOf(st); len(objs) > 0 {
				return objs
			}
		}
		// 3. 非标准嵌套：深度 ≤3 的 DFS 找第一个名为 _state 的数组
		if objs, ok := findStateDeep(v, 3); ok {
			return objs
		}
		// 4. _embedded 下挂着别的数组（键名未知的版本）
		if emb, ok := v["_embedded"].(map[string]any); ok {
			for _, k := range sortedKeys(emb) {
				if arr, ok := emb[k].([]any); ok {
					if objs := objectsOf(arr); len(objs) > 0 {
						return objs
					}
				}
			}
		}
		// 5. 没有包装的单资源响应：根对象就是那一条状态
		return []map[string]any{v}

	case []any:
		return objectsOf(v)
	}
	return nil
}

// findStateDeep 在深度限制内查找第一个名为 _state 的数组。
// 按 key 排序遍历，保证同输入同结果（map 的遍历顺序是随机的）。
func findStateDeep(v any, depth int) ([]map[string]any, bool) {
	if depth <= 0 {
		return nil, false
	}
	m, ok := v.(map[string]any)
	if !ok {
		return nil, false
	}
	for _, k := range sortedKeys(m) {
		child := m[k]
		if arr, ok := child.([]any); ok && strings.EqualFold(k, "_state") {
			if objs := objectsOf(arr); len(objs) > 0 {
				return objs, true
			}
		}
		if objs, ok := findStateDeep(child, depth-1); ok {
			return objs, true
		}
	}
	return nil, false
}

// objectsOf 过滤出数组里的对象元素，忽略标量元素。
func objectsOf(arr []any) []map[string]any {
	out := make([]map[string]any, 0, len(arr))
	for _, e := range arr {
		if m, ok := e.(map[string]any); ok {
			out = append(out, m)
		}
	}
	return out
}

// fieldOf 按候选名取值。
//
// 判据是**键存在**，不是"值非空"：RWS 用 "" 表示空文本、用 false 表示逻辑 0、
// 用 0 表示零值，按值判空会把它们统统当成"字段缺失"并标 Quality=0 ——
// 那会让一台正常控制器的空信号永远显示为异常。
//
// 先按候选顺序精确匹配（这是绝大多数情况，零分配），再退一步做大小写不敏感
// 匹配（同一字段在不同固件上大小写不一致，如 lvalue / LValue）。
func fieldOf(m map[string]any, candidates ...string) (any, bool) {
	for _, c := range candidates {
		if v, ok := m[c]; ok {
			return v, true
		}
	}

	// 回退：直接遍历 map 找大小写变体，**不要先 sortedKeys 再扫**。
	//
	// 那条路是"每个候选一次分配 + 一次排序"，而这里恰恰是**字段确实不存在**
	// 时才会走到的分支 —— isSimulated 查的 lstate 在非仿真信号上必然落空，
	// 也就是绝大多数信号、每一轮都要为一次注定失败的查找付一次排序
	// （2000 点位实测 537ns/次、2 次分配，改成这样是 183ns、零分配）。
	//
	// 定序没有丢，只是收窄到真正需要它的情形：同一个候选命中多个大小写变体时，
	// 结果必须与 map 的遍历顺序无关（见 sortedKeys 的说明），取字典序最小的那个。
	// 正常固件不会出现同一字段的多个变体，所以这条分支基本不执行。
	for _, c := range candidates {
		var found []string
		for k := range m {
			if strings.EqualFold(k, c) {
				found = append(found, k)
			}
		}
		switch len(found) {
		case 0:
			continue
		case 1:
			return m[found[0]], true
		default:
			sort.Strings(found)
			return m[found[0]], true
		}
	}
	return nil, false
}

// scalarString 把标量值转成字符串。
// 对象与数组返回 false —— 那是复合值，走 compoundString。
func scalarString(v any) (string, bool) {
	switch x := v.(type) {
	case nil:
		return "", true
	case string:
		return x, true
	case json.Number:
		return x.String(), true
	case float64:
		return strconv.FormatFloat(x, 'g', -1, 64), true
	case bool:
		if x {
			return "1", true
		}
		return "0", true
	}
	return "", false
}

// 复合值的固定字段顺序。
//
// 按固定顺序而非 map 遍历顺序输出，是为了让同一个复合值在任何一轮、任何
// 进程里都格式化成同一个字符串 —— 否则时序库里的"变化点"会全是噪声，
// 而排查者根本看不出值的哪个分量动了。
var (
	poseOrder  = []string{"x", "y", "z", "q1", "q2", "q3", "q4", "extjoint"}
	jointOrder = []string{
		"rax_1", "rax_2", "rax_3", "rax_4", "rax_5", "rax_6",
		"rex_1", "rex_2", "rex_3", "rex_4", "rex_5", "rex_6",
		"eax_a", "eax_b", "eax_c", "eax_d", "eax_e", "eax_f",
		"extjoint",
	}
)

// compoundString 把复合值（robtarget / jointtarget / 任意嵌套对象）格式化为
// 自描述的 "k=v;k=v" 文本。
//
// 带键名而不是只拼值：`x=1.5;y=2;z=3` 一眼能看懂，`1.5,2,3` 不能 ——
// 而复合值本来就是"用户该用 #field 取分量、却整取了"的情况，
// 输出必须自解释才不至于变成一个纯噪声点位。
func compoundString(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case map[string]any:
		keys := orderedKeys(x)
		parts := make([]string, 0, len(keys))
		for _, k := range keys {
			parts = append(parts, k+"="+compoundString(x[k]))
		}
		return strings.Join(parts, ";")
	case []any:
		parts := make([]string, 0, len(x))
		for _, e := range x {
			parts = append(parts, compoundString(e))
		}
		return strings.Join(parts, ",")
	}
	if s, ok := scalarString(v); ok {
		return s
	}
	return fmt.Sprintf("%v", v)
}

// orderedKeys 返回复合值的输出顺序：命中已知形状（位姿/关节角）时用固定顺序，
// 其余按键名排序兜底 —— 总之必须确定。
func orderedKeys(m map[string]any) []string {
	var order []string
	switch {
	case hasKey(m, "x") && hasKey(m, "y"):
		order = poseOrder
	case hasKey(m, "rax_1"):
		order = jointOrder
	}

	out := make([]string, 0, len(m))
	seen := make(map[string]bool, len(m))
	for _, k := range order {
		for _, actual := range sortedKeys(m) {
			if seen[actual] || !strings.EqualFold(actual, k) {
				continue
			}
			out = append(out, actual)
			seen[actual] = true
		}
	}
	for _, k := range sortedKeys(m) {
		if !seen[k] {
			out = append(out, k)
		}
	}
	return out
}

func hasKey(m map[string]any, name string) bool {
	for k := range m {
		if strings.EqualFold(k, name) {
			return true
		}
	}
	return false
}

// sortedKeys 返回按字典序排列的键。
// Go 的 map 遍历顺序是随机的，而"某个键先被选到"会直接影响取值结果与
// 报错文本 —— 这里必须确定性。
func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
