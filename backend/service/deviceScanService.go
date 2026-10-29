// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package service

import (
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
	"iot-gateway/collector"
	"iot-gateway/devicescan"
	"iot-gateway/enums"
	"iot-gateway/model/dto"
	"iot-gateway/model/po"
	"iot-gateway/model/vo"
)

// DeviceScanService 扫描发现服务：**局域网设备扫描**与**设备点位范围扫描**。
//
// 两件事共用一个服务，是因为它们绑在同一个页面族、同一个总开关
// （deviceScan.enabled）上，而且**错误码大量复用**（只有"不支持点扫"单独一个码）。
// 拆成两个服务只会让错误映射出现第二份实现，进而在两个入口对同一种失败给出
// 不同的答复。实现在两个文件里：本文件管局域网扫描，devicePointScanService.go
// 管点位扫描。
//
// 两层职责，分开是因为它们的失败语义完全不同：
//   - 扫描任务的发起/查询/取消 —— 全部委托给 devicescan.Engine，这里只做
//     错误码映射与 VO 转换；
//   - 把扫描结果落成 device / device_address 行 —— 与 DeviceService.ImportDevices
//     同源的"逐条失败不中断整批"语义，复用 vo.ExcelImportResultVO 契约。
type DeviceScanService struct {
	sqliteDB *gorm.DB
	engine   *devicescan.Engine
	// manager 点位扫描的能力来源与调用出口。生产实现是同一个
	// *pluginhost.Manager，但 devicescan.Engine 只暴露了 DiscoverySpecs，
	// 点扫要走 PointDiscoverySpecs / DiscoverPoints（见 PointScanSource）。
	manager PointScanSource
	// collector 只为拼"这个串口当前被谁占着"的提示（点扫失败时用）。
	// 与 DeviceService 共用包级的 serialPortHolders，不各写一份。
	collector *collector.Engine
}

// NewDeviceScanService 创建扫描发现服务
func NewDeviceScanService(sqliteDB *gorm.DB, engine *devicescan.Engine,
	manager PointScanSource, collector *collector.Engine) *DeviceScanService {
	return &DeviceScanService{sqliteDB: sqliteDB, engine: engine, manager: manager, collector: collector}
}

// Capabilities 返回扫描页初始化所需的全部信息（协议 / 网段 / 开关）。
func (s *DeviceScanService) Capabilities(ctx context.Context) (*vo.ScanCapabilitiesVO, error) {
	out := &vo.ScanCapabilitiesVO{
		Enabled:    s.engine.Enabled(),
		Protocols:  []vo.ScanProtocolVO{},
		Subnets:    []vo.ScanSubnetVO{},
		MaxTargets: s.engine.MaxTargets(),
	}

	// 协议行 id 从库里补：插件只知道协议名，前端建完设备后要跳设备列表、
	// 以及导入时定位协议行都需要 iot_protocol 的主键。
	// 查不到不是错误（协议行可能因 disableProtocolRowProvisioning 缺失），
	// 此时 ProtocolID 留空，前端据此把该项标成不可用 —— 见 Start 的说明。
	idByName := map[string]string{}
	var protocols []po.IotProtocol
	if err := s.sqliteDB.WithContext(ctx).Select("id", "name").Find(&protocols).Error; err != nil {
		return nil, err
	}
	for _, p := range protocols {
		idByName[p.Name] = p.ID
	}

	for _, pd := range s.engine.Specs() {
		out.Protocols = append(out.Protocols, vo.ScanProtocolVO{
			Name:            pd.Protocol,
			ProtocolID:      idByName[pd.Protocol],
			Title:           pd.Spec.Title,
			Description:     pd.Spec.Description,
			TitleI18n:       pd.Spec.TitleI18n,
			DescriptionI18n: pd.Spec.DescriptionI18n,
			DefaultPort:     pd.Spec.DefaultPort,
			TargetMode:      string(pd.Spec.TargetMode),
			ScanFormJSON:    pd.Spec.FormJSON,
		})
	}
	for _, sn := range s.engine.Subnets() {
		out.Subnets = append(out.Subnets, vo.ScanSubnetVO{
			Iface:     sn.Iface,
			CIDR:      sn.CIDR,
			LocalIP:   sn.LocalIP,
			Broadcast: sn.Broadcast,
			HostCount: sn.HostCount,
		})
	}
	return out, nil
}

