// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

// opcuaprobe 用标准 OPC UA 客户端验证网关的 OPC UA Server 通道。
//
// 它只做三件 SCADA 会做的事：连上端点、浏览地址空间、读点位 / 订阅变化，
// 用来回答「网关到底有没有把点位供出去」。不依赖 UaExpert 等图形客户端，
// 便于在服务器、CI 或录屏环境里重复执行。
//
// 用法（默认连本机 4840，匿名 + SecurityPolicy None）：
//
//	go run ./cmd/opcuaprobe -mode connect
//	go run ./cmd/opcuaprobe -mode browse -root line1
//	go run ./cmd/opcuaprobe -mode read -root line1 -device "贴片机 SMT-01"
//	go run ./cmd/opcuaprobe -mode subscribe -root line1 -duration 12s
//
// 加密端点需要按服务端的证书与策略调整；本工具只覆盖内网匿名只读场景。
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/gopcua/opcua"
	"github.com/gopcua/opcua/id"
	"github.com/gopcua/opcua/ua"
)

const objectsFolder = 85

func main() {
	endpoint := flag.String("endpoint", "opc.tcp://127.0.0.1:4840", "OPC UA 端点")
	root := flag.String("root", "line1", "地址空间根节点名称（rootName）")
	device := flag.String("device", "", "只处理这台设备（空 = 全部）")
	mode := flag.String("mode", "browse", "connect | browse | read | subscribe")
	duration := flag.Duration("duration", 12*time.Second, "subscribe 模式的观察时长")
	interval := flag.Duration("interval", time.Second, "subscribe 模式的发布间隔")
	points := flag.Int("points", 6, "subscribe 模式最多监控的点位数")
	timeout := flag.Duration("timeout", 15*time.Second, "连接超时")
	flag.Parse()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	client, err := dial(ctx, *endpoint, *timeout)
	if err != nil {
		fmt.Fprintf(os.Stderr, "连接失败: %v\n", err)
		os.Exit(1)
	}
	defer func() { _ = client.Close(context.Background()) }()

	switch *mode {
	case "connect":
		if err := commandConnect(ctx, client, *endpoint, *root); err != nil {
			fail(err)
		}
	case "browse":
		if err := commandBrowse(ctx, client, *root, *device); err != nil {
			fail(err)
		}
	case "read":
		if err := commandRead(ctx, client, *root, *device); err != nil {
			fail(err)
		}
	case "subscribe":
		if err := commandSubscribe(ctx, client, *root, *device, *duration, *interval, *points); err != nil {
			fail(err)
		}
	default:
		fail(fmt.Errorf("未知 mode %q，可选 connect | browse | read | subscribe", *mode))
	}
}

func fail(err error) {
	fmt.Fprintf(os.Stderr, "失败: %v\n", err)
	os.Exit(1)
}

func stamp() string {
	return time.Now().Format("15:04:05.000")
}

func dial(ctx context.Context, endpoint string, timeout time.Duration) (*opcua.Client, error) {
	fmt.Printf("[%s] connect %s\n", stamp(), endpoint)
	fmt.Printf("[%s] security None · anonymous\n", stamp())

	connectCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	client, err := opcua.NewClient(endpoint,
		opcua.SecurityPolicy("None"),
		opcua.SecurityMode(ua.MessageSecurityModeNone),
		opcua.AuthAnonymous(),
		opcua.AutoReconnect(false),
	)
	if err != nil {
		return nil, err
	}
	if err := client.Connect(connectCtx); err != nil {
		return nil, err
	}
	fmt.Printf("[%s] session created\n", stamp())
	return client, nil
}

func commandConnect(ctx context.Context, client *opcua.Client, endpoint, root string) error {
	fmt.Printf("[%s] endpoint %s\n", stamp(), endpoint)

	namespaces, err := client.NamespaceArray(ctx)
	if err != nil {
		return fmt.Errorf("读 NamespaceArray: %w", err)
	}
	for i, ns := range namespaces {
		if i == 1 || strings.Contains(ns, "iot-gateway") {
			fmt.Printf("[%s] namespace ns=%d %s\n", stamp(), i, ns)
		}
	}

	summary, err := summarize(ctx, client, root)
	if err != nil {
		return err
	}
	fmt.Printf("[%s] root Objects / %s\n", stamp(), root)
	for i, d := range summary {
		mark := "├─"
		if i == len(summary)-1 {
			mark = "└─"
		}
		fmt.Printf("       %s %s (%d)\n", mark, d.name, d.points)
	}
	fmt.Printf("[%s] %d devices · %d points\n", stamp(), len(summary), countPoints(summary))
	fmt.Printf("[%s] ready\n", stamp())
	return nil
}

type deviceSummary struct {
	name   string
	points int
}

