// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package opcuaserver

import (
	"context"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"net"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/gopcua/opcua/id"
	"github.com/gopcua/opcua/server"
	"github.com/gopcua/opcua/ua"
	"github.com/gopcua/opcua/uasc"

	"iot-gateway/push"
)

// serverState 一次地址空间构建 + 一个运行中的 gopcua server。
//
// 设备/点位结构变化时整棵重建（见 Channel.rebuild）：gopcua 的
// NodeNameSpace 没有删除节点的 API，而 AddRef 又不加锁，运行期往已有父节点
// 上挂子节点会与 Browse 并发读写同一 slice。重建代价是一次启动，换来的是
// 没有数据竞争、删除也生效。
type serverState struct {
	cfg    *config
	srv    *server.Server
	ns     *server.NodeNameSpace
	nsID   uint16
	root   *server.Node
	points map[string]*pointNode
	browse *browseIndex
	sig    string
	cancel context.CancelFunc

	closeOnce sync.Once
}

// newServerState 构建地址空间，但尚未 Start（尚未绑定端口）。
func newServerState(cfg *config, topo *topology) (*serverState, error) {
	srv, ns, nsID, root, err := newBaseServer(cfg)
	if err != nil {
		return nil, err
	}
	ns0, err := srv.Namespace(0)
	if err != nil {
		return nil, fmt.Errorf("opcua: 取 ns=0 失败: %w", err)
	}
	points, index := buildAddressSpace(cfg, ns, nsID, root, ns0, topo)

	// gopcua 的 TranslateBrowsePathsToNodeIDs 默认返回 BadServiceUnsupported，
	// 而 UaExpert 拖入点位时会用它计算 NODE PATH。必须在 Start 前覆盖。
	srv.RegisterHandler(id.TranslateBrowsePathsToNodeIDsRequest_Encoding_DefaultBinary,
		translateBrowsePathsHandler(index))

	return &serverState{
		cfg:    cfg,
		srv:    srv,
		ns:     ns,
		nsID:   nsID,
		root:   root,
		points: points,
		browse: index,
		sig:    topo.sig,
	}, nil
}

// newBaseServer 创建 gopcua server 与命名空间根节点。
func newBaseServer(cfg *config) (*server.Server, *server.NodeNameSpace, uint16, *server.Node, error) {
	opts := []server.Option{
		// 第一个端点同时是监听地址（0.0.0.0 表示所有网卡）。
		server.EndPoint(cfg.ListenHost, cfg.ListenPort),
		server.ServerName(cfg.RootName),
		server.ProductName("iot-gateway"),
		server.ManufacturerName("iot-gateway"),
		server.EnableAuthMode(ua.UserTokenTypeAnonymous),
	}
	// 同时登记本机所有可达地址。gopcua 的 CreateSession 按 EndpointURL
	// **精确匹配**服务端端点表：客户端用 192.168.x.x 连接、表里只有
	// 0.0.0.0 时，CreateSessionResponse.ServerEndpoints 为空，UaExpert 会在
	// ActivateSession 报 BadConfigurationError。多登记几份即可匹配。
	for _, host := range endpointHosts(cfg) {
		opts = append(opts, server.EndPoint(host, cfg.ListenPort))
	}

	if cfg.SecurityPolicy == securityNone {
		opts = append(opts, server.EnableSecurity(securityNone, ua.MessageSecurityModeNone))
	} else {
		cert, key, err := loadCertificate(cfg.CertFile, cfg.KeyFile)
		if err != nil {
			return nil, nil, 0, nil, err
		}
		mode, err := securityMode(cfg.SecurityMode)
		if err != nil {
			return nil, nil, 0, nil, err
		}
		opts = append(opts,
			server.Certificate(cert),
			server.PrivateKey(key),
			server.EnableSecurity(cfg.SecurityPolicy, mode),
		)
	}

	srv := server.New(opts...)
	ns := server.NewNodeNameSpace(srv, cfg.NamespaceURI)
	nsID := ns.ID()

	// 根节点必须挂到 ns=0 的标准 Objects 上，客户端默认浏览起点才看得到。
	// NewNodeNameSpace 自带的 Objects 是命名空间局部的（ns=1;i=85）。
	ns0, err := srv.Namespace(0)
	if err != nil {
		return nil, nil, 0, nil, fmt.Errorf("opcua: 取 ns=0 失败: %w", err)
	}
	objects := ns0.Node(ua.NewNumericNodeID(0, id.ObjectsFolder))
	if objects == nil {
		return nil, nil, 0, nil, fmt.Errorf("opcua: 标准 Objects 节点不存在")
	}

	root := newObjectNode(ua.NewStringNodeID(nsID, "root"), cfg.RootName, "")
	addTypeDefinition(root, ns0.Node(ua.NewNumericNodeID(0, id.FolderType)))
	ns.AddNode(root)
	objects.AddRef(root, server.RefTypeIDOrganizes, true)

	// 必须在 Start 之前注册：Start 里的 initHandlers 用 RegisterHandler
	// 补默认处理器，而 RegisterHandler 只在键不存在时写入。
	srv.RegisterHandler(id.GetEndpointsRequest_Encoding_DefaultBinary, getEndpointsHandler(srv))
	return srv, ns, nsID, root, nil
}

