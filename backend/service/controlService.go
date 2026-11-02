// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"gorm.io/gorm"

	"github.com/Ellisata/iot-gateway-sdk/driver"

	"iot-gateway/appError"
	"iot-gateway/collector"
	"iot-gateway/configFile"
	"iot-gateway/enums"
	"iot-gateway/logger"
	"iot-gateway/model/dto"
	"iot-gateway/model/po"
	"iot-gateway/model/vo"
	"iot-gateway/response"
	"iot-gateway/runtimecfg"
)

// controlVerifyDelay 写后回读前的静置时间。
//
// 部分 PLC 的写应答先于内部数据区更新完成；立即回读可能读到旧值，造成
// "写成功但回读不一致"的假告警。50ms 对控制动作的可接受性没有影响。
const controlVerifyDelay = 50 * time.Millisecond

// ControlRunner 控制服务对采集引擎的最小依赖。
//
// 抽成接口不是为了多一层抽象，而是为了让授权/幂等/审计这些**网关侧逻辑**
// 能在不启动真实插件与 PLC 的情况下单测；生产实现是 *collector.Engine。
type ControlRunner interface {
	Write(ctx context.Context, deviceID string, reqs []driver.WriteRequest) ([]driver.WriteResult, error)
	ReadPoints(ctx context.Context, deviceID string, addrs []po.DeviceAddress) ([]driver.ReadResult, error)
}

// WritableSource 协议可写能力的来源（生产实现是 *pluginhost.Manager）。
type WritableSource interface {
	ProtocolWritable(protocol string) bool
}

// ControlService 控制下发服务。
//
// 职责边界：这里只做**网关侧**的授权与编排（总开关、设备/点位归属、点位
// rw_permission 与物模型属性 access_mode、协议能力、幂等、审计、可选回读）；
// 值解析与协议编码全部在插件进程内完成，网关不碰 PLC 字节。
//
// 写权限是**点位与属性的交集**：rw_permission 是设备物理能力，access_mode 是
// 业务语义上界；两者都含 W 才允许写入。绑定校验只保证前者覆盖后者，因此
// 「RW 点位 + R 属性」是合法绑定，但写入要在这里拒绝。
type ControlService struct {
	db      *gorm.DB
	engine  ControlRunner
	plugins WritableSource
	cfg     *configFile.Config
	policy  runtimecfg.Reader
	limiter *controlRateLimiter

	// inflight 收敛**并发**相同 requestId：第一个请求执行，其余等待其结束后
	// 回放审计结果。DB 查重只能挡住"已经完成"的重复请求，挡不住同时到达的。
	inflightMu sync.Mutex
	inflight   map[string]chan struct{}
}

// NewControlService 创建控制下发服务。
//
// policy 是运行期开关来源（control.enabled 是热读项）；为 nil 时回退到启动期
// 配置，便于不启动 runtimecfg 的单元测试。
func NewControlService(db *gorm.DB, engine ControlRunner, plugins WritableSource,
	cfg *configFile.Config, policy runtimecfg.Reader) *ControlService {
	return &ControlService{
		db:       db,
		engine:   engine,
		plugins:  plugins,
		cfg:      cfg,
		policy:   policy,
		limiter:  newControlRateLimiter(cfg.Control.MaxWritesPerMinute, cfg.Control.MaxWritesPerPointPerMinute),
		inflight: make(map[string]chan struct{}),
	}
}

// controlEnabled 返回控制下发总开关的**当前**值。
//
// 热读：页面 / API 修改后下一个请求即生效，不需要重启。所有写入入口
// （Web、OpenAPI、MQTT 控制主题）都经过本服务，因此这里是唯一的总闸。
func (s *ControlService) controlEnabled() bool {
	if s.policy != nil {
		return s.policy.Bool(runtimecfg.KeyControlEnabled)
	}
	return s.cfg.Control.Enabled
}

