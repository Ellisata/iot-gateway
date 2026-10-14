// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package service

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"
	"unicode/utf8"

	"gorm.io/gorm"

	"iot-gateway/alarm"
	"iot-gateway/appError"
	"iot-gateway/collector"
	"iot-gateway/driver"
	"iot-gateway/enums"
	"iot-gateway/excelutil"
	"iot-gateway/i18n"
	"iot-gateway/logger"
	"iot-gateway/model/dto"
	"iot-gateway/model/po"
	"iot-gateway/model/vo"
	"iot-gateway/response"
)

// DeviceService 设备服务
type DeviceService struct {
	sqliteDB     *gorm.DB
	collector    *collector.Engine
	alarmService *AlarmService
}

// NewDeviceService 创建设备服务
func NewDeviceService(sqliteDB *gorm.DB, engine *collector.Engine, alarmService *AlarmService) *DeviceService {
	return &DeviceService{sqliteDB: sqliteDB, collector: engine, alarmService: alarmService}
}

// CreateDevice 创建设备
func (s *DeviceService) CreateDevice(ctx context.Context, req *dto.CreateDeviceDTO) error {
	return s.sqliteDB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// 检查名称是否已存在
		var existing po.Device
		if err := tx.Where("name = ?", req.Name).First(&existing).Error; err != nil {
			if !errors.Is(err, gorm.ErrRecordNotFound) {
				return err
			}
		}
		if existing.ID != "" {
			return appError.NewAppErrorCtx(enums.DeviceExistsEnum.GetCode(), enums.DeviceExistsEnum.GetMessage(), enums.DeviceExistsEnum.GetMsgKey())
		}

		now := time.Now().Format("2006-01-02 15:04:05")
		protocolJson := ""
		if req.ProtocolJSON != nil {
			protocolJson = string(req.ProtocolJSON)
		}
		device := &po.Device{
			Name:         req.Name,
			ProtocolID:   req.ProtocolID,
			ProtocolJSON: protocolJson,
			Description:  req.Description,
			Status:       1,
			CreatedAt:    now,
			UpdatedAt:    now,
		}

		if err := tx.Create(device).Error; err != nil {
			return err
		}
		return nil
	})
}

// UpdateDevice 更新设备
func (s *DeviceService) UpdateDevice(ctx context.Context, req *dto.UpdateDeviceDTO) error {
	err := s.sqliteDB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// 检查设备是否存在
		var existing po.Device
		id := req.ID
		if err := tx.Where("id = ?", id).First(&existing).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return appError.NewAppErrorCtx(enums.DeviceNotExistsEnum.GetCode(), enums.DeviceNotExistsEnum.GetMessage(), enums.DeviceNotExistsEnum.GetMsgKey())
			}
			return err
		}

		// 检查名称是否被占用（修改名称时）
		if req.Name != "" && req.Name != existing.Name {
			var dup po.Device
			if err := tx.Where("name = ? AND id != ?", req.Name, id).First(&dup).Error; err != nil {
				if !errors.Is(err, gorm.ErrRecordNotFound) {
					return err
				}
			}
			if dup.ID != "" {
				return appError.NewAppErrorCtx(enums.DeviceExistsEnum.GetCode(), enums.DeviceExistsEnum.GetMessage(), enums.DeviceExistsEnum.GetMsgKey())
			}
		}
		protocolJson := ""
		if req.ProtocolJSON != nil {
			protocolJson = string(req.ProtocolJSON)
		}

		updates := make(map[string]interface{})
		if req.Name != "" {
			updates["name"] = req.Name
		}
		if req.ProtocolID != "" {
			updates["protocol_id"] = req.ProtocolID
		}
		if req.ProtocolJSON != nil {
			updates["protocol_json"] = protocolJson
		}
		// 连接配置或协议被改过，扫描身份签名就不再描述这台设备了 ——
		// 留着它会让"扫描建设备"对着一个过时的地址报警告，把用户引向一台
		// 早已不连在那个地址上的设备。**清掉而不是重算**：重算得从 protocol_json
		// 里认出地址，那些键名是插件自己声明的、不统一（理由见 po.Device.ScanIdentity）。
		// 代价只是改过配置的设备不再参与地址判重 —— 漏报一种提示，好过误报。
		if req.ProtocolJSON != nil || req.ProtocolID != "" {
			updates["scan_identity"] = ""
		}
		if req.Description != "" {
			updates["description"] = req.Description
		}
		if req.Status != nil {
			updates["status"] = *req.Status
		}
		if len(updates) == 0 {
			return nil
		}
		updates["updated_at"] = time.Now().Format("2006-01-02 15:04:05")

		return tx.Model(&po.Device{}).Where("id = ?", id).Updates(updates).Error
	})
	if err != nil {
		return err
	}

	// 设备停用（status=0）：清除其活跃断联报警，历史保留。
	// 事务外执行（尽力而为，失败不影响设备更新结果）。
	if req.Status != nil && *req.Status == 0 && s.alarmService != nil {
		if err := s.alarmService.ClearDeviceAlarm(ctx, req.ID); err != nil {
			logger.Warn("device: clear alarm for disabled device %s failed: %v", req.ID, err)
		}
	}
	return nil
}

