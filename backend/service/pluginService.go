// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package service

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"gorm.io/gorm"

	"iot-gateway/appError"
	"iot-gateway/enums"
	"iot-gateway/logger"
	"iot-gateway/model/po"
	"iot-gateway/model/vo"
	"iot-gateway/pluginhost"
)

// MaxPluginBatch 单次上传的插件文件数量上限。
//
// 上限存在的意义不是性能，而是事故半径：一次误选整个构建输出目录时，
// 宁可让用户分批来，也不要在一次请求里把几十个来路不明的可执行文件
// 一次性丢进会自动加载的目录。导出是为了让上传接口在解析阶段就能拒绝。
const MaxPluginBatch = 50

// PluginUpload 一个待安装的插件文件。
type PluginUpload struct {
	// Name 上传时的原始文件名（仅取 basename，见 controller 的解析）。
	Name string
	// Data 文件内容。RejectReason 非空时不会被读取。
	Data []byte

	// RejectReason 非空表示 controller 在读取前就已判定该文件不可用
	// （如超出单文件大小上限），服务层直接记一条失败而不做任何写入。
	//
	// 之所以在读取前判定：超限文件根本不该被读进内存，
	// 而「超出上限」这个结论从 multipart 部件头就能得出。
	RejectReason string
}

// PluginService 协议插件安装服务。
//
// 安装路径不碰数据库：插件的真源是文件系统（plugins/ 目录），
// 而 iot_protocol 行由 pluginhost 的扫描器在加载插件后自动供给
// （见 pluginhost/provision.go）。
//
// 卸载路径需要数据库，且只有这一处需要：删协议行前必须先确认没有设备
// 通过 device.protocol_id 引用它们。pluginhost 刻意不认识设备表（它只管
// 进程与注册表），这个跨领域判断放在服务层。
type PluginService struct {
	sqliteDB *gorm.DB
	manager  *pluginhost.Manager
}

// NewPluginService 创建插件安装服务
func NewPluginService(sqliteDB *gorm.DB, manager *pluginhost.Manager) *PluginService {
	return &PluginService{sqliteDB: sqliteDB, manager: manager}
}

// Info 返回网关侧的插件环境信息（平台 / 目录 / 命名规则）。
func (s *PluginService) Info(ctx context.Context) (*vo.PluginInfoVO, error) {
	dir, err := s.manager.PluginDir()
	if err != nil {
		return nil, appError.NewAppErrorCtx(
			enums.PluginDirInvalidEnum.GetCode(),
			enums.PluginDirInvalidEnum.GetMessage(),
			enums.PluginDirInvalidEnum.GetMsgKey())
	}

	return &vo.PluginInfoVO{
		OS:      runtime.GOOS,
		Arch:    runtime.GOARCH,
		Dir:     dir,
		Glob:    s.manager.PluginGlob(),
		FileExt: pluginFileExt(runtime.GOOS),
		Enabled: s.manager.Enabled(),
	}, nil
}

// OpenApiInfo 返回开放接口所需的插件环境信息（不含本机目录）。
func (s *PluginService) OpenApiInfo(ctx context.Context) (*vo.OpenApiPluginInfoVO, error) {
	info, err := s.Info(ctx)
	if err != nil {
		return nil, err
	}
	return &vo.OpenApiPluginInfoVO{
		OS:                  info.OS,
		Arch:                info.Arch,
		Glob:                info.Glob,
		FileExt:             info.FileExt,
		Enabled:             info.Enabled,
		MaxFileSize:         pluginhost.MaxPluginFileSize,
		MaxBatch:            MaxPluginBatch,
		ScanIntervalSeconds: int(s.manager.ScanInterval() / time.Second),
	}, nil
}

