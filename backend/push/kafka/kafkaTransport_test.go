// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package kafka

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"fmt"
	"io"
	"math/big"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/twmb/franz-go/pkg/kerr"
	"github.com/twmb/franz-go/pkg/kfake"
	"github.com/twmb/franz-go/pkg/kgo"

	"iot-gateway/push/messagechannel"
)

const testTopic = "iot-data"

func newKfakeCluster(t *testing.T, opts ...kfake.Opt) []string {
	t.Helper()
	base := []kfake.Opt{
		kfake.NumBrokers(1),
		kfake.SeedTopics(4, testTopic),
	}
	c, err := kfake.NewCluster(append(base, opts...)...)
	if err != nil {
		t.Fatalf("start kfake: %v", err)
	}
	t.Cleanup(c.Close)
	return c.ListenAddrs()
}

func cfgJSON(addrs []string, extra string) string {
	base := fmt.Sprintf(`{"brokers":%q,"topic":%q`, strings.Join(addrs, ","), testTopic)
	if extra != "" {
		base += "," + extra
	}
	return base + "}"
}

func newTestTransport(t *testing.T, raw string) *transport {
	t.Helper()
	cfg, err := parseConfig(raw)
	if err != nil {
		t.Fatalf("parseConfig: %v", err)
	}
	tr := newTransport(cfg)
	t.Cleanup(func() { _ = tr.Close() })
	return tr
}

func consumeN(t *testing.T, addrs []string, n int) []*kgo.Record {
	t.Helper()
	cl, err := kgo.NewClient(
		kgo.SeedBrokers(addrs...),
		kgo.ConsumeTopics(testTopic),
		kgo.ConsumeResetOffset(kgo.NewOffset().AtStart()),
	)
	if err != nil {
		t.Fatalf("new consumer: %v", err)
	}
	t.Cleanup(cl.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	var out []*kgo.Record
	for len(out) < n {
		fs := cl.PollFetches(ctx)
		if err := fs.Err(); err != nil {
			t.Fatalf("consume: %v", err)
		}
		out = append(out, fs.Records()...)
	}
	return out
}

func testCtx(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func TestTransportProbeAndSend(t *testing.T) {
	addrs := newKfakeCluster(t)
	tr := newTestTransport(t, cfgJSON(addrs, ""))
	ctx := testCtx(t)

	if err := tr.Probe(ctx); err != nil {
		t.Fatalf("probe: %v", err)
	}
	out := tr.Send(ctx, []byte("hello"))
	if out.Kind != messagechannel.FailureNone {
		t.Fatalf("send: kind=%v err=%v", out.Kind, out.Err)
	}

	recs := consumeN(t, addrs, 1)
	if got := string(recs[0].Value); got != "hello" {
		t.Errorf("value = %q, want hello", got)
	}
	if recs[0].Key != nil {
		t.Errorf("key = %q, want nil", recs[0].Key)
	}
}

func TestTransportSendKeyedKeepsPartition(t *testing.T) {
	addrs := newKfakeCluster(t)
	tr := newTestTransport(t, cfgJSON(addrs, ""))
	ctx := testCtx(t)

	for _, payload := range []string{"first", "second"} {
		out := tr.SendKeyed(ctx, "dev-001", []byte(payload))
		if out.Kind != messagechannel.FailureNone {
			t.Fatalf("send %s: kind=%v err=%v", payload, out.Kind, out.Err)
		}
	}

	recs := consumeN(t, addrs, 2)
	for _, rec := range recs {
		if got := string(rec.Key); got != "dev-001" {
			t.Errorf("key = %q, want dev-001", got)
		}
	}
	if recs[0].Partition != recs[1].Partition {
		t.Errorf("same key landed on partitions %d and %d", recs[0].Partition, recs[1].Partition)
	}
}

func TestTransportSASLPlain(t *testing.T) {
	addrs := newKfakeCluster(t,
		kfake.EnableSASL(),
		kfake.User("PLAIN", "gateway", "secret"),
	)
	tr := newTestTransport(t, cfgJSON(addrs,
		`"authType":"plain","saslUsername":"gateway","saslPassword":"secret"`))
	ctx := testCtx(t)

	if err := tr.Probe(ctx); err != nil {
		t.Fatalf("probe with SASL/PLAIN: %v", err)
	}
	out := tr.Send(ctx, []byte("with-sasl"))
	if out.Kind != messagechannel.FailureNone {
		t.Fatalf("send with SASL/PLAIN: kind=%v err=%v", out.Kind, out.Err)
	}
}

func TestTransportTLS(t *testing.T) {
	serverTLS := newServerTLSConfig(t)
	addrs := newKfakeCluster(t, kfake.TLS(serverTLS))
	tr := newTestTransport(t, cfgJSON(addrs, `"tlsEnabled":true,"insecureSkipVerify":true`))
	ctx := testCtx(t)

	if err := tr.Probe(ctx); err != nil {
		t.Fatalf("probe over TLS: %v", err)
	}
	out := tr.Send(ctx, []byte("with-tls"))
	if out.Kind != messagechannel.FailureNone {
		t.Fatalf("send over TLS: kind=%v err=%v", out.Kind, out.Err)
	}
}

func TestTransportProbeClosedPort(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()

	tr := newTestTransport(t, cfgJSON([]string{addr}, ""))
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := tr.Probe(ctx); err == nil {
		t.Fatal("probe to a closed port should fail")
	}
}

func TestTransportLazyClientAndIdempotentClose(t *testing.T) {
	tr := newTestTransport(t, cfgJSON([]string{"127.0.0.1:9092"}, ""))
	if tr.client != nil {
		t.Fatal("client should be created lazily")
	}
	if err := tr.Close(); err != nil {
		t.Fatalf("first close: %v", err)
	}
	if err := tr.Close(); err != nil {
		t.Fatalf("second close: %v", err)
	}
}

func TestClassify(t *testing.T) {
	tr := newTestTransport(t, cfgJSON([]string{"127.0.0.1:1"}, ""))
	cases := []struct {
		name       string
		err        error
		autoCreate bool
		want       messagechannel.FailureKind
	}{
		{"success", nil, false, messagechannel.FailureNone},
		{"unknown topic without auto-create", kerr.UnknownTopicOrPartition, false, messagechannel.FailurePoison},
		{"unknown topic with auto-create", kerr.UnknownTopicOrPartition, true, messagechannel.FailureRetry},
		{"topic authorization", kerr.TopicAuthorizationFailed, false, messagechannel.FailurePoison},
		{"invalid topic", kerr.InvalidTopicException, false, messagechannel.FailurePoison},
		{"message too large", kerr.MessageTooLarge, false, messagechannel.FailurePoison},
		{"leader not available", kerr.LeaderNotAvailable, false, messagechannel.FailureRetry},
		{"network exception", kerr.NetworkException, false, messagechannel.FailureRetry},
		{"eof", io.EOF, false, messagechannel.FailureRetry},
		{"deadline exceeded", context.DeadlineExceeded, false, messagechannel.FailureRetry},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tr.cfg.AllowAutoTopicCreation = tc.autoCreate
			if got := tr.classify(tc.err).Kind; got != tc.want {
				t.Errorf("classify(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

// newServerTLSConfig 生成一张仅用于测试的自签名证书。
func newServerTLSConfig(t *testing.T) *tls.Config {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "127.0.0.1"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IsCA:                  true,
		IPAddresses:           []net.IP{net.ParseIP("127.0.0.1")},
		DNSNames:              []string{"localhost"},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create certificate: %v", err)
	}
	return &tls.Config{
		MinVersion:   tls.VersionTLS12,
		Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}},
	}
}
