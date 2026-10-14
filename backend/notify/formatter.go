// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package notify

import (
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"time"

	"iot-gateway/alarm"
	"iot-gateway/model/po"
)

// Webhook 类型（与 alarm_webhook.type 取值一致）。
const (
	TypeDingTalk = "dingtalk" // 钉钉群机器人
	TypeWeCom    = "wecom"    // 企业微信群机器人
	TypeFeishu   = "feishu"   // 飞书群机器人
	TypeCustom   = "custom"   // 通用自定义 webhook
)

// 消息格式（与 alarm_webhook.msg_type 取值一致）。
const (
	MsgTypeMarkdown = "markdown"
	MsgTypeText     = "text"
	MsgTypeCard     = "card"
)

// Request 一次待发送的 HTTP 请求（已含签名参数与最终报文）。
type Request struct {
	Method   string
	URL      string // 已含钉钉加签查询参数（飞书签名在 body 里）
	Headers  map[string]string
	Body     []byte
	Platform string // Webhook 类型，用于选择平台响应体解码器
}

// Formatter 把报警事件渲染成某个平台的一次 HTTP 请求。
//
// 每次发送（含重试）都重新调用 Format：加签含时间戳，必须在发送时刻生成，
// 不能在构造时固化。实现必须无状态、可并发调用。
type Formatter interface {
	Format(ev alarm.Event) (*Request, error)
}

// FormatterFactory 根据配置构建格式化器，同时完成配置合法性校验。
type FormatterFactory func(cfg *po.AlarmWebhook) (Formatter, error)

// formatterFactories 类型 → 构建器注册表。
//
// 这里刻意用包内 map 而非 push 那样的子包 init 注册：webhook 格式化器是纯 JSON
// 构造、无连接无后台协程，且 service 层需要枚举全部类型来校验 type 字段，
// 编译期可枚举比运行期注册更合适。
var formatterFactories = map[string]FormatterFactory{
	TypeDingTalk: newDingTalkFormatter,
	TypeWeCom:    newWeComFormatter,
	TypeFeishu:   newFeishuFormatter,
	TypeCustom:   newCustomFormatter,
}

// TypeMeta 单个 webhook 类型的元信息，供管理接口/前端表单渲染。
type TypeMeta struct {
	Type           string   `json:"type"`
	Label          string   `json:"label"`
	MsgTypes       []string `json:"msgTypes"`       // 支持的报文格式
	DefaultMsgType string   `json:"defaultMsgType"` // 缺省报文格式
	SupportsSign   bool     `json:"supportsSign"`   // 是否支持加签
	SupportsAt     bool     `json:"supportsAt"`     // 是否支持 @
	URLPlaceholder string   `json:"urlPlaceholder"`
}

// typeMetas 各类型元信息（键与 formatterFactories 一致）。
var typeMetas = map[string]TypeMeta{
	TypeDingTalk: {
		Type: TypeDingTalk, Label: "钉钉群机器人",
		MsgTypes: []string{MsgTypeMarkdown, MsgTypeText}, DefaultMsgType: MsgTypeMarkdown,
		SupportsSign: true, SupportsAt: true,
		URLPlaceholder: "https://oapi.dingtalk.com/robot/send?access_token=xxx",
	},
	TypeWeCom: {
		Type: TypeWeCom, Label: "企业微信群机器人",
		MsgTypes: []string{MsgTypeMarkdown, MsgTypeText}, DefaultMsgType: MsgTypeMarkdown,
		SupportsSign: false, SupportsAt: false, // 企微群机器人不支持加签与 @
		URLPlaceholder: "https://qyapi.weixin.qq.com/cgi-bin/webhook/send?key=xxx",
	},
	TypeFeishu: {
		Type: TypeFeishu, Label: "飞书群机器人",
		MsgTypes: []string{MsgTypeText, MsgTypeCard}, DefaultMsgType: MsgTypeText,
		SupportsSign: true, SupportsAt: true,
		URLPlaceholder: "https://open.feishu.cn/open-apis/bot/v2/hook/xxx",
	},
	TypeCustom: {
		Type: TypeCustom, Label: "通用自定义 Webhook",
		MsgTypes: []string{}, DefaultMsgType: "", // 固定信封，不区分报文格式
		SupportsSign: true, SupportsAt: false, // secret 作为 Bearer Token
		URLPlaceholder: "https://your-endpoint.example.com/alarm",
	},
}

