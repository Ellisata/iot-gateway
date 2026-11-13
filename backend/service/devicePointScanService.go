// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"gorm.io/gorm"

	"github.com/Ellisata/iot-gateway-sdk/driver"
	"iot-gateway/appError"
	"iot-gateway/devicescan"
	"iot-gateway/enums"
	"iot-gateway/model/dto"
	"iot-gateway/model/po"
	"iot-gateway/model/vo"
	"iot-gateway/pluginhost"
)

// ==================== 点位范围扫描 ====================
//
// 与 deviceScanService.go（局域网设备扫描）同属 DeviceScanService。分成两个文件
// 只是因为各自都够长，不是因为它们是两件事 —— 错误码、总开关、页面都是共用的。

// PointScanSource 点位扫描的**能力来源**（PointDiscoverySpecs）与**调用出口**
// （DiscoverPoints）。生产实现是 *pluginhost.Manager。
//
// 定义成接口而不是直接依赖 *pluginhost.Manager，是为了让服务层能被替身测试 ——
// 与 devicescan.SpecSource / Prober 是同一条取舍（那里也是靠接口把
// "哪些协议能扫"与"实际去探测"两件事从管理器上摘下来）。
// 少了它，本文件里最值得测的几条规则（部分失败不中断、双重去重、label 兜底、
// 服务端取 protocolJSON）都得先拉起一个真实的插件进程。
type PointScanSource interface {
	// PointDiscoverySpecs 当前已加载插件声明的全部点位扫描能力（每次现算）。
	PointDiscoverySpecs() []driver.ProtocolPointDiscovery
	// DiscoverPoints 让提供该协议的插件扫描 protocolJSON 描述的那台设备。
	DiscoverPoints(ctx context.Context, protocol, protocolJSON string,
		options json.RawMessage) (*driver.PointScanResult, error)
}

// PointCapability 返回某个协议的点位扫描能力，供设备地址页决定按钮能不能点。
//
// **不支持时返回 200 + supported=false，而不是报错**：这是进页面就会调的一次
// 查询，"这个协议不支持"是正常答案，不是失败。前端据 supported/enabled 组合出
// 不同的提示（隐藏按钮会让人以为功能没做，见 index 页的写法）。
//
// protocolName 取不到（协议行为插件供给失败而缺失）时同样回 supported=false ——
// 与 deviceScan 的 Capabilities 里 ProtocolID 留空是同一条取舍：查不到不是错误。
func (s *DeviceScanService) PointCapability(ctx context.Context, protocolName string) (*vo.PointCapabilityVO, error) {
	out := &vo.PointCapabilityVO{
		Enabled:   s.engine.Enabled(),
		Protocol:  protocolName,
		Supported: false,
	}
	if s.manager == nil || protocolName == "" {
		return out, nil
	}
	for _, pd := range s.manager.PointDiscoverySpecs() {
		if pd.Protocol != protocolName {
			continue
		}
		out.Supported = true
		out.Spec = pointSpecVO(pd.Spec)
		break
	}
	return out, nil
}

// DiscoverPoints 对一台已存在的设备发起一次范围扫描。
//
// **这是同步调用**（与设备扫描的"登记任务即返回"相反）：插件侧一次扫描覆盖整段，
// 中途没有"取一部分结果"的入口，所以做不成可轮询的任务。代价是它必须够快 ——
// 网关侧的预算被钳在 9s 以内（见 pluginhost 的 pointScanGatewayBudgetCapMs），
// 为的就是不让它把 http.Server.Shutdown 拖过 10s 的宽限期。
func (s *DeviceScanService) DiscoverPoints(ctx context.Context, req *dto.DiscoverPointsDTO) (*vo.PointScanResultVO, error) {
	// 复用设备扫描的总开关，**不开新的**：点扫与设备扫描是同一页上的两个动作，
	// 两个开关会让人只关掉一个，然后在另一个入口重新遭遇他以为已经关掉的功能。
	if !s.engine.Enabled() {
		return nil, appError.NewAppErrorCtx(
			enums.DeviceScanDisabledEnum.GetCode(),
			enums.DeviceScanDisabledEnum.GetMessage(),
			enums.DeviceScanDisabledEnum.GetMsgKey())
	}

	device, protocolName, err := s.deviceForScan(ctx, req.DeviceID)
	if err != nil {
		return nil, err
	}
	return s.discoverPointsForDevice(ctx, device, protocolName, req.Options)
}

