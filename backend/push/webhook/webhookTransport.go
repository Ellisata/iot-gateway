// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package webhook

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"iot-gateway/push/messagechannel"
)

const maxErrorBodyBytes = 8 << 10

type transport struct {
	cfg     *config
	client  *http.Client
	headers map[string]string
	url     *url.URL
}

func newTransport(cfg *config) *transport {
	headers, _ := parseHeaders(cfg.HeadersJSON)
	u, _ := url.Parse(cfg.URL)

	tr := &http.Transport{
		MaxIdleConnsPerHost: cfg.Workers,
		IdleConnTimeout:     90 * time.Second,
		TLSClientConfig:     &tls.Config{InsecureSkipVerify: cfg.InsecureSkipVerify}, //nolint:gosec // 显式用户配置
	}
	client := &http.Client{
		Timeout:   time.Duration(cfg.timeoutSeconds()) * time.Second,
		Transport: tr,
		// 3xx 视为配置错误（重定向目标通常不是集成商预期的接口）。
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	return &transport{cfg: cfg, client: client, headers: headers, url: u}
}

// Probe 只做 TCP 连接（https 再做 TLS 握手），不发 HTTP 请求，避免副作用。
// 路径、鉴权与载荷是否被接受由 TestSend 验证。
func (t *transport) Probe(ctx context.Context) error {
	host := t.url.Hostname()
	port := t.url.Port()
	if port == "" {
		if t.url.Scheme == "https" {
			port = "443"
		} else {
			port = "80"
		}
	}
	addr := net.JoinHostPort(host, port)

	dialer := &net.Dialer{Timeout: 5 * time.Second}
	if t.url.Scheme == "http" {
		conn, err := dialer.DialContext(ctx, "tcp", addr)
		if err != nil {
			return fmt.Errorf("连接 %s 失败: %w", addr, err)
		}
		return conn.Close()
	}

	raw, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		return fmt.Errorf("连接 %s 失败: %w", addr, err)
	}
	defer raw.Close()
	tlsConn := tls.Client(raw, &tls.Config{
		ServerName:         host,
		InsecureSkipVerify: t.cfg.InsecureSkipVerify, //nolint:gosec // 显式用户配置
	})
	if err := tlsConn.HandshakeContext(ctx); err != nil {
		return fmt.Errorf("TLS 握手失败: %w", err)
	}
	return tlsConn.Close()
}

func (t *transport) Send(ctx context.Context, payload []byte) messagechannel.Outcome {
	req, err := http.NewRequestWithContext(ctx, t.cfg.Method, t.cfg.URL, bytes.NewReader(payload))
	if err != nil {
		return messagechannel.Outcome{Kind: messagechannel.FailurePoison, Err: fmt.Errorf("构造请求失败: %w", err)}
	}

	for name, value := range t.headers {
		req.Header.Set(name, value)
	}
	req.Header.Set("Content-Type", t.cfg.ContentType)

	switch t.cfg.AuthType {
	case authBearer:
		req.Header.Set("Authorization", "Bearer "+t.cfg.BearerToken)
	case authBasic:
		req.SetBasicAuth(t.cfg.BasicUsername, t.cfg.BasicPassword)
	case authHMAC:
		mac := hmac.New(sha256.New, []byte(t.cfg.HMACSecret))
		_, _ = mac.Write(payload)
		req.Header.Set(t.cfg.HMACHeader, t.cfg.HMACPrefix+hex.EncodeToString(mac.Sum(nil)))
	}

	resp, err := t.client.Do(req)
	if err != nil {
		return messagechannel.Outcome{Kind: messagechannel.FailureRetry, Err: fmt.Errorf("请求失败: %w", err)}
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBodyBytes))
	snippet := strings.TrimSpace(string(body))
	if len(snippet) > 300 {
		snippet = snippet[:300] + "..."
	}

	switch {
	case resp.StatusCode >= 200 && resp.StatusCode < 300:
		return messagechannel.Outcome{Kind: messagechannel.FailureNone, StatusCode: resp.StatusCode}
	case resp.StatusCode == http.StatusRequestTimeout ||
		resp.StatusCode == http.StatusTooManyRequests ||
		resp.StatusCode >= 500:
		return messagechannel.Outcome{
			Kind:       messagechannel.FailureRetry,
			StatusCode: resp.StatusCode,
			Err:        fmt.Errorf("HTTP %d: %s", resp.StatusCode, snippet),
		}
	default:
		return messagechannel.Outcome{
			Kind:       messagechannel.FailurePoison,
			StatusCode: resp.StatusCode,
			Err:        fmt.Errorf("HTTP %d: %s", resp.StatusCode, snippet),
		}
	}
}