// DeleteDevice 删除设备
//
// 点位（device_address）在同一事务里一并删除。表间**没有外键约束**
// （见 migrations/002_device.sql），不显式删就会留下一堆查不到的孤立行：
// 采集侧只按 device 加载地址、前端只按 deviceId 列地址，这些行在界面上
// 永远不出现，只能靠翻库发现。同理，将来若有别的表挂 device.id 也要在这里补上。
func (s *DeviceService) DeleteDevice(ctx context.Context, id string) error {
	err := s.sqliteDB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		result := tx.Where("id = ?", id).Delete(&po.Device{})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return appError.NewAppErrorCtx(enums.DeviceNotExistsEnum.GetCode(), enums.DeviceNotExistsEnum.GetMessage(), enums.DeviceNotExistsEnum.GetMsgKey())
		}
		return tx.Where("device_id = ?", id).Delete(&po.DeviceAddress{}).Error
	})
	if err != nil {
		return err
	}

	// 删除后清除其活跃断联报警，历史保留（尽力而为）。
	if s.alarmService != nil {
		if err := s.alarmService.ClearDeviceAlarm(ctx, id); err != nil {
			logger.Warn("device: clear alarm for deleted device %s failed: %v", id, err)
		}
	}
	return nil
}

// BatchDeleteDevice 批量删除设备，返回实际删除的设备行数与连带删除的点位行数。
//
// 一条语句删完，而不是循环调用 DeleteDevice：单条删除在 id 不存在时会报
// DeviceNotExists 并回滚，而批量场景下"某台设备刚被别人删掉"是常态，用它会让
// **整批都删不掉**。所以这里不做逐条存在性校验，也不因个别 id 缺失而失败 ——
// 删了多少就说多少，由调用方决定怎么提示（见 vo.BatchDeleteDeviceResultVO）。
//
// 点位先按同一批 device_id 删掉，再删设备（同一事务，顺序不影响结果）：
// 这样即使某个 id 对应的设备行早就没了，它遗留的孤立点位也能被顺手清干净。
func (s *DeviceService) BatchDeleteDevice(ctx context.Context, ids []string) (int64, int64, error) {
	if len(ids) == 0 {
		return 0, 0, nil
	}
	var devices, addresses int64
	err := s.sqliteDB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		result := tx.Where("device_id IN ?", ids).Delete(&po.DeviceAddress{})
		if result.Error != nil {
			return result.Error
		}
		addresses = result.RowsAffected

		result = tx.Where("id IN ?", ids).Delete(&po.Device{})
		if result.Error != nil {
			return result.Error
		}
		devices = result.RowsAffected
		return nil
	})
	if err != nil {
		return 0, 0, err
	}

	// 清除活跃断联报警，历史保留（尽力而为，失败不影响删除结果，与单条删除一致）。
	if s.alarmService != nil {
		for _, id := range ids {
			if err := s.alarmService.ClearDeviceAlarm(ctx, id); err != nil {
				logger.Warn("device: clear alarm for deleted device %s failed: %v", id, err)
			}
		}
	}
	return devices, addresses, nil
}