// OpenApiStatus 返回已加载插件的诊断快照（路径换成文件名）。
//
// 注意这是**已加载**的插件，不是插件目录里的全部文件：刚上传、尚未被下一轮
// 扫描拉起的插件不会出现在这里。管理平台应在上传后轮询本接口，直到看到目标
// sha256 或加载失败。
func (s *PluginService) OpenApiStatus() []vo.OpenApiPluginStatusVO {
	statuses := s.manager.Status()
	out := make([]vo.OpenApiPluginStatusVO, 0, len(statuses))
	for _, st := range statuses {
		out = append(out, vo.OpenApiPluginStatusVO{
			Name:             filepath.Base(st.Path),
			Protocols:        st.Protocols,
			Scope:            st.Scope,
			Version:          st.Version,
			SHA256:           st.SHA256,
			Up:               st.Up,
			Epoch:            st.Epoch,
			Upgrades:         st.Upgrades,
			UpgradedAt:       st.UpgradedAt,
			PreviousSHA256:   st.PreviousSHA256,
			LastUpgradeError: st.LastUpgradeError,
		})
	}
	return out
}

// List 返回管理端使用的已加载插件清单。
//
// 前端用它把「协议行」映射到「插件文件」：一个插件可以供给多个协议，
// 卸载的粒度是文件而不是行（见 Uninstall）。
func (s *PluginService) List(ctx context.Context) []vo.PluginStatusVO {
	statuses := s.manager.Status()
	out := make([]vo.PluginStatusVO, 0, len(statuses))
	for _, st := range statuses {
		out = append(out, vo.PluginStatusVO{
			Name:             filepath.Base(st.Path),
			Protocols:        st.Protocols,
			Scope:            st.Scope,
			Version:          st.Version,
			SHA256:           st.SHA256,
			Up:               st.Up,
			Epoch:            st.Epoch,
			Upgrades:         st.Upgrades,
			UpgradedAt:       st.UpgradedAt,
			PreviousSHA256:   st.PreviousSHA256,
			LastUpgradeError: st.LastUpgradeError,
		})
	}
	return out
}

// pluginUsage 一台仍在使用待卸载插件协议的设备。
type pluginUsage struct {
	DeviceName   string `gorm:"column:device_name"`
	ProtocolName string `gorm:"column:protocol_name"`
}

// maxUsageShown 拒绝信息里最多列出的设备数。
//
// 设备多时只列前几台：错误文案是给人看的第一落点，不是清单页；
// 总数仍然完整给出。
const maxUsageShown = 10