// DiscoverPointsForOpenApi 管理平台发起的点位扫描。
//
// 两种定位方式，与 OpenApiDiscoverPointsDTO 的注释一致：
//   - 给了 deviceCode：从网关自己的 device 行取配置（网关侧实际生效的那份）；
//   - 只给 protocolName + protocolJson：按调用方给的配置扫，用于**尚未下发**的
//     设备对象（平台是主数据的事实源，先扫点位再一次性下发）。
func (s *DeviceScanService) DiscoverPointsForOpenApi(ctx context.Context, req *dto.OpenApiDiscoverPointsDTO) (*vo.PointScanResultVO, error) {
	if !s.engine.Enabled() {
		return nil, appError.NewAppErrorCtx(
			enums.DeviceScanDisabledEnum.GetCode(),
			enums.DeviceScanDisabledEnum.GetMessage(),
			enums.DeviceScanDisabledEnum.GetMsgKey())
	}

	if code := strings.TrimSpace(req.DeviceCode); code != "" {
		device, protocolName, err := s.deviceForScanByCode(ctx, code)
		if err != nil {
			return nil, err
		}
		return s.discoverPointsForDevice(ctx, device, protocolName, req.Options)
	}

	protocolName := strings.TrimSpace(req.ProtocolName)
	if protocolName == "" {
		return nil, appError.NewAppErrorCtx(
			enums.DeviceScanParamInvalidEnum.GetCode(),
			"deviceCode 与 protocolName 至少提供一个",
			enums.DeviceScanParamInvalidEnum.GetMsgKey())
	}
	protocolJSON, err := normalizeScanProtocolJSON(req.ProtocolJSON)
	if err != nil {
		return nil, appError.NewAppErrorCtx(
			enums.DeviceScanParamInvalidEnum.GetCode(),
			err.Error(),
			enums.DeviceScanParamInvalidEnum.GetMsgKey())
	}
	return s.discoverPointsWithConfig(ctx, protocolName, protocolJSON, req.Options)
}

// discoverPointsForDevice 对一台网关设备执行点扫。
func (s *DeviceScanService) discoverPointsForDevice(ctx context.Context, device *po.Device, protocolName string, options json.RawMessage) (*vo.PointScanResultVO, error) {
	return s.discoverPointsWithConfig(ctx, protocolName, device.ProtocolJSON, options)
}

// discoverPointsWithConfig 按一份连接配置执行点扫，并保留中断前的部分结果。
func (s *DeviceScanService) discoverPointsWithConfig(ctx context.Context, protocolName, protocolJSON string, options json.RawMessage) (*vo.PointScanResultVO, error) {
	// 能力**现算**，不建缓存表：热升级只换 lp.meta，而 registrationSig 不含
	// PointDiscovery，缓存会陈旧（与 devicescan 的 specOf 同一条理由）。
	if !s.protocolSupportsPointScan(protocolName) {
		return nil, appError.NewAppError(enums.DevicePointScanUnsupportedEnum.GetCode(),
			enums.DevicePointScanUnsupportedEnum.GetMessage())
	}

	result, err := s.manager.DiscoverPoints(ctx, protocolName, protocolJSON, options)
	if err != nil {
		// **带着部分结果的失败**：这份结果是一路刻意保下来的（插件侧
		// ScanPoints 在链路中断时 return sc.finish(), chunkErr，宿主
		// pluginhost.DiscoverPoints 又专门判了一次"reply 非空就一并返回"），
		// 丢掉它对用户没有任何好处 —— 一次扫描可能已经跑了十几秒、读到了几百个
		// 地址，只是在第 800 个上断了链路；用户要的是那 800 个，不是一句"失败"。
		//
		// 判定口径与宿主一致（它就是这么过滤的）：扫过或读到了任何东西都算部分结果。
		// 两者都为空说明扫描根本没开始（选项解析失败、设备正忙、配置模式），
		// 那时没有东西可给，照旧按错误返回。
		if result != nil && (result.Scanned > 0 || len(result.Points) > 0) {
			out := pointScanResultVO(protocolName, result)
			// 中断的扫描**必然**是"范围没扫完"，而插件侧的 Incomplete 只在预算
			// 耗尽时置位（链路中断不经过那条分支）。不在这里补上，用户会从一份
			// 截断的清单里得出"这个范围只有这些点位"的错误结论 —— 与设备扫描的
			// Truncated 是同一条要求（见 driver.PointScanResult 对五个标志的说明）。
			out.Incomplete = true
			out.Warning = s.pointScanInterruptMessage(err, protocolJSON)
			return out, nil
		}
		return nil, s.pointScanError(ctx, err, protocolJSON)
	}
	return pointScanResultVO(protocolName, result), nil
}

