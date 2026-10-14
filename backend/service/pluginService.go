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

	"iot-gateway/appError"
	"iot-gateway/enums"
	"iot-gateway/logger"
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
// 不碰数据库：插件的真源是文件系统（plugins/ 目录），
// 而 iot_protocol 行由 pluginhost 的扫描器在加载插件后自动供给
// （见 pluginhost/provision.go），不需要这一层代劳。
type PluginService struct {
	manager *pluginhost.Manager
}

// NewPluginService 创建插件安装服务
func NewPluginService(manager *pluginhost.Manager) *PluginService {
	return &PluginService{manager: manager}
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