// endpointHosts 枚举本机可供客户端使用的地址，用于登记端点描述。
// 客户端实际用什么地址连进来无法在启动时得知；把常见地址都登记上，
// 才能让 UaExpert 在 CreateSessionResponse.ServerEndpoints 里找到匹配项。
func endpointHosts(cfg *config) []string {
	seen := make(map[string]struct{})
	out := make([]string, 0, 4)
	add := func(host string) {
		host = strings.TrimSpace(host)
		// 跳过 IPv6：server.EndPoint 的格式化不带方括号，会生成非法 URL。
		if host == "" || strings.Contains(host, ":") {
			return
		}
		// ListenHost 已经是第一个端点（监听地址），不再重复公布。
		if strings.EqualFold(host, cfg.ListenHost) {
			return
		}
		// 去重区分大小写：主机名的大小写变体需要分别登记。
		if _, ok := seen[host]; ok {
			return
		}
		seen[host] = struct{}{}
		out = append(out, host)
	}

	add("127.0.0.1")
	add("localhost")
	if name, err := os.Hostname(); err == nil {
		// CreateSession 的 URL 比较区分大小写，主机名的大小写变体都登记一份。
		add(name)
		add(strings.ToLower(name))
		add(strings.ToUpper(name))
	}
	if ifaces, err := net.Interfaces(); err == nil {
		for _, iface := range ifaces {
			if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
				continue
			}
			addrs, err := iface.Addrs()
			if err != nil {
				continue
			}
			for _, addr := range addrs {
				ipnet, ok := addr.(*net.IPNet)
				if !ok {
					continue
				}
				if ip4 := ipnet.IP.To4(); ip4 != nil {
					add(ip4.String())
				}
			}
		}
	}

	// 用户显式公布的地址（DNS 别名、NAT 公网 IP）：只登记端点描述，不参与绑定。
	for _, host := range cfg.AdvertiseHosts {
		add(host)
	}
	return out
}

func normalizeEndpointURL(u string) string {
	return strings.TrimSuffix(strings.ToLower(strings.TrimSpace(u)), "/")
}

// getEndpointsHandler 覆盖 gopcua 默认的 GetEndpoints。
//
// 默认实现按 EndpointURL **精确匹配**：监听 0.0.0.0 时端点 URL 是
// opc.tcp://0.0.0.0:4840，而客户端用实际 IP/主机名请求，匹配不到任何端点，
// 于是 UaExpert 之类会报「服务器不支持所配置的安全策略 '...#None'」。
//
// 这里返回全部端点，并把 EndpointURL 回写成客户端请求的地址 —— 它正是客户端
// 已经连通的那个地址，后续 CreateSession 也走这里。
func getEndpointsHandler(srv *server.Server) server.Handler {
	return func(_ *uasc.SecureChannel, r ua.Request, _ uint32) (ua.Response, error) {
		req, ok := r.(*ua.GetEndpointsRequest)
		if !ok {
			return nil, ua.StatusBadRequestTypeInvalid
		}
		var requestHandle uint32
		if req.RequestHeader != nil {
			requestHandle = req.RequestHeader.RequestHandle
		}

		all := srv.Endpoints()
		// 优先返回 URL 匹配的端点（正常情况下端点表里已经登记了客户端的地址）；
		// 一个都没匹配上时退回全部，并回写成客户端请求的 URL。
		want := normalizeEndpointURL(req.EndpointURL)
		matched := make([]*ua.EndpointDescription, 0, len(all))
		seen := make(map[string]struct{}, len(all))
		for _, ep := range all {
			if want != "" && normalizeEndpointURL(ep.EndpointURL) != want {
				continue
			}
			// 同一策略/模式的地址大小写变体只返回一份。
			key := fmt.Sprintf("%s|%d", ep.SecurityPolicyURI, ep.SecurityMode)
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			matched = append(matched, ep)
		}
		if len(matched) == 0 {
			matched = all
		}

		endpoints := make([]*ua.EndpointDescription, 0, len(matched))
		for _, ep := range matched {
			clone := *ep
			if req.EndpointURL != "" {
				clone.EndpointURL = req.EndpointURL
				// DiscoveryURLs 也会被部分客户端用于二次连接，一并改写成可达地址。
				if ep.Server != nil {
					serverDesc := *ep.Server
					serverDesc.DiscoveryURLs = []string{req.EndpointURL}
					clone.Server = &serverDesc
				}
			}
			endpoints = append(endpoints, &clone)
		}
		return &ua.GetEndpointsResponse{
			ResponseHeader: &ua.ResponseHeader{
				Timestamp:          time.Now(),
				RequestHandle:      requestHandle,
				ServiceResult:      ua.StatusOK,
				ServiceDiagnostics: &ua.DiagnosticInfo{},
				StringTable:        []string{},
			},
			Endpoints: endpoints,
		}, nil
	}
}