func summarize(ctx context.Context, client *opcua.Client, root string) ([]deviceSummary, error) {
	rootRef, err := findRoot(ctx, client, root)
	if err != nil {
		return nil, err
	}
	devices, err := children(ctx, client, rootRef.nodeID)
	if err != nil {
		return nil, err
	}
	out := make([]deviceSummary, 0, len(devices))
	for _, dev := range devices {
		points, err := children(ctx, client, dev.nodeID)
		if err != nil {
			return nil, err
		}
		out = append(out, deviceSummary{name: dev.displayName, points: len(points)})
	}
	return out, nil
}

func countPoints(list []deviceSummary) int {
	n := 0
	for _, d := range list {
		n += d.points
	}
	return n
}

// ref 是地址空间里的一次浏览结果，保留父节点用于输出树。
type ref struct {
	nodeID      *ua.NodeID
	browseName  string
	displayName string
	device      string // 所属设备（用于 read / subscribe 的展示）
}

func commandBrowse(ctx context.Context, client *opcua.Client, root, device string) error {
	rootRef, err := findRoot(ctx, client, root)
	if err != nil {
		return err
	}

	allDevices, err := children(ctx, client, rootRef.nodeID)
	if err != nil {
		return err
	}
	devices := allDevices
	if device != "" {
		devices = nil
		for _, d := range allDevices {
			if matchDevice(d.displayName, device) {
				devices = append(devices, d)
			}
		}
		if len(devices) == 0 {
			return fmt.Errorf("根节点 %s 下没有设备 %q", root, device)
		}
	}

	header := root
	if device != "" {
		header = root + " · device " + device
	}
	fmt.Printf("[%s] browse Objects / %s\n", stamp(), header)
	fmt.Printf("Objects\n└─ %s\n", rootRef.displayName)
	for i, dev := range devices {
		branch := "├─"
		if i == len(devices)-1 {
			branch = "└─"
		}
		fmt.Printf("   %s %s\n", branch, dev.displayName)
		points, err := children(ctx, client, dev.nodeID)
		if err != nil {
			return err
		}
		prefix := "   │  "
		if i == len(devices)-1 {
			prefix = "      "
		}
		for j, p := range points {
			mark := "├─"
			if j == len(points)-1 {
				mark = "└─"
			}
			fmt.Printf("%s%s %-8s %s\n", prefix, mark, p.browseName, p.displayName)
		}
	}
	summary, err := summarize(ctx, client, root)
	if err != nil {
		return err
	}
	fmt.Printf("[%s] %d devices · %d points in address space\n", stamp(), len(summary), countPoints(summary))
	return nil
}

func commandRead(ctx context.Context, client *opcua.Client, root, device string) error {
	targets, err := pointTargets(ctx, client, root, device)
	if err != nil {
		return err
	}
	if len(targets) == 0 {
		return fmt.Errorf("地址空间里没有找到点位（root=%s device=%q）", root, device)
	}

	lastDevice := ""
	for _, t := range targets {
		if t.device != lastDevice {
			fmt.Printf("[%s] read %s\n", stamp(), t.device)
			lastDevice = t.device
		}
		dv, err := readValue(ctx, client, t.nodeID)
		if err != nil {
			fmt.Printf("  %-8s %-12s  读取失败: %v\n", t.browseName, t.displayName, err)
			continue
		}
		fmt.Printf("  %-8s %-12s  %-8s %-5s %s\n",
			t.browseName, t.displayName, formatValue(dv), quality(dv.Status), dv.ServerTimestamp.Local().Format("15:04:05.000"))
	}
	return nil
}

