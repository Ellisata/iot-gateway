// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package service

import (
	"context"
	"errors"

	"iot-gateway/appError"
	"iot-gateway/enums"
	"iot-gateway/model/dto"
	"iot-gateway/model/vo"
	"iot-gateway/runtimecfg"
)

// SystemConfigService 运行期配置开关服务。
//
// 只做 VO 投影与错误码映射；校验、持久化、热应用与回滚全部在 runtimecfg.Manager。
// 这样「改了配置却没生效」的失败点只有一处。
type SystemConfigService struct {
	manager *runtimecfg.Manager
}

// NewSystemConfigService 创建运行期配置服务
func NewSystemConfigService(manager *runtimecfg.Manager) *SystemConfigService {
	return &SystemConfigService{manager: manager}
}

// List 返回全部可动态管理的开关及其当前状态。
func (s *SystemConfigService) List(ctx context.Context) (*vo.SystemConfigVO, error) {
	fields := s.manager.List()

	out := &vo.SystemConfigVO{Groups: []vo.SystemConfigGroupVO{}}
	index := make(map[string]int, len(fields))
	for _, f := range fields {
		gi, ok := index[f.Spec.Group]
		if !ok {
			gi = len(out.Groups)
			index[f.Spec.Group] = gi
			out.Groups = append(out.Groups, vo.SystemConfigGroupVO{
				Key:      f.Spec.Group,
				LabelKey: f.Spec.GroupLabelKey,
				Fields:   []vo.SystemConfigFieldVO{},
			})
		}
		out.Groups[gi].Fields = append(out.Groups[gi].Fields, vo.SystemConfigFieldVO{
			Key:            string(f.Spec.Key),
			Type:           string(f.Spec.Type),
			Value:          f.Value,
			DefaultValue:   f.DefaultValue,
			Source:         f.Source,
			Hot:            f.Hot,
			PendingRestart: f.PendingRestart,
			UpdatedBy:      f.UpdatedBy,
			UpdatedAt:      f.UpdatedAt,
			LabelKey:       f.Spec.LabelKey,
			DescKey:        f.Spec.DescKey,
		})
	}
	return out, nil
}

// Update 修改一个开关。updatedBy 来自 JWT 上下文；/agentApi 下为空。
func (s *SystemConfigService) Update(ctx context.Context, req *dto.UpdateSystemConfigDTO,
	updatedBy string) (*vo.SystemConfigUpdateVO, error) {
	key := runtimecfg.Key(req.Key)
	requiresRestart, err := s.manager.Update(ctx, key, req.Value, updatedBy)
	if err != nil {
		return nil, mapSystemConfigError(err)
	}
	return &vo.SystemConfigUpdateVO{
		Key:             req.Key,
		Value:           s.manager.Bool(key),
		RequiresRestart: requiresRestart,
	}, nil
}

// Reset 删除覆盖值，恢复 default.yaml 的默认值。
func (s *SystemConfigService) Reset(ctx context.Context, req *dto.ResetSystemConfigDTO,
	updatedBy string) (*vo.SystemConfigUpdateVO, error) {
	key := runtimecfg.Key(req.Key)
	requiresRestart, err := s.manager.Reset(ctx, key, updatedBy)
	if err != nil {
		return nil, mapSystemConfigError(err)
	}
	return &vo.SystemConfigUpdateVO{
		Key:             req.Key,
		Value:           s.manager.Bool(key),
		RequiresRestart: requiresRestart,
	}, nil
}

// mapSystemConfigError 把 runtimecfg 的错误映射成业务错误码。
//
// 值不合法 / 应用失败 / 保存失败带上根因：用户需要知道是「类型不对」还是
// 「端口被占」，笼统的「系统错误」无法指导下一步。这几条用 NewAppError
// 而不是 NewAppErrorCtx，理由同 deviceScan 的 scanError —— 拼了动态内容
// 再走 i18n 只会把根因盖掉。
func mapSystemConfigError(err error) error {
	switch {
	case errors.Is(err, runtimecfg.ErrUnknownKey):
		return appError.NewAppErrorCtx(
			enums.SystemConfigUnknownKeyEnum.GetCode(),
			enums.SystemConfigUnknownKeyEnum.GetMessage(),
			enums.SystemConfigUnknownKeyEnum.GetMsgKey())
	case errors.Is(err, runtimecfg.ErrInvalidValue):
		return appError.NewAppError(enums.SystemConfigInvalidValueEnum.GetCode(), err.Error())
	case errors.Is(err, runtimecfg.ErrApplyFailed):
		return appError.NewAppError(enums.SystemConfigApplyFailedEnum.GetCode(), err.Error())
	case errors.Is(err, runtimecfg.ErrPersist):
		return appError.NewAppError(enums.SystemConfigPersistFailEnum.GetCode(), err.Error())
	case errors.Is(err, runtimecfg.ErrClosed):
		return appError.NewAppErrorCtx(
			enums.SystemConfigClosedEnum.GetCode(),
			enums.SystemConfigClosedEnum.GetMessage(),
			enums.SystemConfigClosedEnum.GetMsgKey())
	default:
		return err
	}
}