// Uninstall 卸载一个插件，并删除它供给的协议行。
//
// 顺序是刻意的：
//  1. 先查设备引用 —— 被引用的插件不能卸载，否则 device.protocol_id 会悬空，
//     设备在采集时被静默跳过，前端看不出任何原因；
//  2. 卸载插件文件与进程（pluginhost.Uninstall）；
//  3. 最后删协议行 —— 文件已经不在，provision 不会再把行插回来。
//
// 反过来先删行再删文件是不行的：文件删失败时下一轮扫描会重新供给，
// 协议行会以**新的 ID** 重建，期间引用旧 ID 的设备就成了孤儿。
//
// 只接受已加载插件的文件名：未加载的文件无法知道它供给哪些协议，
// 也就无法判断设备引用。这类残留文件目前仍需在网关上人工处理。
func (s *PluginService) Uninstall(ctx context.Context, file string) error {
	if !s.manager.Enabled() {
		return appError.NewAppErrorCtx(
			enums.PluginDisabledEnum.GetCode(),
			enums.PluginDisabledEnum.GetMessage(),
			enums.PluginDisabledEnum.GetMsgKey())
	}

	name := filepath.Base(strings.TrimSpace(file))
	var protocols []string
	found := false
	for _, st := range s.manager.Status() {
		if filepath.Base(st.Path) == name {
			protocols = append([]string(nil), st.Protocols...)
			found = true
			break
		}
	}
	if !found {
		return appError.NewAppErrorCtx(
			enums.PluginNotFoundEnum.GetCode(),
			enums.PluginNotFoundEnum.GetMessage(),
			enums.PluginNotFoundEnum.GetMsgKey())
	}

	var usages []pluginUsage
	if len(protocols) > 0 {
		if err := s.sqliteDB.WithContext(ctx).Table(po.Device{}.TableName()).
			Select("device.name AS device_name, iot_protocol.name AS protocol_name").
			Joins("JOIN iot_protocol ON iot_protocol.id = device.protocol_id").
			Where("iot_protocol.name IN ?", protocols).
			Order("device.name").
			Find(&usages).Error; err != nil {
			return err
		}
		if len(usages) > 0 {
			return appError.NewAppError(
				enums.PluginInUseEnum.GetCode(),
				formatPluginUsage(name, usages))
		}
	}

	if err := s.manager.Uninstall(name); err != nil {
		switch {
		case errors.Is(err, pluginhost.ErrPluginNotFound):
			return appError.NewAppErrorCtx(
				enums.PluginNotFoundEnum.GetCode(),
				enums.PluginNotFoundEnum.GetMessage(),
				enums.PluginNotFoundEnum.GetMsgKey())
		case errors.Is(err, pluginhost.ErrPluginDisabled):
			return appError.NewAppErrorCtx(
				enums.PluginDisabledEnum.GetCode(),
				enums.PluginDisabledEnum.GetMessage(),
				enums.PluginDisabledEnum.GetMsgKey())
		default:
			logger.Error("plugin: 卸载 %s 失败: %v", name, err)
			return appError.NewAppError(
				enums.PluginUninstallFailEnum.GetCode(),
				fmt.Sprintf("%s：%v", enums.PluginUninstallFailEnum.GetMessage(), err))
		}
	}

	if len(protocols) > 0 {
		res := s.sqliteDB.WithContext(ctx).
			Where("name IN ?", protocols).
			Delete(&po.IotProtocol{})
		if res.Error != nil {
			// 插件文件已经删掉，协议行留在库里只会显示成「有行无驱动」的
			// 僵尸协议。把真实状态写进错误里，避免用户以为整单失败。
			logger.Error("plugin: 插件 %s 已卸载，但清理协议行失败: %v", name, res.Error)
			return appError.NewAppError(
				enums.PluginUninstallFailEnum.GetCode(),
				fmt.Sprintf("%s：插件已卸载，但协议行清理失败：%v",
					enums.PluginUninstallFailEnum.GetMessage(), res.Error))
		}
	}

	logger.Info("plugin: 已卸载插件 %s，删除协议行 %s", name, strings.Join(protocols, ", "))
	return nil
}

// formatPluginUsage 把设备引用整理成一条可照做的拒绝文案。
func formatPluginUsage(name string, usages []pluginUsage) string {
	shown := usages
	if len(shown) > maxUsageShown {
		shown = shown[:maxUsageShown]
	}
	parts := make([]string, 0, len(shown))
	for _, u := range shown {
		parts = append(parts, fmt.Sprintf("%s（%s）", u.DeviceName, u.ProtocolName))
	}
	tail := ""
	if len(usages) > len(shown) {
		tail = fmt.Sprintf(" 等共 %d 台", len(usages))
	}
	return fmt.Sprintf("%s：插件 %s 仍被设备使用：%s%s。请先删除这些设备或改用其它协议。",
		enums.PluginInUseEnum.GetMessage(), name, strings.Join(parts, "、"), tail)
}

