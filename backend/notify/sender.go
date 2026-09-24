// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package notify

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"iot-gateway/logger"
)

const (
	// defaultHTTPTimeout 单次投递的 HTTP 超时。
	// 报警通知是旁路，宁可快速失败进重试，也不要长时间占住 worker。
	defaultHTTPTimeout = 5 * time.Second
	// maxRespBodyBytes 读取响应体的上限，防止异常端点返回超大 body。
	maxRespBodyBytes = 8 << 10
	// maxLogRunes 错误信息进日志/API 前的截断长度（按字符，避免切断多字节）。
	maxLogRunes = 200
)

// FailureKind 失败性质，决定是否值得重试。
type FailureKind int

const (
	// FailurePermanent 永久失败：重试也不会成功（配置错、鉴权失败、报文非法）。
	FailurePermanent FailureKind = iota
	// FailureRetriable 可重试：网络抖动、限流、平台侧 5xx。
	FailureRetriable
)

// sendError 带失败性质的发送错误。
type sendError struct {
	kind FailureKind
	err  error
}

func (e *sendError) Error() string { return e.err.Error() }
func (e *sendError) Unwrap() error { return e.err }

// isRetriable 判断错误是否值得重试。
// 非 sendError（不该出现）保守视为可重试，避免把瞬时问题当成永久失败丢弃。
func isRetriable(err error) bool {
	var se *sendError
	if errors.As(err, &se) {
		return se.kind == FailureRetriable
	}
	return true
}

// classifyHTTPStatus 按 HTTP 状态码判定失败性质。
// 408/429 与 5xx 可重试，其余 4xx（报文非法/鉴权失败/URL 错）重试无意义。
func classifyHTTPStatus(code int) FailureKind {
	switch {
	case code == http.StatusRequestTimeout || code == http.StatusTooManyRequests:
		return FailureRetriable
	case code >= 500:
		return FailureRetriable
	default:
		return FailurePermanent
	}
}

// retriableCodes 各平台业务错误码中的「限流」类，命中即可重试。
// 其余非 0 业务码（加签错误、关键字不匹配、机器人被移除等）重试无意义。
var retriableCodes = map[string]map[int]bool{
	TypeDingTalk: {130101: true}, // 发送速度太快
	TypeWeCom:    {45009: true},  // 接口调用超过限制
	TypeFeishu:   {11232: true},  // 频率限制
}

// platformResponse 三家平台错误响应的并集。
// 钉钉/企微用 errcode/errmsg，飞书新版用 code/msg、旧版用 StatusCode/StatusMessage。
type platformResponse struct {
	ErrCode       *int   `json:"errcode"`
	ErrMsg        string `json:"errmsg"`
	Code          *int   `json:"code"`
	Msg           string `json:"msg"`
	StatusCode    *int   `json:"StatusCode"`
	StatusMessage string `json:"StatusMessage"`
}

// decodePlatformResponse 解包平台响应体判定成败。
//
// 钉钉/企微/飞书在业务失败时同样返回 HTTP 200，只在 body 里给错误码，
// 因此只看状态码会把失败当成功，必须解包。
func decodePlatformResponse(platform string, statusCode int, body []byte) error {
	if platform == TypeCustom {
		// 自定义端点报文无统一约定，2xx 即视为成功
		return nil
	}

	var r platformResponse
	if err := json.Unmarshal(body, &r); err != nil {
		// 2xx 但 body 不可解析：按成功处理。宁可漏报一次也不误重试造成重复推送。
		logger.Warn("notify: %s response not parseable (http %d), treated as success: %s",
			platform, statusCode, truncateForLog(string(body)))
		return nil
	}

	code, msg, ok := r.pick()
	if !ok {
		logger.Warn("notify: %s response has no error code field (http %d), treated as success: %s",
			platform, statusCode, truncateForLog(string(body)))
		return nil
	}
	if code == 0 {
		return nil
	}

	kind := FailurePermanent
	if retriableCodes[platform][code] {
		kind = FailureRetriable
	}
	return &sendError{
		kind: kind,
		err:  fmt.Errorf("%s business error %d: %s", platform, code, msg),
	}
}

// pick 取出响应中的业务错误码与描述，按平台字段差异依次尝试。
func (r *platformResponse) pick() (code int, msg string, ok bool) {
	switch {
	case r.ErrCode != nil: // 钉钉 / 企微
		return *r.ErrCode, r.ErrMsg, true
	case r.Code != nil: // 飞书 v2
		return *r.Code, r.Msg, true
	case r.StatusCode != nil: // 飞书旧版
		return *r.StatusCode, r.StatusMessage, true
	}
	return 0, "", false
}

// sender 专用 HTTP 投递器。
//
// 不复用 utils.HttpPost：后者忽略状态码且无超时区分，无法区分「永久失败」与
// 「可重试」。TLS 校验保持开启（目标是公网平台端点）。
type sender struct {
	client *http.Client
}

// newSender 构建投递器；timeout <= 0 时用默认值。
func newSender(timeout time.Duration) *sender {
	if timeout <= 0 {
		timeout = defaultHTTPTimeout
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.MaxIdleConnsPerHost = 4
	return &sender{client: &http.Client{Timeout: timeout, Transport: transport}}
}

// send 执行一次投递，返回带失败性质的错误。
func (s *sender) send(req *Request) error {
	if req == nil {
		return &sendError{kind: FailurePermanent, err: errors.New("nil request")}
	}

	httpReq, err := http.NewRequest(req.Method, req.URL, bytes.NewReader(req.Body))
	if err != nil {
		return &sendError{kind: FailurePermanent, err: err}
	}
	for k, v := range req.Headers {
		httpReq.Header.Set(k, v)
	}

	resp, err := s.client.Do(httpReq)
	if err != nil {
		// 网络层错误（超时/连接拒绝/DNS 失败）：可重试
		return &sendError{kind: FailureRetriable, err: err}
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(io.LimitReader(resp.Body, maxRespBodyBytes))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return &sendError{
			kind: classifyHTTPStatus(resp.StatusCode),
			err:  fmt.Errorf("http %d: %s", resp.StatusCode, truncateForLog(string(body))),
		}
	}
	return decodePlatformResponse(req.Platform, resp.StatusCode, body)
}

// jsonHeaders 各平台统一的请求头。
func jsonHeaders() map[string]string {
	return map[string]string{"Content-Type": "application/json; charset=utf-8"}
}

// truncateForLog 截断错误/响应文本，避免整段 HTML 之类的内容灌进日志与 API 响应。
// 按 rune 截断，不会切断多字节字符。
func truncateForLog(s string) string {
	runes := []rune(s)
	if len(runes) <= maxLogRunes {
		return s
	}
	return string(runes[:maxLogRunes]) + "…"
}