// Capability 查询一台设备是否具备控制下发条件。
//
// reason 是机器可读的原因码，供前端映射文案：
//
//	disabled             control.enabled 未打开
//	device_disabled      设备已停用
//	protocol_not_writable 协议插件没有声明 Writable
//	protocol_missing     设备引用的协议行不存在
func (s *ControlService) Capability(ctx context.Context, deviceID string) (*vo.ControlCapabilityVO, error) {
	dev, err := s.loadDevice(ctx, deviceID)
	if err != nil {
		return nil, err
	}
	protocolName := s.protocolName(ctx, dev.ProtocolID)

	enabled := s.controlEnabled()
	out := &vo.ControlCapabilityVO{
		DeviceID:     dev.ID,
		DeviceName:   dev.Name,
		ProtocolName: protocolName,
		Enabled:      enabled,
	}
	switch {
	case !enabled:
		out.Reason = "disabled"
	case dev.Status != 1:
		out.Reason = "device_disabled"
	case protocolName == "":
		out.Reason = "protocol_missing"
	case !s.plugins.ProtocolWritable(protocolName):
		out.Reason = "protocol_not_writable"
	default:
		out.Writable = true
	}
	return out, nil
}

// PageLogs 分页查询控制下发审计。
//
// 时间过滤用字符串比较：control_log.created_at 由本进程以
// "2006-01-02 15:04:05" 本地时间写入，同格式字符串的字典序即时间序。
// 前端日期选择器应把边界格式化成同一格式（含闭区间）。
func (s *ControlService) PageLogs(ctx context.Context, req *dto.PageControlLogDTO) (*response.PageVo[vo.ControlLogVO], error) {
	if req == nil || req.Page < 1 || req.Size < 1 {
		return nil, controlErrMsg(enums.ControlParamInvalidEnum, "分页参数不合法")
	}
	for _, v := range []struct{ name, value string }{
		{"startTime", req.StartTime},
		{"endTime", req.EndTime},
	} {
		if v.value == "" {
			continue
		}
		if _, err := time.ParseInLocation("2006-01-02 15:04:05", v.value, time.Local); err != nil {
			return nil, controlErrMsg(enums.ControlParamInvalidEnum,
				fmt.Sprintf("%s 格式应为 YYYY-MM-DD HH:mm:ss", v.name))
		}
	}

	db := s.db.WithContext(ctx).Model(&po.ControlLog{})
	if req.DeviceID != "" {
		db = db.Where("device_id = ?", req.DeviceID)
	}
	if req.AddressID != "" {
		db = db.Where("address_id = ?", req.AddressID)
	}
	if req.Source != "" {
		db = db.Where("source = ?", req.Source)
	}
	if req.RequestID != "" {
		db = db.Where("request_id = ?", req.RequestID)
	}
	if req.Operator != "" {
		db = db.Where("operator LIKE ?", "%"+req.Operator+"%")
	}
	if req.Name != "" {
		like := "%" + req.Name + "%"
		db = db.Where("(device_name LIKE ? OR address_name LIKE ? OR address_label LIKE ?)",
			like, like, like)
	}
	if req.StartTime != "" {
		db = db.Where("created_at >= ?", req.StartTime)
	}
	if req.EndTime != "" {
		db = db.Where("created_at <= ?", req.EndTime)
	}
	if req.Code != nil {
		db = db.Where("code = ?", *req.Code)
	}
	if req.Verified != nil {
		db = db.Where("verified = ?", *req.Verified)
	}

	var total int64
	if err := db.Count(&total).Error; err != nil {
		return nil, err
	}
	var rows []po.ControlLog
	if err := db.Order("created_at DESC, id DESC").
		Offset((req.Page - 1) * req.Size).
		Limit(req.Size).
		Find(&rows).Error; err != nil {
		return nil, err
	}

	records := make([]vo.ControlLogVO, len(rows))
	for i, r := range rows {
		records[i] = vo.ControlLogVO{
			ID:            r.ID,
			RequestID:     r.RequestID,
			Source:        r.Source,
			Operator:      r.Operator,
			DeviceID:      r.DeviceID,
			DeviceName:    r.DeviceName,
			ProtocolName:  r.ProtocolName,
			AddressID:     r.AddressID,
			AddressName:   r.AddressName,
			AddressLabel:  r.AddressLabel,
			DataType:      r.DataType,
			RwPermission:  r.RwPermission,
			RequestValue:  r.RequestValue,
			ResultValue:   r.ResultValue,
			ReadbackValue: r.ReadbackValue,
			Verified:      r.Verified == 1,
			Code:          r.Code,
			Message:       r.Message,
			CreatedAt:     r.CreatedAt,
		}
	}
	return &response.PageVo[vo.ControlLogVO]{
		Page:    req.Page,
		Size:    req.Size,
		Total:   total,
		Records: records,
	}, nil
}

