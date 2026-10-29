// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package subscribe

import "testing"

func TestManagerRecentMessagesNilManager(t *testing.T) {
	var m *Manager
	msgs, ok := m.RecentMessages("any")
	if ok || msgs != nil {
		t.Fatalf("nil manager = (%v,%v), want (nil,false)", msgs, ok)
	}
}

func TestManagerRecentMessagesUnknownID(t *testing.T) {
	m := NewManager(nil, nil)
	msgs, ok := m.RecentMessages("missing")
	if ok || msgs != nil {
		t.Fatalf("unknown id = (%v,%v), want (nil,false)", msgs, ok)
	}
}

func TestManagerRecentMessagesActive(t *testing.T) {
	sub, _, _ := newTestSubscriber(t, `{
		"fields": {
			"deviceId":        {"source": "path", "value": "deviceId"},
			"deviceAddressId": {"source": "path", "value": "tagId"},
			"value":           {"source": "path", "value": "value"}
		}
	}`)
	feed(t, sub, fakeMessage{topic: "factory/a", payload: []byte(`{"deviceId":"d1","tagId":"a1","value":1}`)})

	// 单协程测试：直接注入管理器持有的订阅实例，不需要加锁。
	m := NewManager(nil, nil)
	m.subs[sub.cfg.id] = sub

	msgs, ok := m.RecentMessages(sub.cfg.id)
	if !ok {
		t.Fatal("active subscription should be found")
	}
	if len(msgs) != 1 || msgs[0].Topic != "factory/a" {
		t.Fatalf("recent messages = %+v", msgs)
	}
}