// start 绑定端口并开始服务。ctx 是通道生命周期的子 context，
// 取消它只影响本 state，不会波及下一次重建。
func (st *serverState) start(parent context.Context) error {
	ctx, cancel := context.WithCancel(parent)
	if err := st.srv.Start(ctx); err != nil {
		cancel()
		return err
	}
	st.cancel = cancel
	return nil
}

func (st *serverState) close() {
	st.closeOnce.Do(func() {
		if st.cancel != nil {
			st.cancel()
		}
		_ = st.srv.Close()
	})
}

// apply 把一批采集记录写入地址空间，并通知发生变化点位的订阅者。
//
// 值更新是无锁原子写；ChangeNotification 可能因慢客户端阻塞，所以本方法
// 只在通道自己的 apply goroutine 里调用，绝不占用采集协程。
func (st *serverState) apply(b push.PushBatch) (applied, dropped int) {
	if len(b.Records) == 0 {
		return 0, 0
	}
	at := parseCollectedAt(b.CollectedAt)
	changed := make([]*pointNode, 0, len(b.Records))
	seen := make(map[*pointNode]struct{}, len(b.Records))

	for i := range b.Records {
		r := &b.Records[i]
		p := st.points[pointKey(r.DeviceID, r.DeviceAddressID)]
		if p == nil {
			// 拓扑刚建还没包含这个点位（新建点位后的第一个采集周期）：
			// 丢弃本批，下一轮 reconcile 会把节点补上。
			dropped++
			continue
		}
		prev := p.val.Load()
		dv := buildDataValue(p.codec, r.Value, prev, r.Quality, at)
		p.val.Store(dv)
		applied++
		if sameDataValue(prev, dv) {
			continue
		}
		if _, ok := seen[p]; ok {
			continue
		}
		seen[p] = struct{}{}
		changed = append(changed, p)
	}

	for _, p := range changed {
		st.ns.ChangeNotification(p.node.ID())
	}
	return applied, dropped
}

// seedFrom 在重建时沿用旧树里同一点位、同一类型的最后一个值，
// 避免客户端在两次采集之间看到一整片 BadWaitingForInitialData。
func (st *serverState) seedFrom(old *serverState) {
	if old == nil {
		return
	}
	for key, oldPoint := range old.points {
		newPoint, ok := st.points[key]
		if !ok || newPoint.codec.typeID != oldPoint.codec.typeID {
			continue
		}
		if dv := oldPoint.val.Load(); dv != nil {
			newPoint.val.Store(dv)
		}
	}
}

func securityMode(s string) (ua.MessageSecurityMode, error) {
	switch s {
	case "None":
		return ua.MessageSecurityModeNone, nil
	case "Sign":
		return ua.MessageSecurityModeSign, nil
	case "SignAndEncrypt":
		return ua.MessageSecurityModeSignAndEncrypt, nil
	}
	return 0, fmt.Errorf("opcua: 不支持的安全模式 %q", s)
}

// loadCertificate 读取 PEM（或 DER）证书与私钥。
// gopcua 的 server.Certificate 期望 DER 编码，所以这里统一解码。
func loadCertificate(certFile, keyFile string) ([]byte, *rsa.PrivateKey, error) {
	certRaw, err := os.ReadFile(certFile)
	if err != nil {
		return nil, nil, fmt.Errorf("opcua: 读取证书失败: %w", err)
	}
	certDER := certRaw
	if block, _ := pem.Decode(certRaw); block != nil {
		certDER = block.Bytes
	}
	if _, err := x509.ParseCertificate(certDER); err != nil {
		return nil, nil, fmt.Errorf("opcua: 证书解析失败: %w", err)
	}

	keyRaw, err := os.ReadFile(keyFile)
	if err != nil {
		return nil, nil, fmt.Errorf("opcua: 读取私钥失败: %w", err)
	}
	keyDER := keyRaw
	if block, _ := pem.Decode(keyRaw); block != nil {
		keyDER = block.Bytes
	}

	var key any
	if parsed, err := x509.ParsePKCS8PrivateKey(keyDER); err == nil {
		key = parsed
	} else if parsed, err := x509.ParsePKCS1PrivateKey(keyDER); err == nil {
		key = parsed
	} else {
		return nil, nil, fmt.Errorf("opcua: 私钥解析失败（仅支持 PKCS#8 / PKCS#1）")
	}
	rsaKey, ok := key.(*rsa.PrivateKey)
	if !ok {
		return nil, nil, fmt.Errorf("opcua: 私钥必须是 RSA")
	}
	return certDER, rsaKey, nil
}