// Write 执行一次控制下发。
//
// 批量写**不是事务**：每个点的结果独立返回。写失败时整批的状态可能是未知的
// （见 collector.Write 与 driver.Writer 的契约），调用方应使用 verify 回读确认。
func (s *ControlService) Write(ctx context.Context, req *dto.ControlDTO, source, operator string) (*vo.ControlResultVO, error) {
	if !s.controlEnabled() {
		return nil, controlErr(enums.ControlDisabledEnum)
	}
	if req == nil || len(req.Items) == 0 {
		return nil, controlErrMsg(enums.ControlParamInvalidEnum, "下发点位不能为空")
	}
	if len(req.Items) > 50 {
		return nil, controlErrMsg(enums.ControlParamInvalidEnum, "单次下发点位不能超过 50 个")
	}
	requestID := strings.TrimSpace(req.RequestID)
	if len(requestID) > 128 {
		return nil, controlErrMsg(enums.ControlParamInvalidEnum, "requestId 长度不能超过 128")
	}
	if source == "" {
		source = "web"
	}

	// 幂等：同一 requestId 直接返回首次结果，不重复下发。
	// 先做**进程内并发收敛**：第一个请求执行，其余等待它结束后回放审计。
	// DB 查重只能挡住"已经完成"的重复；这条路径挡住同时到达的重复。
	release, prev, err := s.beginRequest(requestID, ctx)
	if err != nil {
		return nil, err
	}
	if prev != nil {
		return prev, nil
	}
	if release != nil {
		defer release()
	}

	if requestID != "" {
		logs, err := s.loadRequestLogs(ctx, requestID)
		if err != nil {
			return nil, err
		}
		if len(logs) > 0 {
			if logs[0].DeviceID != req.DeviceID {
				return nil, controlErrMsg(enums.ControlParamInvalidEnum, "requestId 已被另一台设备使用")
			}
			return resultFromLogs(logs), nil
		}
	}

	dev, err := s.loadDevice(ctx, req.DeviceID)
	if err != nil {
		return nil, err
	}
	if dev.Status != 1 {
		return nil, controlErrMsg(enums.ControlDeviceOfflineEnum, "设备已停用")
	}
	protocolName := s.protocolName(ctx, dev.ProtocolID)
	if protocolName == "" {
		return nil, controlErrMsg(enums.ControlProtocolNotWritableEnum, "设备引用的协议不存在")
	}
	if !s.plugins.ProtocolWritable(protocolName) {
		return nil, controlErr(enums.ControlProtocolNotWritableEnum)
	}

	addrByID, ordered, err := s.validateItems(ctx, dev.ID, req.Items)
	if err != nil {
		return nil, err
	}

	// 限流放在校验之后、真正下发之前：只对合法点位扣配额，且整批原子检查。
	addressIDs := make([]string, len(ordered))
	for i := range ordered {
		addressIDs[i] = ordered[i].ID
	}
	if ok, reason := s.limiter.allow(dev.ID, addressIDs, time.Now()); !ok {
		logger.Warn("control: rate limited device=%s request=%s: %s", dev.ID, requestID, reason)
		return nil, controlErrMsg(enums.ControlRateLimitedEnum, reason)
	}

	valueByID := make(map[string]string, len(req.Items))
	for _, item := range req.Items {
		valueByID[strings.TrimSpace(item.DeviceAddressID)] = item.Value
	}

	writes := make([]driver.WriteRequest, 0, len(ordered))
	for _, a := range ordered {
		writes = append(writes, driver.WriteRequest{
			DeviceAddressID: a.ID,
			Name:            a.Name,
			DataType:        a.DataType,
			Value:           valueByID[a.ID],
		})
	}

	results, writeErr := s.engine.Write(ctx, dev.ID, writes)
	resultByID := make(map[string]driver.WriteResult, len(results))
	for _, r := range results {
		resultByID[r.DeviceAddressID] = r
	}

	out := &vo.ControlResultVO{
		RequestID:    requestID,
		DeviceID:     dev.ID,
		DeviceName:   dev.Name,
		ProtocolName: protocolName,
		Results:      make([]vo.ControlItemResultVO, 0, len(ordered)),
	}
	for _, a := range ordered {
		r, ok := resultByID[a.ID]
		item := vo.ControlItemResultVO{
			AddressID:    a.ID,
			AddressName:  a.Name,
			AddressLabel: a.Label,
			DataType:     a.DataType,
			RwPermission: a.RwPermission,
			Value:        valueByID[a.ID],
		}
		if ok {
			item.ResultValue = r.Value
			item.OK = r.OK()
			item.Code = r.Code
			item.Message = r.Message
		} else {
			// 驱动在链路失败时可能只返回已处理的部分点。
			item.OK = false
			item.Code = driver.WriteCodeTransport
			if writeErr != nil {
				item.Message = "写入状态未知：" + writeErr.Error()
			} else {
				item.Message = "驱动未返回该点的结果"
			}
		}
		out.Results = append(out.Results, item)
	}

	// 可选回读：只对写成功且有读权限的点做。回读失败不改变写结果，
	// 只把 verified 留在 false。
	if req.Verify {
		s.verify(ctx, dev.ID, out.Results, ordered)
	}

	s.persistAudit(source, operator, dev, protocolName, requestID, out.Results, addrByID)

	if writeErr != nil {
		return nil, mapCollectorErr(writeErr)
	}
	return out, nil
}