// normalizeScanProtocolJSON 校验并规整开放接口传入的连接配置。
//
// 只接受 JSON 对象：插件配置解析器期望的是对象，数组/标量会在插件里报一句
// 与根因相隔很远的解析错误；空值则会让插件去连一个默认地址。
func normalizeScanProtocolJSON(raw json.RawMessage) (string, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return "", errors.New("protocolJson 不能为空")
	}
	var obj map[string]any
	if err := json.Unmarshal(trimmed, &obj); err != nil {
		return "", errors.New("protocolJson 必须是 JSON 对象")
	}
	out, err := json.Marshal(obj)
	if err != nil {
		return "", err
	}
	return string(out), nil
}

// DiscoverPointsFromScan 对设备扫描结果中的一台设备发起点位扫描。
//
// 连接配置（protocolJson）由服务端从任务快照里取，不采信客户端 —— 与
// CreateDevicesFromScanDTO 同一条边界。要求任务已结束：扫描还在跑时结果清单
// 仍在增长，此时点扫会让"看到的设备"与"扫到的点位"来自两个不同时刻。
func (s *DeviceScanService) DiscoverPointsFromScan(ctx context.Context, req *dto.DiscoverPointsFromScanDTO) (*vo.PointScanResultVO, error) {
	if !s.engine.Enabled() {
		return nil, appError.NewAppErrorCtx(
			enums.DeviceScanDisabledEnum.GetCode(),
			enums.DeviceScanDisabledEnum.GetMessage(),
			enums.DeviceScanDisabledEnum.GetMsgKey())
	}

	snap, err := s.engine.Status(req.JobID)
	if err != nil {
		return nil, scanError(err)
	}
	if snap.State == devicescan.JobRunning {
		return nil, appError.NewAppErrorCtx(
			enums.DeviceScanBusyEnum.GetCode(),
			"扫描还在进行中，请等它结束后再扫描点位",
			enums.DeviceScanBusyEnum.GetMsgKey())
	}
	if req.Index < 0 || req.Index >= len(snap.Devices) {
		return nil, appError.NewAppErrorCtx(
			enums.DeviceScanParamInvalidEnum.GetCode(),
			"扫描结果已失效（下标越界），请重新扫描",
			enums.DeviceScanParamInvalidEnum.GetMsgKey())
	}
	dev := snap.Devices[req.Index]
	if !s.protocolSupportsPointScan(snap.Protocol) {
		return nil, appError.NewAppError(enums.DevicePointScanUnsupportedEnum.GetCode(),
			enums.DevicePointScanUnsupportedEnum.GetMessage())
	}

	result, err := s.manager.DiscoverPoints(ctx, snap.Protocol, dev.ProtocolJSON, req.Options)
	if err != nil {
		// 与 DiscoverPoints 完全相同：带部分结果的中断仍返回结果，只补 Incomplete/Warning。
		if result != nil && (result.Scanned > 0 || len(result.Points) > 0) {
			out := pointScanResultVO(snap.Protocol, result)
			out.Incomplete = true
			out.Warning = s.pointScanInterruptMessage(err, dev.ProtocolJSON)
			return out, nil
		}
		return nil, s.pointScanError(ctx, err, dev.ProtocolJSON)
	}
	return pointScanResultVO(snap.Protocol, result), nil
}

