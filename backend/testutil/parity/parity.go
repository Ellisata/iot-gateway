// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

// Package parity 读取 pluginhost/testdata/parity 下的**行为等价黄金基线**。
//
// 协议驱动从内置实现搬成进程外插件后，仓库里不再有第二份实现可以对照，
// 等价性因此从「跨实现」改为「跨时间」：内置实现还在时，用假 PLC 把它采出的
// 逐点位结果与类型表冻成黄金；之后插件驱动必须复现同一份黄金。
//
// 分成普通包（而非测试文件）是为了让 driversrc/ 下的插件也能读 ——
// 插件的 main 包是 package main，无法 import pluginhost 的测试代码。
package parity

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

// TypeMapGolden 一个协议 scope 的类型表与协议名清单。
//
// Protocols 是**防手抄错误**的关键：协议名写错一个字符，用它的设备会
// 被判为「协议不支持」而静默跳过（见 collector.newGatewayTask），
// 前端、健康检查全都正常。若这串名字靠人在插件里照抄，抄错时插件自带的
// 测试会跟着一起错 —— 所以它必须由内置实现机器生成。
type TypeMapGolden struct {
	Scope     string   `json:"scope"`
	Protocols []string `json:"protocols"`
	Common    []string `json:"common"`
	Extended  []string `json:"extended"`
}

// DataDir 返回黄金基线目录的绝对路径。
//
// 以 go.mod 为锚点向上查找，而非依赖当前工作目录：各包的测试工作目录是
// 各自的包目录（backend/pluginhost、backend/driversrc/modbus …），
// 写死相对路径会在插件包里找不到文件。
func DataDir() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return filepath.Join(dir, "pluginhost", "testdata", "parity"), nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("parity: 向上未找到 go.mod（从 %s 起）", dir)
		}
		dir = parent
	}
}

// LoadTypeMap 读取某个 scope 的类型表黄金。
func LoadTypeMap(scope string) (TypeMapGolden, error) {
	dir, err := DataDir()
	if err != nil {
		return TypeMapGolden{}, err
	}
	path := filepath.Join(dir, "typemap", scope+".json")

	raw, err := os.ReadFile(path)
	if err != nil {
		return TypeMapGolden{}, fmt.Errorf("读取类型表黄金失败: %w", err)
	}
	var g TypeMapGolden
	if err := json.Unmarshal(raw, &g); err != nil {
		return TypeMapGolden{}, fmt.Errorf("解析类型表黄金 %s 失败: %w", path, err)
	}
	return g, nil
}

// LoadGolden 读取某个用例的逐点位黄金。
func LoadGolden(name string) (Golden, error) {
	dir, err := DataDir()
	if err != nil {
		return Golden{}, err
	}
	path := filepath.Join(dir, name+".golden.json")

	raw, err := os.ReadFile(path)
	if err != nil {
		return Golden{}, fmt.Errorf("读取黄金文件失败: %w", err)
	}
	var g Golden
	if err := json.Unmarshal(raw, &g); err != nil {
		return Golden{}, fmt.Errorf("解析黄金文件 %s 失败: %w", path, err)
	}
	return g, nil
}

// Golden 一条用例冻结下来的逐点位结果。
type Golden struct {
	Case     string   `json:"case"`
	Protocol string   `json:"protocol"`
	Results  []Result `json:"results"`
}

// Result 是 driver.ReadResult 的可比对投影。
type Result struct {
	ID       string `json:"id"`
	Value    string `json:"value"`
	DataType string `json:"dataType"`
	Kind     string `json:"kind"`
	Quality  int    `json:"quality"`
}

// CompareResults 逐项比对实测结果与黄金，返回人类可读的差异描述（一致则为空）。
func CompareResults(got []Result, want []Result) []string {
	var diffs []string

	if len(got) != len(want) {
		return []string{fmt.Sprintf("结果数 %d != 黄金 %d", len(got), len(want))}
	}
	for i := range want {
		if got[i] == want[i] {
			continue
		}
		if got[i].ID != want[i].ID {
			diffs = append(diffs, fmt.Sprintf("第 %d 项 ID = %q，黄金 %q（顺序错位）", i, got[i].ID, want[i].ID))
			continue
		}
		diffs = append(diffs, fmt.Sprintf("点位 %s 不符：实际 %+v，黄金 %+v", want[i].ID, got[i], want[i]))
	}
	return diffs
}

// EqualStrings 比较两个字符串切片是否逐项相等。
func EqualStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// SortedCopy 返回排序后的副本，便于稳定比较与打印。
func SortedCopy(in []string) []string {
	out := append([]string(nil), in...)
	sort.Strings(out)
	return out
}