// GetDeviceByID 根据ID查询设备
func (s *DeviceService) GetDeviceByID(ctx context.Context, id string) (*vo.DeviceVO, error) {
	var device po.Device
	if err := s.sqliteDB.WithContext(ctx).Where("id = ?", id).First(&device).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, appError.NewAppErrorCtx(enums.DeviceNotExistsEnum.GetCode(), enums.DeviceNotExistsEnum.GetMessage(), enums.DeviceNotExistsEnum.GetMsgKey())
		}
		return nil, err
	}

	// 根据 protocol_id 获取协议名称
	protocolName := s.getProtocolName(ctx, device.ProtocolID)

	online := true
	if s.alarmService != nil {
		online = s.alarmService.IsDeviceOnline(ctx, device.ID)
	}

	return &vo.DeviceVO{
		ID:              device.ID,
		Name:            device.Name,
		ProtocolID:      device.ProtocolID,
		ProtocolName:    protocolName,
		ProtocolJSON:    device.ProtocolJSON,
		Description:     device.Description,
		Status:          device.Status,
		Online:          online,
		LastSuccessTime: s.deviceLastSuccess(device.ID),
		CreatedAt:       device.CreatedAt,
		UpdatedAt:       device.UpdatedAt,
	}, nil
}

// TestDeviceConnection 测试设备连接
// 直接以协议名 + 协议 JSON 配置测试连通性。不同协议的 protocolJson 字段各不相同，
// 故不依赖设备表，以原始 JSON 串传递，由各驱动自行解析。
func (s *DeviceService) TestDeviceConnection(ctx context.Context, req *dto.TestDeviceConnectionDTO) error {
	if req.ProtocolName == "" {
		return appError.NewAppError(enums.DevicePingFailEnum.GetCode(), "协议不存在")
	}
	if err := driver.PingDevice(req.ProtocolName, string(req.ProtocolJSON)); err != nil {
		logger.Error("%s", err.Error())
		return appError.NewAppError(enums.DevicePingFailEnum.GetCode(),
			buildPingFailMessage(ctx, err, busyHintFor(s.collector, string(req.ProtocolJSON))))
	}
	return nil
}

// serialPortHolders 取当前采集任务里声明了串口的设备（引擎未注入/未运行时为 nil）。
//
// 包级函数而非 DeviceService 的方法：点位扫描（DeviceScanService）也要用它拼
// "串口被谁占着"的提示，而两台服务各持一份实现必然漂移 —— 那种漂移的表现是
// 同一件事在两个入口给出不同的说法。nil 守卫留在这一处，两个调用方都不必操心。
func serialPortHolders(eng *collector.Engine) []collector.SerialPortHolder {
	if eng == nil {
		return nil
	}
	return eng.SerialPortHolders()
}

// endpointHolders 取当前采集任务里配了以太网目标（host:port）的设备。
// nil 守卫与 serialPortHolders 同理，也留在这一处。
func endpointHolders(eng *collector.Engine) []collector.EndpointHolder {
	if eng == nil {
		return nil
	}
	return eng.EndpointHolders()
}

// buildPingFailMessage 拼装"测试连接"失败的提示。
//
// 为什么要把驱动原文透给前端而不是回一句固定文案：这是运维诊断入口，
// 「COM1 已被占用」这类信息必须能直接看到 —— 只进日志等于没有。
// 代价是驱动原文（通常是中文）不参与 i18n；前缀仍走译文，故 en 环境下
// 至少能看懂"这是连接测试失败"，而不是一句无差别的英文。
//
// hint 由 busyHintFor 拼好（含换行与括号），本函数不再二次包装：包装只有一处，
// 三个入口追加出来的东西才长得一样。
func buildPingFailMessage(ctx context.Context, pingErr error, hint string) string {
	prefix := enums.DevicePingFailEnum.GetMessage()
	if key := enums.DevicePingFailEnum.GetMsgKey(); key != "" {
		if translated := i18n.TCtx(ctx, key); translated != key {
			prefix = translated
		}
	}
	return prefix + "：" + pingErr.Error() + hint
}

// busyHintFor 取引擎快照并拼出"这次失败可能另有外部原因"的说明块。
//
// 三个入口（测试连接 / 点位扫描失败 / 点位扫描中断）都调它，而不是各自取一遍
// holders 再各自包装一次：同一件事在两个入口必须给出一致的说法，各拼一份迟早漂移。
func busyHintFor(eng *collector.Engine, protocolJSON string) string {
	return busyHint(protocolJSON, serialPortHolders(eng), endpointHolders(eng))
}

