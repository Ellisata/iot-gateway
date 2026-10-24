// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package webhook

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"iot-gateway/push/messagechannel"
)

type capturedRequest struct {
	method        string
	body          string
	contentType   string
	authorization string
	header        http.Header
}

func TestSendSuccessAndHeaders(t *testing.T) {
	var got capturedRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		got = capturedRequest{
			method:        r.Method,
			body:          string(b),
			contentType:   r.Header.Get("Content-Type"),
			authorization: r.Header.Get("Authorization"),
			header:        r.Header.Clone(),
		}
		w.WriteHeader(200)
	}))
	defer server.Close()

	cfg, err := parseConfig(`{"url":"` + server.URL + `","headersJson":"{\"X-Env\":\"prod\"}"}`)
	if err != nil {
		t.Fatalf("parseConfig: %v", err)
	}
	out := newTransport(cfg).Send(context.Background(), []byte(`{"v":1}`))
	if out.Kind != messagechannel.FailureNone || out.StatusCode != 200 {
		t.Fatalf("outcome = %+v", out)
	}
	if got.method != "POST" || got.body != `{"v":1}` {
		t.Errorf("request = %+v", got)
	}
	if got.contentType != defaultContentType || got.header.Get("X-Env") != "prod" {
		t.Errorf("headers = %+v", got.header)
	}
}

func TestSendAuth(t *testing.T) {
	var auth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		w.WriteHeader(200)
	}))
	defer server.Close()

	bearerCfg, _ := parseConfig(`{"url":"` + server.URL + `","authType":"bearer","bearerToken":"tok"}`)
	newTransport(bearerCfg).Send(context.Background(), []byte(`{}`))
	if auth != "Bearer tok" {
		t.Errorf("bearer auth = %q", auth)
	}

	basicCfg, _ := parseConfig(`{"url":"` + server.URL + `","authType":"basic","basicUsername":"u","basicPassword":"p"}`)
	newTransport(basicCfg).Send(context.Background(), []byte(`{}`))
	if !strings.HasPrefix(auth, "Basic ") {
		t.Errorf("basic auth = %q", auth)
	}
}

func TestSendHMAC(t *testing.T) {
	var sig string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sig = r.Header.Get("X-Signature")
		w.WriteHeader(200)
	}))
	defer server.Close()

	cfg, _ := parseConfig(`{"url":"` + server.URL + `","authType":"hmac-sha256","hmacSecret":"secret"}`)
	payload := []byte(`{"v":1}`)
	out := newTransport(cfg).Send(context.Background(), payload)
	if out.Kind != messagechannel.FailureNone {
		t.Fatalf("outcome = %+v", out)
	}
	mac := hmac.New(sha256.New, []byte("secret"))
	_, _ = mac.Write(payload)
	want := "sha256=" + hex.EncodeToString(mac.Sum(nil))
	if sig != want {
		t.Errorf("signature = %q, want %q", sig, want)
	}
}

func TestSendClassification(t *testing.T) {
	cases := []struct {
		status int
		want   messagechannel.FailureKind
	}{
		{200, messagechannel.FailureNone},
		{204, messagechannel.FailureNone},
		{400, messagechannel.FailurePoison},
		{401, messagechannel.FailurePoison},
		{403, messagechannel.FailurePoison},
		{404, messagechannel.FailurePoison},
		{302, messagechannel.FailurePoison},
		{408, messagechannel.FailureRetry},
		{429, messagechannel.FailureRetry},
		{500, messagechannel.FailureRetry},
		{503, messagechannel.FailureRetry},
	}
	for _, tc := range cases {
		t.Run(http.StatusText(tc.status), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if tc.status == 302 {
					w.Header().Set("Location", "/other")
				}
				w.WriteHeader(tc.status)
			}))
			defer server.Close()

			cfg, _ := parseConfig(`{"url":"` + server.URL + `"}`)
			out := newTransport(cfg).Send(context.Background(), []byte(`{}`))
			if out.Kind != tc.want || out.StatusCode != tc.status {
				t.Fatalf("status %d: outcome = %+v, want kind %d", tc.status, out, tc.want)
			}
		})
	}
}

func TestSendNetworkErrorIsRetry(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	url := server.URL
	server.Close()

	cfg, _ := parseConfig(`{"url":"` + url + `"}`)
	out := newTransport(cfg).Send(context.Background(), []byte(`{}`))
	if out.Kind != messagechannel.FailureRetry {
		t.Fatalf("outcome = %+v, want retry", out)
	}
}

func TestProbe(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	cfg, _ := parseConfig(`{"url":"` + server.URL + `"}`)
	if err := newTransport(cfg).Probe(context.Background()); err != nil {
		t.Fatalf("Probe live server: %v", err)
	}
	server.Close()
	if err := newTransport(cfg).Probe(context.Background()); err == nil {
		t.Fatal("Probe closed server should fail")
	}
}

func TestProbeTLS(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer server.Close()

	cfg, _ := parseConfig(`{"url":"` + server.URL + `","insecureSkipVerify":true}`)
	if err := newTransport(cfg).Probe(context.Background()); err != nil {
		t.Fatalf("Probe with insecureSkipVerify: %v", err)
	}

	strict, _ := parseConfig(`{"url":"` + server.URL + `"}`)
	if err := newTransport(strict).Probe(context.Background()); err == nil {
		t.Fatal("Probe self-signed server without skip verify should fail")
	}
}