// verify 用同一活会话回读写入点，把结果填进 VO。
func (s *ControlService) verify(ctx context.Context, deviceID string, results []vo.ControlItemResultVO, addrs []po.DeviceAddress) {
	addrByID := make(map[string]po.DeviceAddress, len(addrs))
	for _, a := range addrs {
		addrByID[a.ID] = a
	}
	readable := make([]po.DeviceAddress, 0, len(results))
	for _, r := range results {
		if !r.OK {
			continue
		}
		a := addrByID[r.AddressID]
		if strings.Contains(a.RwPermission, "R") {
			readable = append(readable, a)
		}
	}
	if len(readable) == 0 {
		return
	}

	select {
	case <-time.After(controlVerifyDelay):
	case <-ctx.Done():
		return
	}

	readResults, err := s.engine.ReadPoints(ctx, deviceID, readable)
	if err != nil {
		logger.Warn("control: verify readback for device %s failed: %v", deviceID, err)
		return
	}
	readByID := make(map[string]driver.ReadResult, len(readResults))
	for _, r := range readResults {
		readByID[r.DeviceAddressID] = r
	}

	for i := range results {
		r, ok := readByID[results[i].AddressID]
		if !ok {
			continue
		}
		results[i].ReadbackValue = r.Value
		expected := results[i].ResultValue
		if expected == "" {
			expected = results[i].Value
		}
		if r.Quality == driver.QualityGood && strings.TrimSpace(r.Value) == strings.TrimSpace(expected) {
			results[i].Verified = true
		} else if results[i].Message == "" {
			results[i].Message = fmt.Sprintf("回读值 %q 与写入值 %q 不一致", r.Value, expected)
		}
	}
}

