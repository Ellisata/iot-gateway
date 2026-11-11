// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package collector

import (
	"testing"
	"time"

	"github.com/Ellisata/iot-gateway-sdk/driver"
)

func TestCompileAddressTransformApplyScale(t *testing.T) {
	ct, err := compileAddressTransform(`[{"op":"scale","factor":0.1}]`)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	value, kind, err := ct.apply("253", driver.KindInt, driver.QualityGood)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if value != "25.3" {
		t.Fatalf("value = %q, want 25.3", value)
	}
	if kind != driver.KindFloat {
		t.Fatalf("kind = %q, want %q（缩放会把整数提升为浮点）", kind, driver.KindFloat)
	}
}

func TestApplyRoundKeepsIntegerKind(t *testing.T) {
	ct, err := compileAddressTransform(`[{"op":"round","digits":0}]`)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	value, kind, err := ct.apply("12.6", driver.KindInt, driver.QualityGood)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if value != "13" {
		t.Fatalf("value = %q, want 13", value)
	}
	if kind != driver.KindInt {
		t.Fatalf("kind = %q, want %q（digits=0 不需要提升）", kind, driver.KindInt)
	}
}

func TestApplyDefaultOnEmpty(t *testing.T) {
	ct, err := compileAddressTransform(`[{"op":"default","value":"0"},{"op":"scale","factor":2}]`)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	value, _, err := ct.apply("", driver.KindInt, driver.QualityGood)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if value != "0" {
		t.Fatalf("value = %q, want 0", value)
	}
}

func TestApplySkipsEmptyWithoutDefault(t *testing.T) {
	ct, _ := compileAddressTransform(`[{"op":"scale","factor":2}]`)
	value, kind, err := ct.apply("", driver.KindInt, driver.QualityGood)
	if err != nil {
		t.Fatalf("空值不应报错（没有 default 时直接跳过）: %v", err)
	}
	if value != "" || kind != driver.KindFloat {
		t.Fatalf("value=%q kind=%q", value, kind)
	}
}

func TestApplySkipsBadQuality(t *testing.T) {
	ct, _ := compileAddressTransform(`[{"op":"scale","factor":2}]`)
	value, kind, err := ct.apply("placeholder", driver.KindInt, driver.QualityBadComms)
	if err != nil {
		t.Fatalf("坏质量的占位值不应报错: %v", err)
	}
	if value != "placeholder" || kind != driver.KindFloat {
		t.Fatalf("value=%q kind=%q", value, kind)
	}
}

func TestApplyKeepsRawOnError(t *testing.T) {
	ct, _ := compileAddressTransform(`[{"op":"scale","factor":2}]`)
	value, kind, err := ct.apply("not-a-number", driver.KindInt, driver.QualityGood)
	if err == nil {
		t.Fatal("非数字值配 scale 应返回错误")
	}
	if value != "not-a-number" {
		t.Fatalf("失败时应保留原值，实际 %q", value)
	}
	if kind != driver.KindFloat {
		t.Fatalf("Kind 按配置提升，实际 %q", kind)
	}
}

func TestCompileAddressTransformRejectsInvalid(t *testing.T) {
	if _, err := compileAddressTransform(`[{"op":"regex","pattern":"(\\d+)"}]`); err == nil {
		t.Fatal("点位侧不应接受 regex 算子")
	}
	if _, err := compileAddressTransform(`not json`); err == nil {
		t.Fatal("非法 JSON 应返回错误")
	}
}

func TestShouldLogAddressTransformError(t *testing.T) {
	task := &gatewayTask{}
	t0 := time.Now()

	if !task.shouldLogAddressTransformError("addr-1", "boom", t0) {
		t.Fatal("首次失败应记一行")
	}
	if task.shouldLogAddressTransformError("addr-1", "boom", t0.Add(time.Second)) {
		t.Fatal("同一文案在窗口内不该重复记")
	}
	if !task.shouldLogAddressTransformError("addr-1", "另一个原因", t0.Add(time.Second)) {
		t.Fatal("文案变化应立即记")
	}
	if !task.shouldLogAddressTransformError("addr-1", "另一个原因", t0.Add(transformErrLogInterval+time.Second)) {
		t.Fatal("窗口过后应重新记")
	}
	if !task.shouldLogAddressTransformError("addr-2", "另一个原因", t0.Add(time.Second)) {
		t.Fatal("另一个点位应各自计时")
	}
}
