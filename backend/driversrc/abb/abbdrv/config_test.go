// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package abb

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"
)

func TestDefaultConfig(t *testing.T) {
	c := DefaultConfig()
	if c.UseHTTPS != triAuto || c.RWSVersion != versionAuto {
		t.Errorf("默认应为自动探测：useHttps=%q rwsVersion=%q", c.UseHTTPS, c.RWSVersion)
	}
	if !c.InsecureSkipVerify {
		t.Error("默认应跳过证书校验：ABB 控制器出厂即自签名，默认校验会让首次连接必然失败")
	}
	if !c.BulkRead {
		t.Error("默认应开启批量读取：控制器有 <20 req/s 的硬限流")
	}
	if c.SessionRequest != triAuto {
		t.Errorf("默认 sessionRequest 应为 auto，实际 %q", c.SessionRequest)
	}
	if c.Timeout != 5*time.Second {
		t.Errorf("默认超时应为 5s，实际 %v", c.Timeout)
	}
}

func TestParseConfigEmptyAndEmptyObject(t *testing.T) {
	for _, in := range []string{"", "  ", "{}"} {
		c, err := ParseConfig(in)
		if err != nil {
			t.Fatalf("ParseConfig(%q) 失败: %v", in, err)
		}
		if !reflect.DeepEqual(c, DefaultConfig()) {
			t.Errorf("ParseConfig(%q) 应返回默认配置\n got  %+v\n want %+v", in, c, DefaultConfig())
		}
	}
}

func TestParseConfigInvalidJSON(t *testing.T) {
	if _, err := ParseConfig("{not json"); err == nil {
		t.Error("非法 JSON 应报错")
	}
}