// ImportPlugins 批量安装插件，返回逐文件成败明细。
//
// 批次级的三种情况整单拒绝（重名、数量超限、开关关闭）—— 它们都不是
// 「某个文件不合格」，而是「这次请求本身不该被执行」。其余按文件独立校验，
// 允许部分成功：一个不合平台的二进制不该挡住同批次里其它能用的插件。
func (s *PluginService) ImportPlugins(ctx context.Context, files []PluginUpload) (*vo.PluginImportResultVO, error) {
	if !s.manager.Enabled() {
		return nil, appError.NewAppErrorCtx(
			enums.PluginDisabledEnum.GetCode(),
			enums.PluginDisabledEnum.GetMessage(),
			enums.PluginDisabledEnum.GetMsgKey())
	}
	if len(files) == 0 {
		return nil, appError.NewAppErrorCtx(
			enums.PluginNoFileEnum.GetCode(),
			enums.PluginNoFileEnum.GetMessage(),
			enums.PluginNoFileEnum.GetMsgKey())
	}
	if len(files) > MaxPluginBatch {
		return nil, appError.NewAppErrorCtx(
			enums.PluginTooManyEnum.GetCode(),
			fmt.Sprintf("%s（最多 %d 个，本次 %d 个）", enums.PluginTooManyEnum.GetMessage(), MaxPluginBatch, len(files)),
			enums.PluginTooManyEnum.GetMsgKey())
	}
	// 重名必须在写入前拦下：同名文件后者会覆盖前者，最终只有一个能生效，
	// 而结果里却报两次成功 —— 用户会以为自己装了两个插件。
	if dup := findDuplicatePluginName(files); dup != "" {
		return nil, appError.NewAppErrorCtx(
			enums.PluginDuplicateEnum.GetCode(),
			fmt.Sprintf("%s：%s", enums.PluginDuplicateEnum.GetMessage(), dup),
			enums.PluginDuplicateEnum.GetMsgKey())
	}

	dir, err := s.manager.PluginDir()
	if err != nil {
		return nil, appError.NewAppErrorCtx(
			enums.PluginDirInvalidEnum.GetCode(),
			enums.PluginDirInvalidEnum.GetMessage(),
			enums.PluginDirInvalidEnum.GetMsgKey())
	}

	result := &vo.PluginImportResultVO{
		Total: len(files),
		Dir:   dir,
		Hint: fmt.Sprintf("插件将在约 %s 内被目录扫描发现并加载；"+
			"同名文件会作为已加载插件的热升级替换，该插件下的设备会自动重连。",
			pluginScanHint(s.manager)),
	}

	// 按文件名排序，让结果顺序稳定（multipart 的部件顺序不保证）。
	sorted := make([]PluginUpload, len(files))
	copy(sorted, files)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Name < sorted[j].Name })

	for _, f := range sorted {
		if f.RejectReason != "" {
			result.Failed++
			result.Errors = append(result.Errors, vo.PluginImportErrorVO{
				Name:   f.Name,
				Reason: f.RejectReason,
			})
			continue
		}
		switch err := s.manager.InstallPlugin(f.Name, f.Data); {
		case err == nil:
			result.Success++
			result.Pending++
		case errors.Is(err, pluginhost.ErrPluginUnchanged):
			// 不是失败：目标文件的意图已经达成，只是没有产生写入。
			result.Success++
			result.Warnings = append(result.Warnings,
				fmt.Sprintf("%s：内容与现有文件完全相同，已跳过写入", f.Name))
		default:
			result.Failed++
			result.Errors = append(result.Errors, vo.PluginImportErrorVO{
				Name:   f.Name,
				Reason: pluginInstallReason(err),
			})
			logger.Warn("pluginhost: 插件 %s 上传失败: %v", f.Name, err)
		}
	}

	return result, nil
}

// pluginInstallReason 把安装错误转成给用户看的原因。
//
// pluginhost 的校验错误本身就是逐条写给人看的（可直接照做的文案，
// 如「该文件是 linux/ELF 格式，本网关运行于 windows/amd64」），原样透出即可。
func pluginInstallReason(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// findDuplicatePluginName 返回第一个重复的文件名；无重复返回空串。
//
// 按 basename 比较且**不区分大小写**：Windows 的文件系统本就不区分，
// 大小写不同的两个名字会互相覆盖。
func findDuplicatePluginName(files []PluginUpload) string {
	seen := make(map[string]struct{}, len(files))
	for _, f := range files {
		key := filepath.Base(f.Name)
		lower := strings.ToLower(key)
		if _, ok := seen[lower]; ok {
			return key
		}
		seen[lower] = struct{}{}
	}
	return ""
}

// pluginFileExt 目标平台要求的插件扩展名。
func pluginFileExt(goos string) string {
	if goos == "windows" {
		return ".exe"
	}
	return ""
}

// pluginScanHint 描述扫描间隔，让用户对生效时间有预期。
func pluginScanHint(m *pluginhost.Manager) string {
	return m.ScanInterval().String()
}
