// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package kafka

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/twmb/franz-go/pkg/kerr"
	"github.com/twmb/franz-go/pkg/kgo"
	"github.com/twmb/franz-go/pkg/sasl/plain"

	"iot-gateway/push/messagechannel"
)

// transport 实现 messagechannel.Transport（以及 KeyedTransport / io.Closer）。
//
// 客户端惰性创建：预览、未启用的通道不会起 franz-go 的后台协程与连接；
// Close 由 messagechannel.Stop 或临时通道的 Release 调用，重复调用安全。
type transport struct {
	cfg  *config
	opts []kgo.Opt

	mu     sync.Mutex
	client *kgo.Client
}

func newTransport(cfg *config) *transport {
	opts := []kgo.Opt{
		kgo.SeedBrokers(cfg.brokerAddrs...),
		kgo.ClientID(cfg.ClientID),
		kgo.RequiredAcks(requiredAcks(cfg.Acks)),
		kgo.ProduceRequestTimeout(time.Duration(cfg.TimeoutSeconds) * time.Second),
	}
	// franz-go 默认开启幂等写入且要求 acks=all；acks 更弱时必须显式关闭，
	// 否则客户端会拒绝非 all 的配置。
	if cfg.Acks != acksAll {
		opts = append(opts, kgo.DisableIdempotentWrite())
	}
	if cfg.AllowAutoTopicCreation {
		opts = append(opts, kgo.AllowAutoTopicCreation())
	}
	if cfg.TLSEnabled {
		opts = append(opts, kgo.DialTLSConfig(&tls.Config{
			MinVersion:         tls.VersionTLS12,
			InsecureSkipVerify: cfg.InsecureSkipVerify, //nolint:gosec // 显式用户配置
		}))
	}
	if cfg.AuthType == authPlain {
		opts = append(opts, kgo.SASL(plain.Auth{
			User: cfg.SASLUsername,
			Pass: cfg.SASLPassword,
		}.AsMechanism()))
	}
	return &transport{cfg: cfg, opts: opts}
}

func requiredAcks(acks string) kgo.Acks {
	switch acks {
	case acksNone:
		return kgo.NoAck()
	case acksLeader:
		return kgo.LeaderAck()
	default:
		return kgo.AllISRAcks()
	}
}

func (t *transport) getClient() (*kgo.Client, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.client != nil {
		return t.client, nil
	}
	cl, err := kgo.NewClient(t.opts...)
	if err != nil {
		return nil, err
	}
	t.client = cl
	return cl, nil
}

// Probe 只验证集群里有 broker 可达（broker-only Metadata 请求），
// 不校验 topic 是否存在、也不触发自动创建；真正的写入由 TestSend 验证。
func (t *transport) Probe(ctx context.Context) error {
	cl, err := t.getClient()
	if err != nil {
		return fmt.Errorf("创建 Kafka 客户端失败: %w", err)
	}
	if err := cl.Ping(ctx); err != nil {
		return fmt.Errorf("连接 Kafka 失败: %w", err)
	}
	return nil
}

// Send 实现 messagechannel.Transport（无消息键）。
func (t *transport) Send(ctx context.Context, payload []byte) messagechannel.Outcome {
	return t.SendKeyed(ctx, "", payload)
}

// SendKeyed 实现 messagechannel.KeyedTransport。key 为空时传 nil，
// 交给 franz-go 的默认分区器（sticky/均匀）选择分区。
func (t *transport) SendKeyed(ctx context.Context, key string, payload []byte) messagechannel.Outcome {
	cl, err := t.getClient()
	if err != nil {
		return messagechannel.Outcome{
			Kind: messagechannel.FailureRetry,
			Err:  fmt.Errorf("创建 Kafka 客户端失败: %w", err),
		}
	}

	rec := &kgo.Record{Topic: t.cfg.Topic, Value: payload}
	if key = strings.TrimSpace(key); key != "" {
		rec.Key = []byte(key)
	}
	err = cl.ProduceSync(ctx, rec).FirstErr()
	if err == nil {
		return messagechannel.Outcome{Kind: messagechannel.FailureNone}
	}
	return t.classify(err)
}

// Close 关闭并丢弃当前客户端；下一次 Send/Probe 会重新惰性建连。
func (t *transport) Close() error {
	t.mu.Lock()
	cl := t.client
	t.client = nil
	t.mu.Unlock()
	if cl != nil {
		cl.Close()
	}
	return nil
}

// classify 把 franz-go 的错误映射到 outbox 的补发语义。
//
// 只有「重试也不会成功」的错误才判毒批次；网络、超时、分区迁移等一律保留补发。
func (t *transport) classify(err error) messagechannel.Outcome {
	if err == nil {
		return messagechannel.Outcome{Kind: messagechannel.FailureNone}
	}
	var ke *kerr.Error
	if errors.As(err, &ke) {
		// topic 不存在且未开启自动创建：重试永远不会成功，避免缓存无限堆积。
		if ke.Code == kerr.UnknownTopicOrPartition.Code && !t.cfg.AllowAutoTopicCreation {
			return messagechannel.Outcome{
				Kind: messagechannel.FailurePoison,
				Err: fmt.Errorf("topic %q 不存在或未授权: %w；请先创建 topic 或开启自动创建",
					t.cfg.Topic, err),
			}
		}
		if ke.Retriable {
			return messagechannel.Outcome{Kind: messagechannel.FailureRetry, Err: err}
		}
		return messagechannel.Outcome{Kind: messagechannel.FailurePoison, Err: err}
	}
	return messagechannel.Outcome{Kind: messagechannel.FailureRetry, Err: err}
}