// pointScanInterruptMessage 部分结果随附的提示语。
//
// 与 pointScanError 的取舍相同（根因是动态的，只能把插件的原文带出来），
// 但说的不是一件事：那边是"这次扫描没能进行"，这边是"下面这些是中断前已经读到的，
// 可以照常建为点位"。用户拿到的仍是可用的东西，只是这份清单残缺 ——
// 两句话必须分开，否则用户会把一份半成品当成空结果丢掉。
func (s *DeviceScanService) pointScanInterruptMessage(scanErr error, protocolJSON string) string {
	msg := "扫描中断（下面这些是中断前已读到的地址，可以照常建为点位）：" + scanErr.Error()
	return msg + busyHintFor(s.collector, protocolJSON)
}

// CreateAddresses 把勾选的点位批量建成 device_address 行。
//
// 逐条失败不中断整批，返回契约复用 vo.ExcelImportResultVO（与 Excel 导入、
// 设备扫描建设备共用同一套前端组件）。范本是 DeviceAddressService.ImportDeviceAddresses，
// 逐行校验也复用了同一个 validateAddressImportRow 纯函数 —— 两处规则必须一致，
// 否则"从扫描建点位"与"从 Excel 导点位"会对同一份数据给出不同判定。
//
// 与那条路的**唯一差异是 label 的兜底**：Excel 模板里标签是用户自己填的一列，
// 留空就是漏填；而扫描结果**没有 label 来源**（设备不会告诉你"这个寄存器是温度"），
// 要求用户逐行为几十个点位想标签等于逼他放弃。所以这里 label 为空时兜底为 name ——
// 这是有意的，name 至少是唯一的、能定位的。用户可以在建完后回到列表里改。
func (s *DeviceScanService) CreateAddresses(ctx context.Context, req *dto.CreateAddressesFromScanDTO) (*vo.ExcelImportResultVO, error) {
	// 设备必须存在：它决定协议，从而决定通用类型名怎么映射成内部类型。
	_, protocolName, err := s.deviceForScan(ctx, req.DeviceID)
	if err != nil {
		return nil, err
	}

	// 预加载该设备已有地址名，用于去重（与 ImportDeviceAddresses 同源）。
	var names []string
	if err := s.sqliteDB.WithContext(ctx).Model(&po.DeviceAddress{}).
		Where("device_id = ?", req.DeviceID).Pluck("name", &names).Error; err != nil {
		return nil, err
	}

	result := &vo.ExcelImportResultVO{Errors: []vo.ExcelImportErrorVO{}}
	existing := make(map[string]bool, len(names))
	for _, n := range names {
		existing[n] = true
	}
	// 批内去重与库内去重是**两件事**：(device_id, name) 是复合唯一，
	// 而同一次扫描里同一个地址被勾两次完全可能（前端不该让它发生，但服务端
	// 不能假定）。少了批内那层，第二条会撞 SQLite 约束，用户看到一条数据库错误。
	seen := make(map[string]bool)

	now := time.Now().Format("2006-01-02 15:04:05")
	_ = s.sqliteDB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		for i, item := range req.Items {
			result.Total++
			// Row 沿用 Excel 导入"从 1 计"的语义，这里就是第几条勾选。
			row := i + 1

			label := item.Label
			if label == "" {
				label = item.Name
			}
			freq := ""
			if item.ScanFrequency > 0 {
				freq = strconv.Itoa(item.ScanFrequency)
			}

			// 与 Excel 导入共用同一套逐行规则。
			reason, frequency := validateAddressImportRow(item.Name, label, item.CommonDataType,
				item.RwPermission, freq)
			if reason != "" {
				result.Errors = append(result.Errors, vo.ExcelImportErrorVO{
					Row: row, Name: item.Name, Reason: reason,
				})
				continue
			}

			// 通用数据类型 → 协议内部类型（与单条创建、Excel 导入走同一套映射）
			internal, ok := driver.LookupDataType(protocolName, item.CommonDataType)
			if !ok {
				result.Errors = append(result.Errors, vo.ExcelImportErrorVO{
					Row: row, Name: item.Name,
					Reason: fmt.Sprintf("协议不支持该数据类型：%s", item.CommonDataType),
				})
				continue
			}

			if existing[item.Name] || seen[item.Name] {
				result.Errors = append(result.Errors, vo.ExcelImportErrorVO{
					Row: row, Name: item.Name, Reason: enums.DeviceAddressExistsEnum.GetMessage(),
				})
				continue
			}

			record := po.DeviceAddress{
				DeviceID:       req.DeviceID,
				Name:           item.Name,
				Label:          label,
				CommonDataType: item.CommonDataType,
				DataType:       internal,
				RwPermission:   item.RwPermission,
				ScanFrequency:  frequency,
				Description:    item.Description,
				Status:         1,
				CreatedAt:      now,
				UpdatedAt:      now,
			}
			if err := tx.Create(&record).Error; err != nil {
				result.Errors = append(result.Errors, vo.ExcelImportErrorVO{
					Row: row, Name: item.Name, Reason: "入库失败：" + err.Error(),
				})
				continue
			}
			existing[item.Name] = true
			seen[item.Name] = true
			result.Success++
		}
		return nil
	})

	result.Failed = len(result.Errors)
	return result, nil
}