// busyHint 按顺序拼出可**直接追加**到错误文案后的说明块（自带换行与括号）；
// 没有可说的返回空串。
//
// 两句话可以同时出现（JSON 里既有 comPort 又有 host 的怪配置），故不合并成一段：
// 串口说"这个口被谁占着"，以太网说"这个地址正被谁采着"，是两件事。
//
// 拼接的**时机**由调用方保证 —— 只在调用确实失败时拼（测试成功还提示占用，
// 荒谬）。这条纪律三个入口共用，写在这里以免有人在某个入口漏掉。
func busyHint(protocolJSON string, serial []collector.SerialPortHolder,
	endpoints []collector.EndpointHolder) string {

	var b strings.Builder
	for _, hint := range []string{
		serialBusyHint(protocolJSON, serial),
		endpointBusyHint(protocolJSON, endpoints),
	} {
		if hint == "" {
			continue
		}
		b.WriteString("\n（")
		b.WriteString(hint)
		b.WriteString("）")
	}
	return b.String()
}

// serialBusyHint 返回"该串口当前被谁占着"的说明；没查到返回空串。
//
// 只收**同口**的持有者（大小写不敏感 —— Windows 的 COM1 与 com1 是同一个端口，
// 而串口名的比较必须用与 driver.ExtractSerialPort 同一套规则，否则
// "我在测哪个口"与"谁占着这个口"两边会各判各的）。
func serialBusyHint(protocolJSON string, holders []collector.SerialPortHolder) string {
	port := driver.ExtractSerialPort(protocolJSON)
	if port == "" || len(holders) == 0 {
		return ""
	}

	var connected, disconnected []string
	for _, h := range holders {
		if !strings.EqualFold(h.Port, port) {
			continue
		}
		if h.Connected {
			connected = append(connected, h.DeviceName)
		} else {
			disconnected = append(disconnected, h.DeviceName)
		}
	}
	if len(connected) == 0 && len(disconnected) == 0 {
		return ""
	}

	// 已连接的优先列出：它才是"句柄现在确实被打开着"的那一个。
	names := append(append([]string{}, connected...), disconnected...)
	who := describeHolders(names)

	const tail = "串口在同一时刻只能被一个连接打开；若该设备正在采集，请先停用该设备再测试"
	if len(connected) > 0 {
		return fmt.Sprintf("%s 当前由%s占用；%s", port, who, tail)
	}
	// 只有未连接的持有者：设备懒连接、或正处于重连退避窗口内，句柄可能已被释放。
	// 措辞必须留余地，否则就成了凭空断言。
	return fmt.Sprintf("%s 当前配置给了%s（这些设备当前未连接，串口未必仍被占用）；%s", port, who, tail)
}

// endpointBusyHint 返回"这个以太网目标被哪台设备配着"的说明；没查到返回空串。
//
// 与 serialBusyHint 的**区别在措辞分档的依据，不在是否过滤**：两处都只看"这个目标
// 配给了谁"，只是串口那边分档的是"句柄是否打开"，这边分档的是"链路是否通着"。
//
// **不按 Connected 过滤**（初版这么写过，被真机打回来了）：设备没在采集恰恰是
// 最需要提示的时刻之一 —— 实测那台 PLC 在唯一连接槽被一条**已死**的连接占住后，
// 一律拒绝新连接（对端断开它不会立刻察觉），此时网关侧没有任何"正在采集"的设备，
// 而未连接的那台设备恰恰就是这个地址上唯一的历史持有者。按 Connected 过滤会让
// 提示在最需要它的时候**一声不吭**。
//
// 为什么这句提示值得存在（真机实测，10.62.100.22:10000，三菱内置以太网）：
// 采集正常、测试连接必超时，用户看到的是"i/o timeout"这句与真相毫无关系的话 ——
// 真相是 PLC 只服务一条连接，第二条连接在 TCP 层被收下但不应答（连续重试后连
// TCP 握手都不被接受）。措辞只说确定的事（这个地址配给了谁、那台设备当前是否连着）
// 加一句可被证伪的推测（"若被测设备只允许一条连接"），不断言因果 —— 真的原因也
// 可能是地址填错、网络不通，那时这句话依然读得通。
func endpointBusyHint(protocolJSON string, holders []collector.EndpointHolder) string {
	endpoint := driver.ExtractEndpoint(protocolJSON)
	if endpoint == "" || len(holders) == 0 {
		return ""
	}

	var connected, disconnected []string
	for _, h := range holders {
		// 主机名大小写不敏感（同 serialBusyHint：比较方负责归一，持有者原样保留）
		if !strings.EqualFold(h.Endpoint, endpoint) {
			continue
		}
		if h.Connected {
			connected = append(connected, h.DeviceName)
		} else {
			disconnected = append(disconnected, h.DeviceName)
		}
	}
	if len(connected) == 0 && len(disconnected) == 0 {
		return ""
	}

	// 已连接的优先列出：它才是"此刻确实有一条连接挂在设备侧"的那一个。
	who := describeHolders(append(append([]string{}, connected...), disconnected...))

	const tail = "若被测设备只允许一条连接，第二条通常会被拒绝或不应答（表现都是超时）——可先停用该设备再测试，或在设备侧放开连接数"
	if len(connected) > 0 {
		return fmt.Sprintf("%s 已配置给%s，该设备正在采集；%s", endpoint, who, tail)
	}
	// 只有未连接的持有者：设备侧那个连接槽可能仍被**此前断开的**客户端占着
	// （对端断开设备不会立刻察觉），现场的上位机软件同样可能占着它。
	// 措辞必须留余地，且这里的动作与上面那一档不同 —— 让用户去"停用设备"没有意义。
	return fmt.Sprintf("%s 已配置给%s（这些设备当前未连接；设备侧那个连接槽可能仍被"+
		"此前断开的客户端或现场上位机软件占着）——可先确认没有别的客户端在连，"+
		"或把该端口的连接数调大", endpoint, who)
}