// Start 发起一次扫描，登记任务即返回（不等扫描结束）。
func (s *DeviceScanService) Start(ctx context.Context, req *dto.StartDeviceScanDTO) (*vo.ScanJobVO, error) {
	snap, err := s.engine.Submit(devicescan.SubmitRequest{
		Protocol: req.Protocol,
		Options:  req.Options,
		Subnets:  req.Subnets,
		Port:     req.Port,
	})
	if err != nil {
		return nil, scanError(err)
	}
	return scanJobVO(snap), nil
}

// Status 查询扫描任务。任务过期（只保留在内存、TTL 到期或网关重启过）
// 返回"任务不存在"，前端据此停止轮询。
//
// withDevices 为 false 时只回进度与计数，**不带设备与错误清单** —— 前端轮询
// 走这条路，理由见 dto.ScanJobQueryDTO.WithDevices。
func (s *DeviceScanService) Status(ctx context.Context, jobID string, withDevices bool) (*vo.ScanJobVO, error) {
	var (
		snap *devicescan.Snapshot
		err  error
	)
	if withDevices {
		snap, err = s.engine.Status(jobID)
	} else {
		snap, err = s.engine.Progress(jobID)
	}
	if err != nil {
		return nil, scanError(err)
	}
	return scanJobVO(snap), nil
}

// Continue 续扫当前任务的下一批目标。
//
// 与 Start 的区别只是"不新建任务"：协议、选项、网段、端口与已发现的结果都在
// 服务端持有，客户端只需要任务 id。同一时刻仍只允许一个任务在跑（续扫时若上一批
// 在飞探测还没收尾，返回"已有扫描任务在运行"）。
func (s *DeviceScanService) Continue(ctx context.Context, jobID string) (*vo.ScanJobVO, error) {
	snap, err := s.engine.Continue(jobID)
	if err != nil {
		return nil, scanError(err)
	}
	return scanJobVO(snap), nil
}

// Cancel 取消扫描任务。已结束的任务取消是幂等的成功。
func (s *DeviceScanService) Cancel(ctx context.Context, jobID string) error {
	if err := s.engine.Cancel(jobID); err != nil {
		return scanError(err)
	}
	return nil
}

