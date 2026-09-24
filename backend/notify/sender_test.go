// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package notify

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestClassifyHTTPStatus(t *testing.T) {
	cases := []struct {
		code int
		want FailureKind
	}{
		{200, FailurePermanent}, // 2xx 不会走到分类（调用方已先判成功），此处只验证不会 panic
		{400, FailurePermanent},
		{401, FailurePermanent},
		{404, FailurePermanent},
		{408, FailureRetriable},
		{429, FailureRetriable},
		{500, FailureRetriable},
		{502, FailureRetriable},
		{503, FailureRetriable},
	}
	for _, c := range cases {
		if got := classifyHTTPStatus(c.code); got != c.want {
			t.Fatalf("classifyHTTPStatus(%d) = %v, want %v", c.code, got, c.want)
		}
	}
}

// 三家平台出错时都返回 HTTP 200 + body 里的业务错误码，
// 只看状态码会把失败当成功 —— 这些用例锁住该行为。
func TestDecodePlatformResponse(t *testing.T) {
	cases := []struct {
		name      string
		platform  string
		body      string
		wantErr   bool
		retriable bool
	}{
		{"dingtalk ok", TypeDingTalk, `{"errcode":0,"errmsg":"ok"}`, false, false},
		{"dingtalk sign error", TypeDingTalk, `{"errcode":310000,"errmsg":"sign not match"}`, true, false},
		{"dingtalk rate limited", TypeDingTalk, `{"errcode":130101,"errmsg":"send too fast"}`, true, true},
		{"wecom ok", TypeWeCom, `{"errcode":0,"errmsg":"ok"}`, false, false},
		{"wecom rate limited", TypeWeCom, `{"errcode":45009,"errmsg":"api freq out of limit"}`, true, true},
		{"wecom invalid key", TypeWeCom, `{"errcode":93000,"errmsg":"invalid webhook url"}`, true, false},
		{"feishu v2 ok", TypeFeishu, `{"code":0,"msg":"success"}`, false, false},
		{"feishu legacy ok", TypeFeishu, `{"StatusCode":0,"StatusMessage":"success"}`, false, false},
		{"feishu rate limited", TypeFeishu, `{"code":11232,"msg":"rate limited"}`, true, true},
		{"feishu sign fail", TypeFeishu, `{"code":19021,"msg":"sign match fail"}`, true, false},
		{"unparseable body treated as success", TypeDingTalk, `<html>502</html>`, false, false},
		{"no code field treated as success", TypeDingTalk, `{"hello":"world"}`, false, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := decodePlatformResponse(c.platform, http.StatusOK, []byte(c.body))
			if c.wantErr && err == nil {
				t.Fatalf("expected error for %s, got nil", c.body)
			}
			if !c.wantErr && err != nil {
				t.Fatalf("expected success for %s, got %v", c.body, err)
			}
			if c.wantErr && c.retriable && !isRetriable(err) {
				t.Fatalf("expected %s to be retriable, got %v", c.body, err)
			}
			if c.wantErr && !c.retriable && isRetriable(err) {
				t.Fatalf("expected %s to be permanent, got retriable", c.body)
			}
		})
	}
}

// 自定义端点无统一报文约定：2xx 即成功，不解析 body。
func TestDecodeCustomIgnoresBody(t *testing.T) {
	if err := decodePlatformResponse(TypeCustom, http.StatusOK, []byte(`{"code":500}`)); err != nil {
		t.Fatalf("custom endpoint should treat any 2xx as success, got %v", err)
	}
}

func TestSendClassifiesTransportErrors(t *testing.T) {
	// 5xx -> 可重试
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	err := newSender(time.Second).send(&Request{
		Method: http.MethodPost, URL: srv.URL, Platform: TypeCustom,
	})
	if err == nil || !isRetriable(err) {
		t.Fatalf("expected retriable error for 500, got %v", err)
	}

	// 4xx -> 永久失败
	srv2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer srv2.Close()

	err = newSender(time.Second).send(&Request{
		Method: http.MethodPost, URL: srv2.URL, Platform: TypeCustom,
	})
	if err == nil || isRetriable(err) {
		t.Fatalf("expected permanent error for 400, got %v", err)
	}

	// 连接失败 -> 可重试
	err = newSender(time.Second).send(&Request{
		Method: http.MethodPost, URL: "http://127.0.0.1:1/nope", Platform: TypeCustom,
	})
	if err == nil || !isRetriable(err) {
		t.Fatalf("expected retriable error for connection failure, got %v", err)
	}
}

func TestTruncateForLogKeepsRunes(t *testing.T) {
	long := ""
	for i := 0; i < 500; i++ {
		long += "报"
	}
	got := truncateForLog(long)
	if len([]rune(got)) != maxLogRunes+1 { // +1 是省略号
		t.Fatalf("unexpected truncated rune count: %d", len([]rune(got)))
	}
	if short := truncateForLog("ok"); short != "ok" {
		t.Fatalf("short string should be untouched, got %q", short)
	}
}
