package service

import (
	"context"

	"iot-gateway/appError"
	"iot-gateway/collector"
	"iot-gateway/enums"
	"iot-gateway/model/vo"
)

// CollectionService 采集编排服务
type CollectionService struct {
	engine *collector.Engine
}

// NewCollectionService 创建采集编排服务
func NewCollectionService(engine *collector.Engine) *CollectionService {
	return &CollectionService{
		engine: engine,
	}
}

// StartCollection 启动采集（项目本身作为网关，采集所有设备）
func (s *CollectionService) StartCollection(ctx context.Context) error {
	if err := s.engine.StartCollection(); err != nil {
		return appError.NewAppErrorCtx(enums.CollectionStartFailEnum.GetCode(), "启动采集失败: "+err.Error(), enums.CollectionStartFailEnum.GetMsgKey())
	}
	return nil
}

// RefreshCollection 热刷新采集配置（新增设备/点位无需重启）
func (s *CollectionService) RefreshCollection(ctx context.Context) error {
	if err := s.engine.Refresh(); err != nil {
		return appError.NewAppErrorCtx(enums.CollectionStartFailEnum.GetCode(), "热刷新采集配置失败: "+err.Error(), enums.CollectionStartFailEnum.GetMsgKey())
	}
	return nil
}

// StopCollection 停止采集
func (s *CollectionService) StopCollection(ctx context.Context) error {
	if err := s.engine.StopCollection(); err != nil {
		return appError.NewAppErrorCtx(enums.CollectionStopFailEnum.GetCode(), "停止采集失败: "+err.Error(), enums.CollectionStopFailEnum.GetMsgKey())
	}
	return nil
}

// GetCollectionStatus 获取采集状态。
// 引擎全局仅一个采集任务，故返回单个状态对象（而非切片）；
// 采集未启动时返回默认状态（running=false、时间为空、计数为 0），前端无需处理空数组。
func (s *CollectionService) GetCollectionStatus(ctx context.Context) vo.CollectionStatusVO {
	statuses := s.engine.GetStatus()
	if len(statuses) == 0 {
		return vo.CollectionStatusVO{}
	}
	st := statuses[0]
	return vo.CollectionStatusVO{
		Running:         st.Running,
		LastPollTime:    st.LastPollTime,
		LastSuccessTime: st.LastSuccessTime,
		ErrorCount:      st.ErrorCount,
		DeviceCount:     st.DeviceCount,
		AddressCount:    st.AddressCount,
	}
}