// validateItems 校验点位归属、启用状态与写权限，并保持请求顺序。
func (s *ControlService) validateItems(ctx context.Context, deviceID string, items []dto.ControlItemDTO) (map[string]po.DeviceAddress, []po.DeviceAddress, error) {
	seen := make(map[string]struct{}, len(items))
	ids := make([]string, 0, len(items))
	for _, item := range items {
		id := strings.TrimSpace(item.DeviceAddressID)
		if id == "" {
			return nil, nil, controlErrMsg(enums.ControlParamInvalidEnum, "点位 id 不能为空")
		}
		if _, dup := seen[id]; dup {
			return nil, nil, controlErrMsg(enums.ControlParamInvalidEnum, "同一点位不能在一次请求里重复出现")
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}

	var rows []po.DeviceAddress
	if err := s.db.WithContext(ctx).Where("device_id = ? AND id IN ?", deviceID, ids).Find(&rows).Error; err != nil {
		return nil, nil, err
	}
	byID := make(map[string]po.DeviceAddress, len(rows))
	for _, a := range rows {
		if a.Status != 1 {
			return nil, nil, controlErrMsg(enums.ControlAddressNotFoundEnum, fmt.Sprintf("点位 %s 已停用", a.Name))
		}
		if !strings.Contains(a.RwPermission, "W") {
			return nil, nil, controlErr(enums.ControlAddressNotWritableEnum)
		}
		byID[a.ID] = a
	}

	// 物模型属性是写入能力的硬上界。绑定校验允许「RW 点位 + R 属性」（点位覆盖
	// 属性），这类点位物理可写但业务语义只读，写入必须拒绝 —— 否则开放接口与
	// 控制台都能绕过物模型声明改值。
	if err := s.ensurePropertiesWritable(ctx, rows); err != nil {
		return nil, nil, err
	}

	ordered := make([]po.DeviceAddress, 0, len(ids))
	for _, id := range ids {
		a, ok := byID[id]
		if !ok {
			return nil, nil, controlErr(enums.ControlAddressNotFoundEnum)
		}
		ordered = append(ordered, a)
	}
	return byID, ordered, nil
}

// ensurePropertiesWritable 拒绝绑定到只读物模型属性的点位。
//
// 未绑定属性的点位不受影响：没有业务语义声明时，写权限只由 rw_permission 决定。
// 属性行缺失（外部改库、悬空 property_id）时无法自证可写，保守拒绝。
func (s *ControlService) ensurePropertiesWritable(ctx context.Context, addrs []po.DeviceAddress) error {
	ids := make([]string, 0, len(addrs))
	seen := make(map[string]struct{}, len(addrs))
	for _, a := range addrs {
		if a.PropertyID == "" {
			continue
		}
		if _, ok := seen[a.PropertyID]; ok {
			continue
		}
		seen[a.PropertyID] = struct{}{}
		ids = append(ids, a.PropertyID)
	}
	if len(ids) == 0 {
		return nil
	}

	var props []po.ThingModelProperty
	if err := s.db.WithContext(ctx).Select("id", "access_mode").
		Where("id IN ?", ids).Find(&props).Error; err != nil {
		return err
	}
	modeByID := make(map[string]string, len(props))
	for _, p := range props {
		modeByID[p.ID] = p.AccessMode
	}

	for _, a := range addrs {
		if a.PropertyID == "" {
			continue
		}
		mode, ok := modeByID[a.PropertyID]
		if !ok {
			return controlErrMsg(enums.ControlPropertyNotWritableEnum,
				fmt.Sprintf("点位 %s 绑定的物模型属性不存在，无法确认写权限", a.Name))
		}
		if !strings.Contains(mode, "W") {
			return controlErrMsg(enums.ControlPropertyNotWritableEnum,
				fmt.Sprintf("点位 %s 绑定的物模型属性为只读", a.Name))
		}
	}
	return nil
}

func (s *ControlService) loadDevice(ctx context.Context, deviceID string) (*po.Device, error) {
	var dev po.Device
	if err := s.db.WithContext(ctx).Where("id = ?", deviceID).First(&dev).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, controlErr(enums.DeviceNotExistsEnum)
		}
		return nil, err
	}
	return &dev, nil
}

func (s *ControlService) protocolName(ctx context.Context, protocolID string) string {
	var p po.IotProtocol
	if err := s.db.WithContext(ctx).Select("name").Where("id = ?", protocolID).First(&p).Error; err != nil {
		return ""
	}
	return p.Name
}

// beginRequest 收敛并发相同 requestId。
//
// 返回 release（第一个请求必须 defer 调用）、已有的历史结果（重复请求直接回放）、
// 或错误（等待期间 ctx 被取消）。requestId 为空时不做任何事。
//
// 等待者只等待、不执行；第一个请求结束时 release 关闭通道，等待者再去读审计。
// 如果第一个请求在落审计前就失败（例如参数校验失败），审计为空，等待者得到一个
// 明确的"请更换 requestId"错误，而不是静默重试 —— 重试可能把一次未知状态
// 的写变成两次真实写。
func (s *ControlService) beginRequest(requestID string, ctx context.Context) (func(), *vo.ControlResultVO, error) {
	if requestID == "" {
		return nil, nil, nil
	}

	s.inflightMu.Lock()
	if ch, ok := s.inflight[requestID]; ok {
		s.inflightMu.Unlock()
		select {
		case <-ch:
		case <-ctx.Done():
			return nil, nil, ctx.Err()
		}
		logs, err := s.loadRequestLogs(ctx, requestID)
		if err != nil {
			return nil, nil, err
		}
		if len(logs) > 0 {
			return func() {}, resultFromLogs(logs), nil
		}
		return nil, nil, controlErrMsg(enums.ControlParamInvalidEnum,
			"相同 requestId 的请求未产生可回放的结果，请更换 requestId 重试")
	}
	ch := make(chan struct{})
	s.inflight[requestID] = ch
	s.inflightMu.Unlock()

	return func() {
		s.inflightMu.Lock()
		if s.inflight[requestID] == ch {
			delete(s.inflight, requestID)
			close(ch)
		}
		s.inflightMu.Unlock()
	}, nil, nil
}

