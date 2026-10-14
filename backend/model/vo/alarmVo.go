// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package vo

// AlarmVO 报警响应
type AlarmVO struct {
	ID             string `json:"id"`
	TargetID       string `json:"targetId"`
	TargetName     string `json:"targetName"`
	TargetType     string `json:"targetType"`    // device | channel | point | point_threshold
	AlarmType      string `json:"alarmType"`     // offline | quality | threshold | recover
	AlarmTypeName  string `json:"alarmTypeName"` // 断联报警 | 采集质量报警 | 超限报警 | 恢复记录
	Level          string `json:"level"`
	Content        string `json:"content"`
	Status         string `json:"status"`     // active | cleared
	StatusName     string `json:"statusName"` // 未恢复 | 已恢复
	FirstOccurTime string `json:"firstOccurTime"`
	LastOccurTime  string `json:"lastOccurTime"`
	ClearTime      string `json:"clearTime"`
	CreatedAt      string `json:"createdAt"`
	UpdatedAt      string `json:"updatedAt"`
}

// PointThresholdRecordVO 越限记录响应（越限样本明细）。
//
// 与 AlarmVO 是两件事：报警是去抖后的状态机（连续 N 轮才报），本表按**原始越限**
// 逐条留证（同一连续越限段内节流）—— 因此 1~2 轮的尖峰只在这里看得到。
type PointThresholdRecordVO struct {
	ID          string `json:"id"`
	DeviceID    string `json:"deviceId"`
	DeviceName  string `json:"deviceName"`
	AddressID   string `json:"addressId"`
	AddressName string `json:"addressName"`
	Value       string `json:"value"`
	Kind        string `json:"kind"`
	// Bound 命中的是条件里的哪一部分：range 为 high/low，其余算子即算子名本身。
	Bound     string `json:"bound"`
	BoundName string `json:"boundName"` // 超上限 | 低于下限 | 大于 | 等于 | 属于 …
	// ThresholdOp / ThresholdValue 是写入时的快照（ThresholdValue 为操作数的 JSON 数组），
	// 改配置后历史记录仍可自证。
	ThresholdOp    string `json:"thresholdOp"`
	ThresholdValue string `json:"thresholdValue"`
	OccurredAt     string `json:"occurredAt"`
}

// ==================== 可靠性统计 VO ====================
//
// **口径是"报警口径"**：只有去抖后落库的故障段计入（见 alarm.Tracker 的连续 N 轮判定）。
// 低于去抖阈值的抖动不产生报警行，因此不计入 —— 算出来的可用率会略高于"真实"可用率。
// 这是与报警定义保持一致的有意取舍（运维认为不值得叫人的抖动不是事故），
// 完整论证见 specs/alarm.md §3。拿它与第三方 uptime 对比时差距就出在这里。

// DeviceReliabilityVO 单台设备在窗口内的可靠性指标。
//
// **窗口内一次故障都没有的设备不会出现在结果里**（等价于可用率 100%，但不在榜上）——
// 结果集只覆盖"出过故障的设备"，所以它可以直接当"最差设备排行"用，但不是全量设备清单。
//
// 带 limit 请求时 Ranking 仍是**全局名次**（截断发生在排序之后、编号之前），
// 所以 limit=10 拿到的就是"最差的前 10 台"，第 1 名是全网最差那台。
type DeviceReliabilityVO struct {
	DeviceID   string `json:"deviceId"`
	DeviceName string `json:"deviceName"` // 取自报警行的名称快照（设备已删除也仍有值）
	// Episodes 窗口内的故障段数（报警行数，不含 recover 历史行）。
	Episodes int `json:"episodes"`
	// DownSeconds 窗口内累计停机秒数，**四舍五入到整秒**。跨越窗口边界的段按重叠部分计入；
	// 尚未恢复的段算到窗口终点。
	DownSeconds float64 `json:"downSeconds"`
	// MttrSeconds 平均修复时长（秒），**四舍五入到整秒**，只统计**在窗口内开始且已恢复**的段
	// —— 跨越窗口起点的段起点不可见，算进去会把修复时长虚增。
	// 无已完成故障时为 -1（不是 0：真实段长不可能为 0）。
	MttrSeconds float64 `json:"mttrSeconds"`
	// Availability 窗口内的可用率，**百分数 0~100，四舍五入到两位小数**（如 99.17）。
	// **分母是整窗口**，未扣除网关自身停机（网关停机期间不轮询、不产生报警，
	// 那段时间在所有设备上都被算作在线）。需要扣的话请自行用 WindowSeconds 重算
	// —— 网关观测不到自己的停机区间。
	//
	// 取整只发生在输出前：排序与 Ranking 用的是取整前的原始可用率，
	// 停机只差几秒的两台设备不会因取整并列。
	Availability float64 `json:"availability"`
	// WindowSeconds 窗口总秒数，供调用方按自己的分母重算可用率。
	WindowSeconds float64 `json:"windowSeconds"`
	// Ranking 按可用率升序的排名，1 = 最差。同可用率时按设备 ID 稳定排序。
	Ranking int `json:"ranking"`
}