// deviceForScan 查设备行并取出它的协议名。
//
// 两个方法都要它，且都**只认 deviceId**：连接配置（protocolJson）由服务端从这里
// 读出后透传，绝不采信客户端 —— 它决定网关去连哪台设备（照 CreateDevices 的先例）。
func (s *DeviceScanService) deviceForScan(ctx context.Context, deviceID string) (*po.Device, string, error) {
	var device po.Device
	if err := s.sqliteDB.WithContext(ctx).Where("id = ?", deviceID).First(&device).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, "", appError.NewAppErrorCtx(
				enums.DeviceNotExistsEnum.GetCode(),
				enums.DeviceNotExistsEnum.GetMessage(),
				enums.DeviceNotExistsEnum.GetMsgKey())
		}
		return nil, "", err
	}
	return &device, s.protocolNameOf(ctx, device.ProtocolID), nil
}

// deviceForScanByCode 按自然键 code 定位设备（管理平台只知道下发用的编码）。
//
// 编码为空或查不到都按"设备不存在"处理：空编码在网关里是旧数据的可能值，
// 拿它去匹配只会命中一台不该被更新的设备。
func (s *DeviceScanService) deviceForScanByCode(ctx context.Context, code string) (*po.Device, string, error) {
	code = strings.TrimSpace(code)
	if code == "" {
		return nil, "", appError.NewAppErrorCtx(
			enums.DeviceScanParamInvalidEnum.GetCode(),
			"设备编码不能为空",
			enums.DeviceScanParamInvalidEnum.GetMsgKey())
	}
	var device po.Device
	if err := s.sqliteDB.WithContext(ctx).Where("code = ?", code).First(&device).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, "", appError.NewAppErrorCtx(
				enums.DeviceNotExistsEnum.GetCode(),
				"网关上不存在该设备编码，请先下发设备配置",
				enums.DeviceNotExistsEnum.GetMsgKey())
		}
		return nil, "", err
	}
	return &device, s.protocolNameOf(ctx, device.ProtocolID), nil
}

// protocolNameOf 把协议行 id 解析成协议名；查不到返回空串（由能力检查统一报不支持）。
func (s *DeviceScanService) protocolNameOf(ctx context.Context, protocolID string) string {
	if protocolID == "" {
		return ""
	}
	var protocol po.IotProtocol
	if err := s.sqliteDB.WithContext(ctx).Where("id = ?", protocolID).First(&protocol).Error; err == nil {
		return protocol.Name
	}
	return ""
}

// protocolSupportsPointScan 现算该协议是否声明了点位扫描能力。
//
// 每次遍历 m.plugins 现算，不建缓存表 —— 理由与 pluginhost.DiscoverySpecs 的
// 注释一模一样（registrationSig 不含 PointDiscovery，缓存必然陈旧）。
func (s *DeviceScanService) protocolSupportsPointScan(protocolName string) bool {
	if s.manager == nil || protocolName == "" {
		return false
	}
	for _, pd := range s.manager.PointDiscoverySpecs() {
		if pd.Protocol == protocolName {
			return true
		}
	}
	return false
}