// CreateDevices 把勾选的扫描结果批量建成 device 行。
//
// 逐条失败不中断整批，返回契约复用 vo.ExcelImportResultVO（与 Excel 导入、
// 插件上传共用同一套前端组件）。范本是 DeviceService.ImportDevices，
// 逐行校验也复用了同一个 validateImportRow 纯函数 —— 两处规则必须一致，
// 否则"从扫描建设备"与"从 Excel 建设备"会对同一份数据给出不同判定。
//
// **protocolJson 由服务端从任务快照里取，不采信客户端传入的值。**
// 它是跨信任边界的数据（决定网关去连哪个地址），而任务快照本来就在本进程内。
// 即便取了，也要过一道 json.Valid：插件给的东西同样不可假定合法。
//
// 两类"不建"的行，区别只在要不要用户确认：
//   - **硬失败**：撞名、协议行缺失、连接配置不合法 —— 重试也不会变，用户得改。
//   - **软提示**：扫描地址与库内某台设备相同（code=addressExists）——
//     带上 ConfirmDuplicate 重提就能建成。理由见 scanIdentityOf。
func (s *DeviceScanService) CreateDevices(ctx context.Context, req *dto.CreateDevicesFromScanDTO) (*vo.ExcelImportResultVO, error) {
	snap, err := s.engine.Status(req.JobID)
	if err != nil {
		return nil, scanError(err)
	}
	if snap.State == devicescan.JobRunning {
		// 边扫边建会导致"同一次扫描的两批结果"从两个快照里来，
		// 且用户看到的列表还在变。要求先等它结束（或取消）。
		return nil, appError.NewAppErrorCtx(
			enums.DeviceScanBusyEnum.GetCode(),
			"扫描还在进行中，请等它结束后再添加设备",
			enums.DeviceScanBusyEnum.GetMsgKey())
	}

	// 预加载库内设备的名字（去重）与扫描身份签名（地址重复提示）与协议名 → ID 映射。
	// 名字那一半与 ImportDevices 同源。
	var rows []po.Device
	if err := s.sqliteDB.WithContext(ctx).Model(&po.Device{}).
		Select("name", "scan_identity").Find(&rows).Error; err != nil {
		return nil, err
	}
	protocolMap := make(map[string]string)
	var protocols []po.IotProtocol
	if err := s.sqliteDB.WithContext(ctx).Select("id", "name").Find(&protocols).Error; err != nil {
		return nil, err
	}
	for _, p := range protocols {
		protocolMap[p.Name] = p.ID
	}

	result := &vo.ExcelImportResultVO{Errors: []vo.ExcelImportErrorVO{}}
	existing := make(map[string]bool, len(rows))
	// takenIdentity 签名 → 占用它的设备名（只为人读的提示文案）。
	// 空签名不入表：那是"不是扫描建的"或"建完改过配置"的标记，不参与比对 ——
	// 拿一群空串互相撞会把毫不相干的设备报成重复。
	//
	// 它在事务外读、事务内边建边补（见下面的 takenIdentity[identity] = name），
	// 因此**只是提示而不是判据**：两个并发的建库请求理论上可以都通过这道检查。
	// 这是有意的 —— 地址重复本来就允许用户确认后放行，这里没有唯一索引兜底，
	// 也不该有（理由见 po.Device.ScanIdentity）。
	takenIdentity := make(map[string]string, len(rows))
	for _, r := range rows {
		existing[r.Name] = true
		if r.ScanIdentity != "" {
			takenIdentity[r.ScanIdentity] = r.Name
		}
	}
	seen := make(map[string]bool)

	// 整体事务提交；单条插入失败只记明细，不回滚其余条 ——
	// 与 excelutil.Import 完全一致的语义（部分成功是预期行为）。
	now := time.Now().Format("2006-01-02 15:04:05")
	_ = s.sqliteDB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		for i, item := range req.Items {
			result.Total++
			// Row 沿用 Excel 导入"从 1 计"的语义，这里就是第几条勾选。
			// 前端只把它当定位符，不要求与某个真实行号对应。
			row := i + 1

			if item.Index < 0 || item.Index >= len(snap.Devices) {
				result.Errors = append(result.Errors, vo.ExcelImportErrorVO{
					Row: row, Name: item.Name, Reason: "扫描结果已失效（下标越界），请重新扫描",
				})
				continue
			}
			dev := snap.Devices[item.Index]

			if reason := validateScanProtocolJSON(dev.ProtocolJSON); reason != "" {
				result.Errors = append(result.Errors, vo.ExcelImportErrorVO{
					Row: row, Name: item.Name, Reason: reason,
				})
				continue
			}

			name := item.Name
			if name == "" {
				name = defaultDeviceName(dev)
			}
			// 与 Excel 导入共用同一套逐行规则（名称非空/长度、协议存在）。
			if reason := validateImportRow(name, snap.Protocol, protocolMap); reason != "" {
				result.Errors = append(result.Errors, vo.ExcelImportErrorVO{
					Row: row, Name: name, Reason: reason,
				})
				continue
			}
			// 库内与批内双重去重：device.name 有唯一索引，撞名的整条会入库失败，
			// 提前判出来能给出"名称已存在"而不是一条 SQLite 约束错误。
			if existing[name] || seen[name] {
				result.Errors = append(result.Errors, vo.ExcelImportErrorVO{
					Row: row, Name: name, Reason: enums.DeviceExistsEnum.GetMessage(),
				})
				continue
			}

			// 地址重复：**软提示，不拦**。用户确认后带上 ConfirmDuplicate
			// 重提同一批即可建成（见 dto.CreateDevicesFromScanDTO 的说明）。
			//
			// 放在撞名之后判：撞名是硬失败（device.name 有唯一索引兜底，改个名字
			// 才能过），先报地址重复会让用户确认一遍才发现还得改名 —— 白跑一趟。
			identity := scanIdentityOf(snap.Protocol, dev)
			if identity != "" && !req.ConfirmDuplicate {
				if owner, ok := takenIdentity[identity]; ok {
					result.Errors = append(result.Errors, vo.ExcelImportErrorVO{
						Row: row, Name: name, Code: vo.ExcelImportErrCodeAddressExists,
						Reason: fmt.Sprintf("地址 %s 上已有设备「%s」",
							scanAddressLabel(dev), owner),
					})
					continue
				}
			}

			record := po.Device{
				Name:         name,
				ProtocolID:   protocolMap[snap.Protocol],
				ProtocolJSON: dev.ProtocolJSON,
				Description:  item.Description,
				Status:       1,
				CreatedAt:    now,
				UpdatedAt:    now,
				ScanIdentity: identity,
			}
			if err := tx.Create(&record).Error; err != nil {
				result.Errors = append(result.Errors, vo.ExcelImportErrorVO{
					Row: row, Name: name, Reason: "入库失败：" + err.Error(),
				})
				continue
			}
			existing[name] = true
			seen[name] = true
			// 批内也要记：同一批里两条结果指向同一个地址时，后一条同样要提示。
			if identity != "" {
				takenIdentity[identity] = name
			}
			result.Success++
		}
		return nil
	})

	result.Failed = len(result.Errors)
	return result, nil
}