func commandSubscribe(ctx context.Context, client *opcua.Client, root, device string, duration, interval time.Duration, limit int) error {
	targets, err := pointTargets(ctx, client, root, device)
	if err != nil {
		return err
	}
	if len(targets) == 0 {
		return fmt.Errorf("地址空间里没有找到点位（root=%s device=%q）", root, device)
	}
	if limit > 0 && len(targets) > limit {
		targets = targets[:limit]
	}

	notifyCh := make(chan *opcua.PublishNotificationData, 64)
	sub, err := client.Subscribe(ctx, &opcua.SubscriptionParameters{Interval: interval}, notifyCh)
	if err != nil {
		return fmt.Errorf("创建订阅: %w", err)
	}
	defer func() { _ = sub.Cancel(context.Background()) }()
	fmt.Printf("[%s] subscription %d created · publishing %s\n", stamp(), sub.SubscriptionID, interval)

	handleToPoint := make(map[uint32]ref, len(targets))
	requests := make([]*ua.MonitoredItemCreateRequest, 0, len(targets))
	for i, t := range targets {
		handle := uint32(i + 1)
		handleToPoint[handle] = t
		requests = append(requests, opcua.NewMonitoredItemCreateRequestWithDefaults(t.nodeID, ua.AttributeIDValue, handle))
	}
	res, err := sub.Monitor(ctx, ua.TimestampsToReturnBoth, requests...)
	if err != nil {
		return fmt.Errorf("创建监控项: %w", err)
	}
	ok := 0
	for _, r := range res.Results {
		if r.StatusCode == ua.StatusOK {
			ok++
		}
	}
	fmt.Printf("[%s] monitored items %d\n", stamp(), ok)

	deadline := time.NewTimer(duration)
	defer deadline.Stop()
	for {
		select {
		case <-deadline.C:
			fmt.Printf("[%s] done · %s\n", stamp(), duration)
			return nil
		case n := <-notifyCh:
			if n == nil {
				continue
			}
			if n.Error != nil {
				return n.Error
			}
			dcn, ok := n.Value.(*ua.DataChangeNotification)
			if !ok {
				continue
			}
			for _, item := range dcn.MonitoredItems {
				p := handleToPoint[item.ClientHandle]
				dv := item.Value
				if dv == nil || dv.Value == nil {
					continue
				}
				fmt.Printf("[%s] change %-8s %-12s = %-8s %-5s %s\n",
					stamp(), p.browseName, p.displayName, formatValue(dv), quality(dv.Status),
					dv.ServerTimestamp.Local().Format("15:04:05.000"))
			}
		}
	}
}

func findRoot(ctx context.Context, client *opcua.Client, name string) (ref, error) {
	refs, err := children(ctx, client, ua.NewNumericNodeID(0, objectsFolder))
	if err != nil {
		return ref{}, err
	}
	for _, r := range refs {
		if r.displayName == name || r.browseName == name {
			return r, nil
		}
	}
	names := make([]string, 0, len(refs))
	for _, r := range refs {
		names = append(names, r.displayName)
	}
	return ref{}, fmt.Errorf("Objects 下没有根节点 %q（实际: %s）", name, strings.Join(names, ", "))
}

func children(ctx context.Context, client *opcua.Client, parent *ua.NodeID) ([]ref, error) {
	refs, err := opcua.NewNode(parent, client).References(
		ctx, id.HierarchicalReferences, ua.BrowseDirectionForward, ua.NodeClassAll, true)
	if err != nil {
		return nil, err
	}
	out := make([]ref, 0, len(refs))
	for _, r := range refs {
		browse := ""
		if r.BrowseName != nil {
			browse = r.BrowseName.Name
		}
		display := browse
		if r.DisplayName != nil && r.DisplayName.Text != "" {
			display = r.DisplayName.Text
		}
		out = append(out, ref{nodeID: r.NodeID.NodeID, browseName: browse, displayName: display})
	}
	return out, nil
}

// pointTargets 展开 root → 设备 → 点位，给 read / subscribe 共用。
func pointTargets(ctx context.Context, client *opcua.Client, root, device string) ([]ref, error) {
	rootRef, err := findRoot(ctx, client, root)
	if err != nil {
		return nil, err
	}
	devices, err := children(ctx, client, rootRef.nodeID)
	if err != nil {
		return nil, err
	}

	var targets []ref
	for _, dev := range devices {
		if device != "" && !matchDevice(dev.displayName, device) {
			continue
		}
		points, err := children(ctx, client, dev.nodeID)
		if err != nil {
			return nil, err
		}
		for _, p := range points {
			p.device = dev.displayName
			targets = append(targets, p)
		}
	}
	return targets, nil
}

// matchDevice 支持完整名称或唯一子串（如 SMT），避免在终端里输入整串中文。
func matchDevice(name, filter string) bool {
	return name == filter || strings.Contains(name, filter)
}

func readValue(ctx context.Context, client *opcua.Client, nodeID *ua.NodeID) (*ua.DataValue, error) {
	resp, err := client.Read(ctx, &ua.ReadRequest{
		NodesToRead: []*ua.ReadValueID{{NodeID: nodeID, AttributeID: ua.AttributeIDValue}},
	})
	if err != nil {
		return nil, err
	}
	if len(resp.Results) != 1 {
		return nil, fmt.Errorf("Read 结果数 %d", len(resp.Results))
	}
	return resp.Results[0], nil
}

func formatValue(dv *ua.DataValue) string {
	if dv == nil || dv.Value == nil {
		return "—"
	}
	v := dv.Value.Value()
	switch x := v.(type) {
	case float64:
		return fmt.Sprintf("%.1f", x)
	case float32:
		return fmt.Sprintf("%.1f", x)
	default:
		return fmt.Sprint(v)
	}
}

func quality(s ua.StatusCode) string {
	switch {
	case s >= ua.StatusBad:
		return "Bad"
	case s >= ua.StatusUncertain:
		return "Uncertain"
	default:
		return "Good"
	}
}