// pointScanError 把点位扫描的失败映射成业务错误码。
//
// 与 scanError 同一套取舍：能归到"未启用/不支持该协议"这两个**用户能采取动作**
// 的分支就归过去，其余一律走 40026 并把根因原样带出来 —— 点扫的失败原因几乎全是
// 动态的（"设备正忙：采集正在读取这台设备"、"该串口配给了别的设备"），
// 回一句固定的译文等于把最有用的信息丢掉。
//
// **不复用 buildPingFailMessage**：它的前缀硬编码为"测试连接失败"，在点扫场景里
// 是错的（用户点的是"自动发现点位"）。但"这台设备的目标现在被谁占着"那段提示
// 复用包级的 busyHintFor（串口占用 + 以太网目标正被采集）—— 两个入口的答案必须一致，
// 而它也是"串口被谁占着"这个问题的唯一一份实现。
func (s *DeviceScanService) pointScanError(ctx context.Context, scanErr error, protocolJSON string) error {
	if errors.Is(scanErr, pluginhost.ErrPointDiscoveryUnsupported) {
		return appError.NewAppError(enums.DevicePointScanUnsupportedEnum.GetCode(),
			enums.DevicePointScanUnsupportedEnum.GetMessage())
	}
	if errors.Is(scanErr, context.DeadlineExceeded) {
		return appError.NewAppError(enums.DeviceScanParamInvalidEnum.GetCode(),
			"扫描超时（可能范围太大或链路太慢）："+scanErr.Error())
	}

	msg := "扫描失败：" + scanErr.Error()
	return appError.NewAppError(enums.DeviceScanParamInvalidEnum.GetCode(),
		msg+busyHintFor(s.collector, protocolJSON))
}

// pointSpecVO 把插件契约的扫描规格投影成响应 VO。
func pointSpecVO(spec driver.PointDiscoverySpec) *vo.PointSpecVO {
	return &vo.PointSpecVO{
		Title:                 spec.Title,
		Description:           spec.Description,
		TitleI18n:             spec.TitleI18n,
		DescriptionI18n:       spec.DescriptionI18n,
		PointFormJSON:         spec.FormJSON,
		DefaultCommonDataType: spec.DefaultCommonDataType,
		MaxBudgetMs:           spec.MaxBudgetMs,
		// 实际生效的预算 = min(插件声明, 网关上限)。前端显示的是这一个 ——
		// 插件声明的值可以比它大三倍（Modbus 20000 → 7000），显示声明值会让
		// 用户按 20 秒预估，实际 7 秒就停了。钳制逻辑仍然只有 pluginhost 一处。
		EffectiveBudgetMs: pluginhost.BudgetWithinGatewayCapMs(spec.MaxBudgetMs),
		MaxPoints:         spec.MaxPoints,
	}
}

// pointScanResultVO 把插件契约的扫描结果投影成响应 VO。
//
// 逐字段搬运而不是直接透传契约类型：前端只该看到网关这一层的稳定形状，
// 而 driver.PointScanResult 会随插件版本演进（与 ScanJobVO 的取舍一致）。
func pointScanResultVO(protocolName string, r *driver.PointScanResult) *vo.PointScanResultVO {
	out := &vo.PointScanResultVO{
		Protocol:              protocolName,
		Points:                make([]vo.PointScanPointVO, 0, len(r.Points)),
		AssumedCommonDataType: r.AssumedCommonDataType,
		Scanned:               r.Scanned,
		Readable:              r.Readable,
		Unreadable:            r.Unreadable,
		Truncated:             r.Truncated,
		Incomplete:            r.Incomplete,
		ElapsedMs:             r.ElapsedMs,
	}
	for i := range r.Points {
		p := &r.Points[i]
		out.Points = append(out.Points, vo.PointScanPointVO{
			Name:           p.Name,
			Value:          p.Value,
			Label:          p.Label,
			CommonDataType: p.CommonDataType,
			Note:           p.Note,
		})
	}
	return out
}