func (s *ControlService) loadRequestLogs(ctx context.Context, requestID string) ([]po.ControlLog, error) {
	var logs []po.ControlLog
	if err := s.db.WithContext(ctx).
		Where("request_id = ?", requestID).
		Order("created_at ASC, id ASC").
		Find(&logs).Error; err != nil {
		return nil, err
	}
	return logs, nil
}

// persistAudit 落审计。审计失败不改变已经发生的写结果，只记 WARN。
//
// 用独立上下文而不是请求 ctx：客户端断开时审计仍然要落下来 —— 它记录的是
// 已经对现场设备产生的动作。
func (s *ControlService) persistAudit(source, operator string, dev *po.Device, protocolName, requestID string, results []vo.ControlItemResultVO, addrByID map[string]po.DeviceAddress) {
	if len(results) == 0 {
		return
	}
	now := time.Now().Format("2006-01-02 15:04:05")
	logs := make([]po.ControlLog, 0, len(results))
	for _, r := range results {
		a := addrByID[r.AddressID]
		verified := 0
		if r.Verified {
			verified = 1
		}
		logs = append(logs, po.ControlLog{
			RequestID:     requestID,
			Source:        source,
			Operator:      operator,
			CreatedAt:     now,
			DeviceID:      dev.ID,
			DeviceName:    dev.Name,
			ProtocolName:  protocolName,
			AddressID:     r.AddressID,
			AddressName:   a.Name,
			AddressLabel:  a.Label,
			DataType:      a.DataType,
			RwPermission:  a.RwPermission,
			RequestValue:  r.Value,
			ResultValue:   r.ResultValue,
			ReadbackValue: r.ReadbackValue,
			Verified:      verified,
			Code:          r.Code,
			Message:       r.Message,
		})
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := s.db.WithContext(ctx).Create(&logs).Error; err != nil {
		logger.Warn("control: persist audit for request %q failed: %v", requestID, err)
	}
}

func resultFromLogs(logs []po.ControlLog) *vo.ControlResultVO {
	first := logs[0]
	out := &vo.ControlResultVO{
		RequestID:    first.RequestID,
		DeviceID:     first.DeviceID,
		DeviceName:   first.DeviceName,
		ProtocolName: first.ProtocolName,
		Duplicate:    true,
		Results:      make([]vo.ControlItemResultVO, 0, len(logs)),
	}
	for _, l := range logs {
		out.Results = append(out.Results, vo.ControlItemResultVO{
			AddressID:     l.AddressID,
			AddressName:   l.AddressName,
			AddressLabel:  l.AddressLabel,
			DataType:      l.DataType,
			RwPermission:  l.RwPermission,
			Value:         l.RequestValue,
			ResultValue:   l.ResultValue,
			ReadbackValue: l.ReadbackValue,
			Verified:      l.Verified == 1,
			OK:            l.Code == driver.WriteCodeOK,
			Code:          l.Code,
			Message:       l.Message,
		})
	}
	return out
}

func controlErr(e *enums.BusinessEnum) *appError.AppError {
	return appError.NewAppErrorCtx(e.GetCode(), e.GetMessage(), e.GetMsgKey())
}

func controlErrMsg(e *enums.BusinessEnum, msg string) *appError.AppError {
	return appError.NewAppErrorCtx(e.GetCode(), msg, e.GetMsgKey())
}

func mapCollectorErr(err error) error {
	switch {
	case errors.Is(err, collector.ErrNotRunning):
		return controlErrMsg(enums.ControlDeviceOfflineEnum, "采集引擎未运行")
	case errors.Is(err, collector.ErrDeviceOffline):
		return controlErr(enums.ControlDeviceOfflineEnum)
	case errors.Is(err, collector.ErrWriteUnsupported):
		return controlErr(enums.ControlProtocolNotWritableEnum)
	case errors.Is(err, collector.ErrDeviceNotActive):
		return controlErrMsg(enums.ControlDeviceOfflineEnum, "设备不在当前采集任务中")
	default:
		// 链路级失败：驱动/插件没能完成调用，写入是否生效未知。
		return appError.NewAppErrorCtx(enums.ControlWriteFailedEnum.GetCode(), err.Error(), enums.ControlWriteFailedEnum.GetMsgKey())
	}
}