// describeHolders 把设备名列表拼成「设备「A」、「B」等 3 台」。
func describeHolders(names []string) string {
	const maxShown = 3
	var b strings.Builder
	for i, n := range names {
		if i == maxShown {
			fmt.Fprintf(&b, "等 %d 台设备", len(names))
			break
		}
		if i > 0 {
			b.WriteString("、")
		}
		fmt.Fprintf(&b, "设备「%s」", n)
	}
	return b.String()
}

// PageDevice 设备分页查询（支持名称模糊检索）
func (s *DeviceService) PageDevice(ctx context.Context, req *dto.PageDeviceDTO) (*response.PageVo[vo.DeviceVO], error) {
	db := s.sqliteDB.WithContext(ctx).Model(&po.Device{})

	if req.Name != "" {
		db = db.Where("name LIKE ?", "%"+req.Name+"%")
	}

	var total int64
	if err := db.Count(&total).Error; err != nil {
		return nil, err
	}

	var devices []po.Device
	offset := (req.Page - 1) * req.Size
	if err := db.Offset(offset).Limit(req.Size).Order("created_at DESC").Find(&devices).Error; err != nil {
		return nil, err
	}

	records := s.toDeviceVOs(ctx, devices)

	return &response.PageVo[vo.DeviceVO]{
		Page:    req.Page,
		Size:    req.Size,
		Total:   total,
		Records: records,
	}, nil
}

// toDeviceVOs 把设备行映射为 VO，并统一富化在线状态与最近成功采集时间。
//
// 从 PageDevice 抽出来给分页与游标两条路径共用：离线集合一次性取（仅
// target_type=device，避免混入通道报警），protocolName 与 lastSuccessTime 逐台补齐。
// 两处各写一份的话，将来加字段就会只加进其中一条路径 —— 那正是"两份差不多"的来源。
func (s *DeviceService) toDeviceVOs(ctx context.Context, devices []po.Device) []vo.DeviceVO {
	offlineSet := make(map[string]bool)
	if s.alarmService != nil {
		if active, err := s.alarmService.ListActiveTargets(ctx, alarm.TypeDevice); err == nil {
			for _, a := range active {
				offlineSet[a.TargetID] = true
			}
		}
	}

	records := make([]vo.DeviceVO, len(devices))
	for i, d := range devices {
		records[i] = vo.DeviceVO{
			ID:              d.ID,
			Name:            d.Name,
			ProtocolID:      d.ProtocolID,
			ProtocolName:    s.getProtocolName(ctx, d.ProtocolID),
			ProtocolJSON:    d.ProtocolJSON,
			Description:     d.Description,
			Status:          d.Status,
			Online:          !offlineSet[d.ID],
			LastSuccessTime: s.deviceLastSuccess(d.ID),
			CreatedAt:       d.CreatedAt,
			UpdatedAt:       d.UpdatedAt,
		}
	}
	return records
}