// TestTriBoolForms form-create 的 select 回传字符串，手写的 protocol_json
// 习惯用 JSON 布尔 —— 两种形态都必须接受。
func TestTriBoolForms(t *testing.T) {
	cases := []struct {
		raw     string
		want    string
		wantErr bool
	}{
		{``, triAuto, false},         // 字段缺失（raw 为 nil）
		{`null`, triAuto, false},     // 显式 null
		{`"auto"`, triAuto, false},   // select 回传
		{`true`, triTrue, false},     // JSON 布尔
		{`false`, triFalse, false},   //
		{`"true"`, triTrue, false},   // 字符串布尔
		{`"false"`, triFalse, false}, //
		{`"maybe"`, "", true},        // 非法取值必须报错，不能静默取默认
		{`1`, "", true},              // 数字也不是合法形态
	}
	for _, c := range cases {
		var raw json.RawMessage
		if c.raw != "" {
			raw = json.RawMessage(c.raw)
		}
		got, err := triBool(raw, triAuto, "test")
		if c.wantErr {
			if err == nil {
				t.Errorf("triBool(%s) 应报错，实际返回 %q", c.raw, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("triBool(%s) 失败: %v", c.raw, err)
			continue
		}
		if got != c.want {
			t.Errorf("triBool(%s) = %q, 期望 %q", c.raw, got, c.want)
		}
	}
}

func TestParseConfigBoolFields(t *testing.T) {
	c, err := ParseConfig(`{"host":"10.0.0.1","insecureSkipVerify":false,"bulkRead":false,"sessionRequest":"true"}`)
	if err != nil {
		t.Fatalf("ParseConfig 失败: %v", err)
	}
	if c.InsecureSkipVerify {
		t.Error("insecureSkipVerify=false 应生效")
	}
	if c.BulkRead {
		t.Error("bulkRead=false 应生效")
	}
	if c.SessionRequest != triTrue {
		t.Errorf("sessionRequest = %q, 期望 true", c.SessionRequest)
	}
}

func TestParseConfigClamping(t *testing.T) {
	c, err := ParseConfig(`{"host":"h","timeoutMs":999999,"maxRequestsPerSecond":999}`)
	if err != nil {
		t.Fatalf("ParseConfig 失败: %v", err)
	}
	if c.TimeoutMS != 30000 {
		t.Errorf("timeoutMs 应被钳到 30000，实际 %d", c.TimeoutMS)
	}
	if c.MaxRequestsPerSecond != maxRequestsPerSecondLimit {
		t.Errorf("maxRequestsPerSecond 应被钳到 %d，实际 %d", maxRequestsPerSecondLimit, c.MaxRequestsPerSecond)
	}

	c, err = ParseConfig(`{"host":"h","timeoutMs":1,"maxRequestsPerSecond":-5}`)
	if err != nil {
		t.Fatalf("ParseConfig 失败: %v", err)
	}
	if c.TimeoutMS != 200 {
		t.Errorf("timeoutMs 应被钳到 200，实际 %d", c.TimeoutMS)
	}
	// 负数回退默认值（>0 才覆盖），再由钳制兜底。
	if c.MaxRequestsPerSecond < 1 {
		t.Errorf("maxRequestsPerSecond 不应小于 1，实际 %d", c.MaxRequestsPerSecond)
	}
}

func TestParseConfigEnums(t *testing.T) {
	bad := []struct{ json, why string }{
		{`{"host":"h","rwsVersion":"3.0"}`, "未知 RWS 版本"},
		{`{"host":"h","cartesianSource":"joint"}`, "未知笛卡尔来源"},
		{`{"host":"h","cartesianCoordinate":"Frob"}`, "未知坐标系"},
	}
	for _, c := range bad {
		if _, err := ParseConfig(c.json); err == nil {
			t.Errorf("ParseConfig(%s)（%s）应报错", c.json, c.why)
		}
	}

	good := []struct{ json, field, want string }{
		{`{"host":"h","rwsVersion":"1.0"}`, "RWSVersion", version10},
		{`{"host":"h","rwsVersion":"2.0"}`, "RWSVersion", version20},
		{`{"host":"h","cartesianSource":"CARTESIAN"}`, "CartesianSource", "cartesian"},
		{`{"host":"h","cartesianCoordinate":"world"}`, "CartesianCoordinate", "World"},
	}
	for _, c := range good {
		cfg, err := ParseConfig(c.json)
		if err != nil {
			t.Fatalf("ParseConfig(%s) 失败: %v", c.json, err)
		}
		switch c.field {
		case "RWSVersion":
			if cfg.RWSVersion != c.want {
				t.Errorf("RWSVersion = %q, 期望 %q", cfg.RWSVersion, c.want)
			}
		case "CartesianSource":
			if cfg.CartesianSource != c.want {
				t.Errorf("CartesianSource = %q, 期望 %q", cfg.CartesianSource, c.want)
			}
		case "CartesianCoordinate":
			if cfg.CartesianCoordinate != c.want {
				t.Errorf("CartesianCoordinate = %q, 期望 %q", cfg.CartesianCoordinate, c.want)
			}
		}
	}
}

// TestParseConfigEmptyHostFallsBackToDefault 空 host 沿用默认值。
//
// 与 opcuadrv 一致。设备表单里 host 本就是必填项，所以这里不需要为
// "host 为空"另设一条错误 —— 那是构造不出来的状态。
func TestParseConfigEmptyHostFallsBackToDefault(t *testing.T) {
	c, err := ParseConfig(`{"host":""}`)
	if err != nil {
		t.Fatalf("ParseConfig 失败: %v", err)
	}
	if c.Host != "127.0.0.1" {
		t.Errorf("host = %q, 期望默认的 127.0.0.1", c.Host)
	}
	// endpoint 优先于 host/port。
	c, err = ParseConfig(`{"host":"ignored","endpoint":"https://10.0.0.1:443"}`)
	if err != nil {
		t.Fatalf("ParseConfig 失败: %v", err)
	}
	if got := c.candidateBaseURLs(); len(got) != 1 || got[0] != "https://10.0.0.1:443" {
		t.Errorf("candidateBaseURLs = %q, 期望只有 endpoint", got)
	}
}

// TestParseConfigPasswordPresenceDistinguishesFromEmpty 是本文件里最容易写错的
// 一条：**字段缺失**与**显式空串**必须区分开。
//
// 用 string 接时两者都是 ""，只能二选一 —— 要么没法配置免认证的空密码，
// 要么用户不动表单就把出厂默认密码 robotics 丢了。
func TestParseConfigPasswordPresenceDistinguishesFromEmpty(t *testing.T) {
	c, err := ParseConfig(`{"host":"h","username":"Default User"}`)
	if err != nil {
		t.Fatalf("ParseConfig 失败: %v", err)
	}
	if c.Password != "robotics" {
		t.Errorf("密码字段缺失时应沿用默认的 robotics，实际 %q", c.Password)
	}

	c, err = ParseConfig(`{"host":"h","username":"Default User","password":""}`)
	if err != nil {
		t.Fatalf("ParseConfig 失败: %v", err)
	}
	if c.Password != "" {
		t.Errorf("显式空密码应生效，实际 %q", c.Password)
	}

	c, err = ParseConfig(`{"host":"h","password":"secret"}`)
	if err != nil {
		t.Fatalf("ParseConfig 失败: %v", err)
	}
	if c.Password != "secret" {
		t.Errorf("password = %q, 期望 secret", c.Password)
	}
}

func TestCandidateBaseURLs(t *testing.T) {
	cases := []struct {
		name string
		cfg  *Config
		want []string
	}{
		{
			"auto + 无端口：HTTPS 优先，HTTP 兜底",
			&Config{Host: "10.0.0.1", UseHTTPS: triAuto},
			[]string{"https://10.0.0.1:443", "http://10.0.0.1:80"},
		},
		{
			"auto + 指定端口：两个 scheme 共用该端口",
			&Config{Host: "10.0.0.1", Port: 5466, UseHTTPS: triAuto},
			[]string{"https://10.0.0.1:5466", "http://10.0.0.1:5466"},
		},
		{
			"显式 HTTPS：不回落",
			&Config{Host: "10.0.0.1", UseHTTPS: triTrue},
			[]string{"https://10.0.0.1:443"},
		},
		{
			"显式 HTTP：不尝试 HTTPS",
			&Config{Host: "10.0.0.1", UseHTTPS: triFalse},
			[]string{"http://10.0.0.1:80"},
		},
		{
			"host 字段里带了端口（现场很常见）",
			&Config{Host: "10.0.0.1:8080", UseHTTPS: triAuto},
			[]string{"https://10.0.0.1:8080", "http://10.0.0.1:8080"},
		},
		{
			"endpoint 优先且去掉结尾斜杠",
			&Config{Endpoint: "https://10.0.0.1:443/", Host: "ignored"},
			[]string{"https://10.0.0.1:443"},
		},
		{
			"endpoint 缺 scheme 时按 https 补",
			&Config{Endpoint: "10.0.0.1:8443"},
			[]string{"https://10.0.0.1:8443"},
		},
		{
			"IPv6 地址要加方括号",
			&Config{Host: "fe80::1", UseHTTPS: triFalse},
			[]string{"http://[fe80::1]:80"},
		},
	}

	for _, c := range cases {
		got := c.cfg.candidateBaseURLs()
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s\n candidateBaseURLs() = %q\n 期望 %q", c.name, got, c.want)
		}
	}
}

func TestNormalizeBaseURL(t *testing.T) {
	cases := []struct{ in, want string }{
		{"https://h:443", "https://h:443"},
		{"https://h:443/", "https://h:443"},
		{"http://h/", "http://h"},
		{"h:80", "https://h:80"},
	}
	for _, c := range cases {
		if got := normalizeBaseURL(c.in); got != c.want {
			t.Errorf("normalizeBaseURL(%q) = %q, 期望 %q", c.in, got, c.want)
		}
	}
}