// SupportedTypes 返回全部支持的 webhook 类型（已排序）。
func SupportedTypes() []string {
	types := make([]string, 0, len(formatterFactories))
	for t := range formatterFactories {
		types = append(types, t)
	}
	sort.Strings(types)
	return types
}

// SupportedTypeMetas 返回全部类型的元信息（按类型名排序）。
func SupportedTypeMetas() []TypeMeta {
	metas := make([]TypeMeta, 0, len(typeMetas))
	for _, t := range SupportedTypes() {
		if m, ok := typeMetas[t]; ok {
			metas = append(metas, m)
		}
	}
	return metas
}

// IsSupportedType 判断类型是否受支持。
func IsSupportedType(t string) bool {
	_, ok := formatterFactories[t]
	return ok
}

// NewFormatter 按配置构建格式化器，同时校验配置合法性。
func NewFormatter(cfg *po.AlarmWebhook) (Formatter, error) {
	factory, ok := formatterFactories[cfg.Type]
	if !ok {
		return nil, fmt.Errorf("unsupported webhook type %q", cfg.Type)
	}
	return factory(cfg)
}

// ValidateConfig 校验配置合法性但不发送任何请求（供保存前校验复用）。
func ValidateConfig(cfg *po.AlarmWebhook) error {
	_, err := NewFormatter(cfg)
	return err
}

// validateCommon 各类型共用的校验：URL 与 @ 参数。
// 类型合法性由 NewFormatter 在进入工厂前判定，此处不重复检查
// （也避免 工厂 → validateCommon → IsSupportedType → 工厂表 的初始化环）。
func validateCommon(cfg *po.AlarmWebhook, meta TypeMeta) error {
	if strings.TrimSpace(cfg.URL) == "" {
		return fmt.Errorf("url is required")
	}
	u, err := url.Parse(cfg.URL)
	if err != nil {
		return fmt.Errorf("invalid url: %w", err)
	}
	// 只接受 http(s)：url.Parse 会放过 file:/javascript: 等 scheme
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("url must use http or https scheme, got %q", u.Scheme)
	}
	if u.Host == "" {
		return fmt.Errorf("url must contain a host")
	}
	if !meta.SupportsAt && (cfg.AtAll == 1 || len(parseAtList(cfg)) > 0) {
		// 宁可保存时报错，也不要静默忽略 —— 静默会让值班人员以为已经 @ 到人
		return fmt.Errorf("webhook type %q does not support @mention", cfg.Type)
	}
	return nil
}

// resolveMsgType 返回生效的报文格式：配置为空时回落该类型默认值，并校验取值合法。
func resolveMsgType(cfg *po.AlarmWebhook, meta TypeMeta) (string, error) {
	mt := strings.TrimSpace(cfg.MsgType)
	if mt == "" {
		return meta.DefaultMsgType, nil
	}
	for _, allowed := range meta.MsgTypes {
		if mt == allowed {
			return mt, nil
		}
	}
	return "", fmt.Errorf("msgType %q not supported by %q (supported: %s)",
		mt, cfg.Type, strings.Join(meta.MsgTypes, ", "))
}

// parseAtList 解析 at_list 的 JSON 数组文本；为空或非法时返回 nil。
func parseAtList(cfg *po.AlarmWebhook) []string {
	s := strings.TrimSpace(cfg.AtList)
	if s == "" || s == "[]" {
		return nil
	}
	var list []string
	if err := json.Unmarshal([]byte(s), &list); err != nil {
		return nil
	}
	out := make([]string, 0, len(list))
	for _, v := range list {
		if v = strings.TrimSpace(v); v != "" {
			out = append(out, v)
		}
	}
	return out
}