// ListDevicesAfterID 游标分批查询设备（keyset：id > afterID，按 id 升序，最多 limit 条）。
//
// 供开放接口的全量同步用。设备 id 是 UUIDv7（长度固定的 32 位 hex，字典序即时间序），
// 故按 id 升序续读时：遍历途中**新增**的设备 id 更大、排在后面会被同一次遍历读到，
// **删除**的设备自然消失 —— 不像 OFFSET 那样让未读区间整体位移、造成漏读或重读。
// 查询走 device 的主键索引（id 是 TEXT PRIMARY KEY）。
//
// afterID 为空表示从头开始；返回条数可能少于 limit（已到末尾，调用方据此判 hasMore）。
// limit <= 0 时按 100 兜底：调用方漏传不该退化成"一次拉全表"。
func (s *DeviceService) ListDevicesAfterID(ctx context.Context, afterID string, limit int) ([]vo.DeviceVO, error) {
	if limit <= 0 {
		limit = 100
	}
	db := s.sqliteDB.WithContext(ctx).Model(&po.Device{})
	if afterID != "" {
		db = db.Where("id > ?", afterID)
	}

	var devices []po.Device
	if err := db.Order("id ASC").Limit(limit).Find(&devices).Error; err != nil {
		return nil, err
	}
	return s.toDeviceVOs(ctx, devices), nil
}

// GetDeviceNamesByIDs 根据设备ID列表批量查询设备名称，返回 id → name 映射。
// 库中不存在的ID不出现在结果里；ids 为空直接返回空映射，不访问数据库。
func (s *DeviceService) GetDeviceNamesByIDs(ctx context.Context, ids []string) (map[string]string, error) {
	nameMap := make(map[string]string, len(ids))
	if len(ids) == 0 {
		return nameMap, nil
	}
	var devices []po.Device
	if err := s.sqliteDB.WithContext(ctx).Select("id", "name").Where("id IN ?", ids).Find(&devices).Error; err != nil {
		return nil, err
	}
	for _, d := range devices {
		nameMap[d.ID] = d.Name
	}
	return nameMap, nil
}

// getProtocolName 根据 protocol_id 查询协议名称
func (s *DeviceService) getProtocolName(ctx context.Context, protocolID string) string {
	if protocolID == "" {
		return ""
	}
	var protocol po.IotProtocol
	if err := s.sqliteDB.WithContext(ctx).Select("name").Where("id = ?", protocolID).First(&protocol).Error; err != nil {
		return ""
	}
	return protocol.Name
}

// GetDeviceOverview 统计设备在线情况（只读聚合，无写库）。
// 口径：total=全部设备（启用+禁用）；enabled=启用设备；disabled=禁用设备；
// collected=正在被采集调度的启用设备（有驱动且至少一个活跃地址）；
// offline=collected 且存在 active 断联报警；online=collected-offline；
// unCollected=启用但未接入采集（协议不支持/无驱动/无活跃地址，永不判定在线/离线）；
// onlineRate=online/collected（分母用已接入采集，避免 unCollected 虚高在线率）。
func (s *DeviceService) GetDeviceOverview(ctx context.Context) (*vo.DeviceOverviewVO, error) {
	var devices []po.Device
	if err := s.sqliteDB.WithContext(ctx).Select("id", "status").Find(&devices).Error; err != nil {
		return nil, err
	}
	enabledSet := make(map[string]bool)
	disabledSet := make(map[string]bool)
	for _, d := range devices {
		if d.Status == 1 {
			enabledSet[d.ID] = true
		} else {
			disabledSet[d.ID] = true
		}
	}

	// 离线集合：active 的 device 断联报警（停用/删除设备时报警已被清除，天然只含启用且被采集的设备）
	offlineSet := make(map[string]bool)
	if s.alarmService != nil {
		if active, err := s.alarmService.ListActiveTargets(ctx, alarm.TypeDevice); err == nil {
			for _, a := range active {
				offlineSet[a.TargetID] = true
			}
		}
	}

	// 已接入采集集合：采集引擎当前调度的设备（引擎只加载 status=1 设备）
	collectedSet := make(map[string]bool)
	if s.collector != nil {
		for _, id := range s.collector.ActiveDeviceIDs() {
			collectedSet[id] = true
		}
	}

	return buildDeviceOverview(enabledSet, disabledSet, offlineSet, collectedSet), nil
}