// DeviceReliabilityReportVO 设备可靠性统计的响应体：网关身份 + 排行。
//
// **为什么包一层而不直接返回数组**：这份排行是给外部系统**跨网关汇总**用的
// （把各网关的榜单拼成一张全厂表），而裸数组里没有任何字段能回答"这一行是哪台网关的"。
// 身份放在外层而不是每行各带一份 —— 一次响应只可能来自一台网关，逐行重复同一串
// 值既冗余，也让调用方多出一处"本不该不一致却可能不一致"的地方。
// 形态与 RealtimeSnapshotVO 一致（上下文在外、明细在 records 里）。
type DeviceReliabilityReportVO struct {
	// GatewayID / GatewayName 网关自身身份（iot_gateway 单行表）。
	//
	// 身份行缺失时退回**空串，而不是让整份报表失败**：身份是标注不是数据，而身份
	// 播种失败在 InitDB 里本就是刻意非致命的（同一个取舍见 discovery.Announcer，
	// 那边把"身份行缺失"当合法状态、只暂停广播）。为一个标签把整份排行一起丢掉，
	// 等于把可用率押在一条与统计无关的行上。调用方见空串即知本行未被标注。
	GatewayID   string `json:"gatewayId"`
	GatewayName string `json:"gatewayName"`
	// Records 排序与 Ranking 的语义见 DeviceReliabilityVO；无故障设备时为空数组
	// （不是 null）。
	Records []DeviceReliabilityVO `json:"records"`
}

// PointAbnormalVO 单个点位在窗口内的异常指标。
//
// 质量异常与超限**分开计数**，不合并成一个"非正常次数"：前者要找运维（地址/解析问题），
// 后者可能只是工艺波动，混在一起值班的人没法据此行动。
type PointAbnormalVO struct {
	// TargetID 点位报警的 target_id，即 device_address.id。质量与超限共用这一个键。
	TargetID string `json:"targetId"`
	// TargetName 「设备名.点位名」拼串，**直接用于展示**。
	// 它是名称快照，设备删除后仍可读；但它是个拼串，**不要拆** —— 名字含点号时会歧义。
	TargetName string `json:"targetName"`
	// DeviceID 所属设备（device.id）。分组 / 过滤用这个，别用 TargetName 拆。
	DeviceID string `json:"deviceId"`
	// QualityEpisodes / ThresholdEpisodes 窗口内的采集质量异常段数 / 超限段数。
	QualityEpisodes   int `json:"qualityEpisodes"`
	ThresholdEpisodes int `json:"thresholdEpisodes"`
	TotalEpisodes     int `json:"totalEpisodes"`
	// LastOccurTime 窗口内最近一次异常的首次发生时间，无异常时为空串。
	LastOccurTime string `json:"lastOccurTime"`
	// Ranking 按 TotalEpisodes 降序的排名，1 = 最差。
	//
	// 注意它**不是速率**：采样快的点位单位时间内的判定次数更多，抖动型点位因此
	// 更容易上榜。永久性坏点不受影响（边沿触发，永远只有一段）。
	Ranking int `json:"ranking"`
}

// PointAbnormalReportVO 点位异常 top-N 的响应体：网关身份 + 榜单。
//
// 包一层的理由与 DeviceReliabilityReportVO 完全相同（跨网关汇总时需要知道
// 这一行属于哪台网关），此处不重复；两者形态刻意保持一致，调用方可以用同一段
// 代码处理两份报表。
type PointAbnormalReportVO struct {
	// GatewayID / GatewayName 网关自身身份；身份行缺失时为空串（同上）。
	GatewayID   string `json:"gatewayId"`
	GatewayName string `json:"gatewayName"`
	// Records 排序与 Ranking 的语义见 PointAbnormalVO；无异常点位时为空数组（不是 null）。
	Records []PointAbnormalVO `json:"records"`
}