// ---------------------------------------------------------------------------
// 消息渲染
// ---------------------------------------------------------------------------

// Message 平台无关的报警消息中间表示，各格式化器据此渲染成自己的报文。
type Message struct {
	Title     string      // 【断联报警】设备 dev1 / 【质量报警-恢复通知】点位 1#炉.温度
	Rows      [][2]string // 键值行：目标类型 / 目标名称 / 报警级别 / 报警内容 / 时间
	AtAll     bool
	AtList    []string
	IsRecover bool
}

// buildMessage 把报警事件渲染成平台无关消息。
// atAll/atList 由各格式化器在构造时解析好传入（避免每次发送重复解析 JSON）。
func buildMessage(ev alarm.Event, atAll bool, atList []string) Message {
	label := targetTypeLabel(ev.TargetType)
	rows := [][2]string{
		{"目标类型", label},
		{"目标名称", ev.TargetName},
		{"报警级别", ev.Level},
		// 与结构化行刻意重复：这就是报警页面上的原文，便于群消息与库内记录对账
		{"报警内容", ev.Content},
	}
	if ev.AlarmType == alarm.TypeRecover {
		// 恢复记录的 first_occur_time 就是恢复时刻，与 clear_time 同值，
		// 因此这里只展示一行时间，避免同值重复。
		rows = append(rows, [2]string{"恢复时间", ev.ClearTime})
	} else {
		rows = append(rows, [2]string{"首次发生", ev.FirstOccurTime})
	}

	return Message{
		Title:     fmt.Sprintf("%s%s %s", alarmTitlePrefix(ev.AlarmType, ev.TargetType), label, ev.TargetName),
		Rows:      rows,
		AtAll:     atAll,
		AtList:    atList,
		IsRecover: ev.AlarmType == alarm.TypeRecover,
	}
}

// PlainText 渲染纯文本正文（钉钉 text / 飞书 text）：
//
//	【断联报警】设备 dev1
//	目标类型：设备
//	目标名称：dev1
func (m Message) PlainText() string {
	var b strings.Builder
	b.WriteString(m.Title)
	for _, r := range m.Rows {
		b.WriteString("\n")
		b.WriteString(r[0])
		b.WriteString("：")
		b.WriteString(r[1])
	}
	return b.String()
}

// MarkdownLines 渲染 markdown 正文行（不含标题），prefix 为每行前缀：
// 钉钉 "- "、企微 "> "、飞书传空串。
func (m Message) MarkdownLines(prefix string) string {
	lines := make([]string, 0, len(m.Rows))
	for _, r := range m.Rows {
		lines = append(lines, prefix+"**"+r[0]+"**："+r[1])
	}
	return strings.Join(lines, "\n")
}