// buildDeviceOverview 由四组设备集合计算在线情况（纯函数，便于单测）。
// 防御：offline 与 collected 只计入 enabled 集合内的设备，避免停用/脏数据污染统计。
func buildDeviceOverview(enabledSet, disabledSet, offlineSet, collectedSet map[string]bool) *vo.DeviceOverviewVO {
	enabled := len(enabledSet)
	total := enabled + len(disabledSet)
	collected, online, offline := 0, 0, 0
	for id := range collectedSet {
		if !enabledSet[id] {
			continue
		}
		collected++
		if offlineSet[id] {
			offline++
		} else {
			online++
		}
	}
	rate := 0.0
	if collected > 0 {
		rate = math.Round(float64(online)*100/float64(collected)) / 100
	}
	return &vo.DeviceOverviewVO{
		Total:       total,
		Enabled:     enabled,
		Disabled:    total - enabled,
		Collected:   collected,
		Online:      online,
		Offline:     offline,
		UnCollected: enabled - collected,
		OnlineRate:  rate,
	}
}

// deviceLastSuccess 返回设备最近一次成功采集时间（采集引擎未运行或设备未采集时为空串）。
func (s *DeviceService) deviceLastSuccess(deviceID string) string {
	if s.collector == nil {
		return ""
	}
	return s.collector.DeviceLastSuccessTime(deviceID)
}

// ==================== Excel 批量导入设备 ====================

// ImportDevices 从 Excel(.xlsx) 批量导入设备。
// 模板三列：名称 / 协议 / 描述。协议列按 iot_protocol.name 匹配，未知协议跳过该行并记录原因；
// 名称重复（库内或文件内）跳过该行；其余行部分导入并返回汇总，供前端展示失败明细。
// 导入仅建基本信息，协议参数留空 "{}"，连接参数后续在编辑界面补填。
func (s *DeviceService) ImportDevices(ctx context.Context, data []byte) (*vo.ExcelImportResultVO, error) {
	// 预加载现有设备名，用于去重
	var names []string
	if err := s.sqliteDB.WithContext(ctx).Model(&po.Device{}).Pluck("name", &names).Error; err != nil {
		return nil, err
	}

	// 预加载协议名 → ID 映射
	protocolMap := make(map[string]string)
	var protocols []po.IotProtocol
	if err := s.sqliteDB.WithContext(ctx).Select("id", "name").Find(&protocols).Error; err != nil {
		return nil, err
	}
	for _, p := range protocols {
		protocolMap[p.Name] = p.ID
	}

	now := time.Now().Format("2006-01-02 15:04:05")
	return excelutil.Import(ctx, s.sqliteDB, data, excelutil.Options[po.Device]{
		Headers:      []string{"名称", "协议", "描述"},
		ExistingKeys: names,
		Cols:         3,
		DupMsg:       enums.DeviceExistsEnum.GetMessage(),
		Parse: func(row []string) (*po.Device, string, string) {
			name := excelutil.Cell(row, 0)
			protocolName := excelutil.Cell(row, 1)
			desc := excelutil.Cell(row, 2)
			if reason := validateImportRow(name, protocolName, protocolMap); reason != "" {
				return nil, name, reason
			}
			return &po.Device{
				Name:         name,
				ProtocolID:   protocolMap[protocolName],
				ProtocolJSON: "{}",
				Description:  desc,
				Status:       1,
				CreatedAt:    now,
				UpdatedAt:    now,
			}, name, ""
		},
	})
}

// validateImportRow 校验单行设备导入数据（纯函数，便于单测；去重由 excelutil.Import 统一处理）。
// 返回 "" 表示通过，否则返回中文失败原因。
func validateImportRow(name, protocolName string, protocolMap map[string]string) string {
	if name == "" {
		return "名称为空"
	}
	if utf8.RuneCountInString(name) > 100 {
		return "名称不能超过100个字符"
	}
	if protocolName == "" {
		return "协议为空"
	}
	if _, ok := protocolMap[protocolName]; !ok {
		return fmt.Sprintf("协议不存在：%s", protocolName)
	}
	return ""
}

// GenerateImportTemplate 生成设备导入模板（.xlsx）：表头「名称/协议/描述」+ 示例行 + 冻结首行。
func (s *DeviceService) GenerateImportTemplate() (*bytes.Buffer, error) {
	return excelutil.BuildTemplate(
		[]string{"名称", "协议", "描述"},
		[]string{"示例：1号注塑机", "ModBus.TCP", "示例数据，可删除或覆盖"},
		[]float64{32, 22, 40},
	)
}