// validateScanProtocolJSON 校验插件给出的连接配置。
//
// 插件是可信代码，但它给的东西不能假定合法：一份坏 JSON 会写进 device 行，
// 之后每次采集解析配置都失败，而用户看到的是"设备采不到值"，完全指不到根因。
// 空值也拒 —— 建一台 protocolJson 为空的设备没有任何意义。
func validateScanProtocolJSON(protocolJSON string) string {
	if protocolJSON == "" {
		return "扫描结果没有给出连接配置"
	}
	if !json.Valid([]byte(protocolJSON)) {
		return "扫描结果给出的连接配置不是合法 JSON"
	}
	return ""
}

// defaultDeviceName 用户没填设备名时的兜底。
//
// 站号参与命名：同一 IP 上多个站号会各扫出一条结果，只用 IP 会撞名
// （而库里 device.name 有唯一索引，撞名的整行会被判失败）。
func defaultDeviceName(dev driver.DiscoveredDevice) string {
	if dev.UnitID > 1 {
		return dev.Host + "-" + strconv.Itoa(dev.UnitID)
	}
	return dev.Host
}

// scanIdentityOf 由一条扫描结果算出**扫描来源身份签名**，用来回答"设备表里
// 是不是已经有一台设备连在这个地址上了"（结果列在 po.Device.ScanIdentity）。
//
// 为什么是「协议|主机|端口|站号」四元组，而不是只看 IP：同一个 IP 上多台设备
// 是合法且常见的 —— Modbus TCP 串口服务器后面挂多个从站（靠站号区分）、
// 多通道串口服务器的不同端口、同一台 PLC 同时开 S7 与 Modbus。只有四元组全同
// 才是真正的重复：那意味着网关会为同一个端点开两份采集任务，重复占用 PLC
// 有限的连接槽位，并把同一份数据推两遍。defaultDeviceName 的注释是同一件事的
// 另一面。
//
// 主机归一成小写并去空白：插件给的一般已经是规范形式，但大小写差异会让比对
// 静默失效，而做掉它的代价是零。
//
// 主机为空时返回空串 —— **空签名不参与比对**：插件是外部代码，它给一个没有
// 主机的结果（不该发生）时，宁可漏报也不能拿这个无意义的值去和别的空签名互撞。
func scanIdentityOf(protocol string, dev driver.DiscoveredDevice) string {
	host := strings.ToLower(strings.TrimSpace(dev.Host))
	if host == "" {
		return ""
	}
	return protocol + "|" + host + "|" + strconv.Itoa(dev.Port) + "|" + strconv.Itoa(dev.UnitID)
}

// scanAddressLabel 人读的地址标签，**只用于提示文案**（不参与判重 ——
// 判重一律走 scanIdentityOf 的签名）。站号为 0 表示该协议不适用这个概念，
// 显示出来只会让用户困惑。
func scanAddressLabel(dev driver.DiscoveredDevice) string {
	if dev.UnitID > 0 {
		return fmt.Sprintf("%s:%d#%d", dev.Host, dev.Port, dev.UnitID)
	}
	return fmt.Sprintf("%s:%d", dev.Host, dev.Port)
}