// nonNilStrings 把 nil 切片规整为空切片：nil 会被 json 序列化成 null，
// 而平台（钉钉 at.atMobiles）期望的是数组。
func nonNilStrings(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// TestEventOptions 构造测试事件的可选参数，零值走默认。
type TestEventOptions struct {
	// AlarmType 传 recover 时生成一条恢复记录；其余值一律按**目标类型**的报警类型
	// 生成（device/channel → offline，point → quality，point_threshold → threshold）
	// —— 让预览能反映该目标真正会收到的那种报警，而不是一个需要调用方自己配对的值。
	AlarmType  string
	TargetType string // device（默认）| channel | point | point_threshold
	TargetName string // 默认「测试设备」/「测试点位」
}

// BuildTestEvent 构造一条用于「测试发送」的报警事件。
//
// 放在本包而非 service：目标类型标签在这里维护一处；报警文案则**直接复用 alarm
// 的 Spec**（DeviceSpec / ChannelSpec / PointSpec）—— 那是真实报警文案的唯一真源，
// 预览与实际收到的消息才不会说法不一致。
func BuildTestEvent(opts TestEventOptions) alarm.Event {
	spec, defaultName := testSpec(opts.TargetType)
	alarmType := spec.AlarmType
	if opts.AlarmType == alarm.TypeRecover {
		alarmType = alarm.TypeRecover
	}
	targetName := opts.TargetName
	if targetName == "" {
		targetName = defaultName
	}

	now := time.Now().Format("2006-01-02 15:04:05")
	ev := alarm.Event{
		AlarmID:        "test",
		TargetID:       "test",
		TargetName:     targetName,
		TargetType:     spec.TargetType,
		AlarmType:      alarmType,
		Level:          "warning",
		Content:        spec.Content(targetName, ""),
		Status:         alarm.StatusActive,
		FirstOccurTime: now,
		LastOccurTime:  now,
		OccurredAt:     time.Now(),
	}
	if alarmType == alarm.TypeRecover {
		ev.Content = spec.RecoverContent(targetName)
		ev.Status = alarm.StatusCleared
		ev.ClearTime = now
	}
	return ev
}

// testSpec 按目标类型取文案规格与默认目标名；零值/未知类型回落设备
// （与改动前一致：老调用方只传 alarmType，不带 targetType）。
func testSpec(targetType string) (alarm.Spec, string) {
	switch targetType {
	case alarm.TypeChannel:
		return alarm.ChannelSpec, "测试设备"
	case alarm.TypePoint:
		return alarm.PointSpec, "测试点位"
	case alarm.TypePointThreshold:
		return alarm.ThresholdSpec, "测试点位"
	default:
		return alarm.DeviceSpec, "测试设备"
	}
}

// targetTypeLabel 目标类型中文说明。
func targetTypeLabel(t string) string {
	switch t {
	case alarm.TypeDevice:
		return "设备"
	case alarm.TypeChannel:
		return "推送通道"
	case alarm.TypePoint, alarm.TypePointThreshold:
		// 超限报警的目标主体同样是点位，标签复用「点位」—— 区别体现在标题前缀
		// （【超限报警】）与正文（「点位 X 超限（45.2 > 上限 40）」）上。
		return "点位"
	}
	return t
}

// alarmTitlePrefix 报警类型标题前缀。
//
// 恢复是跨全部报警类型共用的 alarm_type（一行恢复记录看不出它恢复的是断联、质量
// 还是超限），原始类别只能由 **target_type** 反推 —— 两者在 alarm.Spec 里一一对应
// （device/channel → offline，point → quality，point_threshold → threshold）。
// 因此恢复标题补上类别前缀（【超限报警-恢复通知】）：同一个群里断联与超限各恢复一次，
// 光看「【恢复通知】」分不出哪条是哪条。
//
// **注意两处默认分支都是断联**：漏判一种类别会让它顶着「【断联报警】」发出去，
// 所以新增报警类型必须在这里补 case（而未知 target_type 的恢复记录宁可退回不带
// 类别前缀的「【恢复通知】」，也不安一个错误类别）。
func alarmTitlePrefix(alarmType, targetType string) string {
	if alarmType == alarm.TypeRecover {
		if label, ok := alarmKindLabel(targetType); ok {
			return "【" + label + "-恢复通知】"
		}
		return "【恢复通知】"
	}
	if label, ok := alarmKindLabel(alarmType); ok {
		return "【" + label + "】"
	}
	return "【断联报警】"
}

// alarmKindLabel 报警类别中文名，第二个返回值为 false 表示取值未知。
//
// 入参既可以是 alarm_type（offline/quality/threshold）也可以是 target_type
// （device/channel/point/point_threshold）：两个枚举的取值互不重叠，且在上述
// 一一对应关系下含义相同，故共用一张表。
func alarmKindLabel(kind string) (string, bool) {
	switch kind {
	case alarm.TypeOffline, alarm.TypeDevice, alarm.TypeChannel:
		return "断联报警", true
	case alarm.TypeQuality, alarm.TypePoint:
		return "质量报警", true
	case alarm.TypeThreshold, alarm.TypePointThreshold:
		return "超限报警", true
	}
	return "", false
}