// scanError 把 devicescan 的规划期错误映射成业务错误码。
//
// 逐个 errors.Is 而不是笼统归成"参数错误"：用户需要知道是"协议不支持扫描"
// 还是"这个网段不是本机的"，两者的下一步动作完全不同（换协议 vs 改网段）。
func scanError(err error) error {
	switch {
	case errors.Is(err, devicescan.ErrDisabled):
		return appError.NewAppErrorCtx(
			enums.DeviceScanDisabledEnum.GetCode(),
			enums.DeviceScanDisabledEnum.GetMessage(),
			enums.DeviceScanDisabledEnum.GetMsgKey())
	case errors.Is(err, devicescan.ErrBusy):
		return appError.NewAppErrorCtx(
			enums.DeviceScanBusyEnum.GetCode(),
			enums.DeviceScanBusyEnum.GetMessage(),
			enums.DeviceScanBusyEnum.GetMsgKey())
	case errors.Is(err, devicescan.ErrJobNotFound):
		return appError.NewAppErrorCtx(
			enums.DeviceScanJobNotFoundEnum.GetCode(),
			enums.DeviceScanJobNotFoundEnum.GetMessage(),
			enums.DeviceScanJobNotFoundEnum.GetMsgKey())
	case errors.Is(err, devicescan.ErrUnsupportedProtocol):
		return appError.NewAppError(enums.DeviceScanUnsupportedEnum.GetCode(),
			fmt.Sprintf("%s：%s", enums.DeviceScanUnsupportedEnum.GetMessage(), err.Error()))
	case errors.Is(err, devicescan.ErrNoTargets):
		return appError.NewAppErrorCtx(
			enums.DeviceScanNoTargetsEnum.GetCode(),
			enums.DeviceScanNoTargetsEnum.GetMessage(),
			enums.DeviceScanNoTargetsEnum.GetMsgKey())
	default:
		// ErrInvalidPort / ErrUnknownSubnet / 其它：参数类问题，原样带上根因。
		// 用 NewAppError 而不是 NewAppErrorCtx —— 文案里拼了动态内容，
		// 走 i18n 只会把根因盖掉（同 buildPingFailMessage 的取舍）。
		return appError.NewAppError(enums.DeviceScanParamInvalidEnum.GetCode(),
			enums.DeviceScanParamInvalidEnum.GetMessage()+"："+err.Error())
	}
}

// scanJobVO 把引擎快照投影成响应 VO。
//
// **Devices / Errors 照抄快照的 nil 与空**：nil 表示这一份快照没带清单
// （轻量快照），响应里就是 null；空数组表示"带清单，但确实一个结果都没有"。
// 两者在客户端要触发不同的动作（保留 / 清空自己持有的那份），不能在这里
// 一起归一成空数组 —— 归一了就再也分不出来。
func scanJobVO(snap *devicescan.Snapshot) *vo.ScanJobVO {
	out := &vo.ScanJobVO{
		JobID:            snap.JobID,
		Protocol:         snap.Protocol,
		State:            string(snap.State),
		Reason:           snap.Reason,
		RequestedTargets: snap.Requested,
		PlannedTargets:   snap.Planned,
		Truncated:        snap.Truncated,
		RemainingTargets: snap.Remaining,
		Total:            snap.Total,
		Scanned:          snap.Scanned,
		ScannedTotal:     snap.ScannedTotal,
		Found:            snap.Found,
		ElapsedMs:        snap.ElapsedMs,
		EstimateMaxMs:    snap.EstimateMaxMs,
		StartedAt:        snap.StartedAt,
		EndedAt:          snap.EndedAt,
	}
	if snap.Devices == nil {
		return out
	}
	out.Devices = make([]vo.ScanDeviceVO, 0, len(snap.Devices))
	for i := range snap.Devices {
		d := &snap.Devices[i]
		dev := vo.ScanDeviceVO{
			Index:        i,
			Host:         d.Host,
			Port:         d.Port,
			UnitID:       d.UnitID,
			Name:         d.Name,
			Vendor:       d.Vendor,
			Model:        d.Model,
			Version:      d.Version,
			ProtocolJSON: d.ProtocolJSON,
		}
		for _, a := range d.Extra {
			dev.Extra = append(dev.Extra, vo.ScanAttributeVO{Name: a.Name, Value: a.Value})
		}
		out.Devices = append(out.Devices, dev)
	}
	if snap.Errors != nil {
		out.Errors = make([]vo.ScanErrorVO, 0, len(snap.Errors))
		for _, e := range snap.Errors {
			out.Errors = append(out.Errors, vo.ScanErrorVO{Host: e.Host, Reason: e.Reason})
		}
	}
	return out
}
