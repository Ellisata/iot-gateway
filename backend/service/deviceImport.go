// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/Ellisata/iot-gateway-sdk/driver"
	"gorm.io/gorm"

	"iot-gateway/appError"
	"iot-gateway/enums"
	"iot-gateway/excelutil"
	"iot-gateway/formjson"
	"iot-gateway/i18n"
	"iot-gateway/model/po"
)

// 设备导入列下标。诊断代码里 -1 表示该列不存在。
const (
	importColName = iota
	importColProtocol
	importColDescription
	importColCode
	importColModel
	importColStatus
	importBaseCols
)

// paramHeaderRe 匹配参数列表头末尾的字段键标记，如「设备IP *[host]」。
// 字段键是导入端唯一的权威：标题随语言变化，列顺序也可被用户调整。
var paramHeaderRe = regexp.MustCompile(`\[([A-Za-z_][A-Za-z0-9_.-]*)\]\s*$`)

// deviceImportParam 一个协议参数列。
type deviceImportParam struct {
	Col    int
	Header string // 原始表头，用于报错
	Field  formjson.Field
}

// deviceImportPlan 由表头解析出的导入计划。
type deviceImportPlan struct {
	// 基础列下标，-1 表示模板中没有这一列（仅名称/协议为必需）。
	NameCol        int
	ProtocolCol    int
	DescriptionCol int
	CodeCol        int
	ModelCol       int
	StatusCol      int

	// Params 协议参数列。为空表示通用模板/旧模板，导入时 protocol_json 保持 "{}"。
	Params []deviceImportParam

	// Protocol 含参数列时整表统一到的协议。
	Protocol po.IotProtocol
	// Fields 当前协议的顶层参数字段，按 field key 索引。
	Fields map[string]formjson.Field
}

func (p *deviceImportPlan) hasParams() bool { return len(p.Params) > 0 }

// buildDeviceImportPlan 识别模板表头。基础列按中英文别名匹配、顺序无关；
// 参数列以末尾 [key] 标记识别，因此英文模板、重排列、加备注列都不影响导入。
func buildDeviceImportPlan(header []string) (*deviceImportPlan, error) {
	if len(header) == 0 {
		return nil, importTableError("Excel 内容为空")
	}

	plan := &deviceImportPlan{
		NameCol:        -1,
		ProtocolCol:    -1,
		DescriptionCol: -1,
		CodeCol:        -1,
		ModelCol:       -1,
		StatusCol:      -1,
	}
	seenBase := make(map[int]bool)
	seenParam := make(map[string]bool)

	for idx, raw := range header {
		headerText := normalizeImportHeader(raw)
		if headerText == "" {
			continue
		}

		title, key, isParam := splitParamHeader(headerText)
		if isParam {
			if seenParam[key] {
				return nil, importTableError(fmt.Sprintf("模板中协议参数列「%s」重复", key))
			}
			seenParam[key] = true
			plan.Params = append(plan.Params, deviceImportParam{
				Col:    idx,
				Header: headerText,
				// Field 在 bindFields 里按协议表单补齐；这里先留 key 供报错。
				Field: formjson.Field{Key: key, Title: title},
			})
			continue
		}

		base, ok := baseColumnOf(headerText)
		if !ok {
			continue // 用户自加的备注列：忽略而不是报错
		}
		if seenBase[base] {
			return nil, importTableError(fmt.Sprintf("模板中「%s」列重复", headerText))
		}
		seenBase[base] = true
		switch base {
		case importColName:
			plan.NameCol = idx
		case importColProtocol:
			plan.ProtocolCol = idx
		case importColDescription:
			plan.DescriptionCol = idx
		case importColCode:
			plan.CodeCol = idx
		case importColModel:
			plan.ModelCol = idx
		case importColStatus:
			plan.StatusCol = idx
		}
	}

	if plan.NameCol < 0 {
		return nil, importTableError("模板缺少「名称」列，请重新下载模板")
	}
	if plan.ProtocolCol < 0 {
		return nil, importTableError("模板缺少「协议」列，请重新下载模板")
	}
	if plan.CodeCol < 0 {
		return nil, importTableError("模板缺少「编码」列，请重新下载模板")
	}
	return plan, nil
}

func normalizeImportHeader(raw string) string {
	text := strings.TrimPrefix(strings.TrimSpace(raw), "\ufeff")
	text = strings.ReplaceAll(text, "［", "[")
	text = strings.ReplaceAll(text, "］", "]")
	text = strings.TrimSpace(text)
	text = strings.Trim(text, "*＊")
	return strings.TrimSpace(text)
}

// splitParamHeader 区分基础列与协议参数列。参数列的标题可以重复（不同字段同名），
// 因此导入端只认末尾的 [fieldKey]。
func splitParamHeader(header string) (title, key string, ok bool) {
	m := paramHeaderRe.FindStringSubmatchIndex(header)
	if m == nil {
		return "", "", false
	}
	key = header[m[2]:m[3]]
	title = strings.TrimSpace(header[:m[0]])
	title = strings.TrimSpace(strings.TrimRight(title, "*＊"))
	if title == "" {
		title = key
	}
	return title, key, true
}

func baseColumnOf(header string) (int, bool) {
	normalized := strings.ToLower(header)
	normalized = strings.ReplaceAll(normalized, " ", "")
	normalized = strings.ReplaceAll(normalized, "_", "")
	normalized = strings.ReplaceAll(normalized, "-", "")
	switch normalized {
	case "名称", "name":
		return importColName, true
	case "协议", "protocol":
		return importColProtocol, true
	case "描述", "description", "desc":
		return importColDescription, true
	case "编码", "code":
		return importColCode, true
	case "物模型", "thingmodel", "model":
		return importColModel, true
	case "状态", "status":
		return importColStatus, true
	default:
		return 0, false
	}
}

// resolveUniformProtocol 校验含协议参数列的模板「整表同一协议」。
// 返回 nil 表示没有任何数据行（导入结果为空，不算错误）。
func (p *deviceImportPlan) resolveUniformProtocol(rows [][]string, protocols map[string]po.IotProtocol) (*po.IotProtocol, error) {
	firstProtocol := ""
	firstRow := 0
	for i := 1; i < len(rows); i++ {
		if excelutil.IsBlankRow(rows[i]) {
			continue
		}
		rowNumber := i + 1
		name := excelutil.Cell(rows[i], p.ProtocolCol)
		if name == "" {
			return nil, importTableError(fmt.Sprintf("第 %d 行协议为空；含协议参数的模板要求每行都填写协议", rowNumber))
		}
		if firstProtocol == "" {
			firstProtocol, firstRow = name, rowNumber
			continue
		}
		if name != firstProtocol {
			return nil, importTableError(fmt.Sprintf(
				"第 %d 行协议「%s」与第 %d 行「%s」不一致；含协议参数的模板要求整表使用同一协议",
				rowNumber, name, firstRow, firstProtocol,
			))
		}
	}
	if firstProtocol == "" {
		return nil, nil
	}
	proto, ok := protocols[firstProtocol]
	if !ok {
		return nil, importTableError("协议不存在：" + firstProtocol)
	}
	return &proto, nil
}

// bindFields 把模板参数列绑定到协议当前表单的字段上，并校验模板没有过期。
func (p *deviceImportPlan) bindFields(doc *formjson.Document, protocolName string) error {
	if len(doc.Unsupported) > 0 {
		return importTableError(fmt.Sprintf(
			"协议「%s」的表单包含无法用 Excel 表达的分组或集合字段，请使用设备编辑页填写",
			protocolName,
		))
	}

	fields := make(map[string]formjson.Field, len(doc.Fields))
	for _, field := range doc.Fields {
		fields[field.Key] = field
	}

	present := make(map[string]bool, len(p.Params))
	for i := range p.Params {
		field, ok := fields[p.Params[i].Field.Key]
		if !ok {
			return importTableError(fmt.Sprintf(
				"模板列「%s」不是协议「%s」的当前参数字段，请重新下载模板",
				p.Params[i].Header, protocolName,
			))
		}
		p.Params[i].Field = field
		present[field.Key] = true
	}

	for _, field := range doc.Fields {
		if field.Required && !present[field.Key] {
			return importTableError(fmt.Sprintf(
				"协议「%s」的必填参数「%s」缺少对应列，请重新下载模板",
				protocolName, field.Title,
			))
		}
	}

	p.Fields = fields
	return nil
}

// thingModelIndex 物模型按名称/标识双向索引。名称与标识都可能被用户填进模板。
type thingModelIndex struct {
	byName map[string]po.ThingModel
	byCode map[string]po.ThingModel
}

func loadThingModelIndex(ctx context.Context, db *gorm.DB) (thingModelIndex, error) {
	var models []po.ThingModel
	if err := db.WithContext(ctx).Select("id", "name", "code", "status").Find(&models).Error; err != nil {
		return thingModelIndex{}, err
	}
	idx := thingModelIndex{
		byName: make(map[string]po.ThingModel, len(models)),
		byCode: make(map[string]po.ThingModel, len(models)),
	}
	for _, model := range models {
		idx.byName[model.Name] = model
		if model.Code != "" {
			idx.byCode[model.Code] = model
		}
	}
	return idx, nil
}

// resolve 返回物模型 ID；text 为空表示不绑定。
func (idx thingModelIndex) resolve(text string) (string, string) {
	text = strings.TrimSpace(text)
	if text == "" {
		return "", ""
	}
	byName, hasName := idx.byName[text]
	byCode, hasCode := idx.byCode[text]
	if hasName && hasCode && byName.ID != byCode.ID {
		return "", fmt.Sprintf("物模型不明确（名称与标识指向不同模型）：%s", text)
	}
	model, ok := byName, hasName
	if !ok {
		model, ok = byCode, hasCode
	}
	if !ok {
		return "", "物模型不存在：" + text
	}
	if model.Status != 1 {
		return "", "物模型已停用，不能绑定：" + text
	}
	return model.ID, ""
}

// parseImportStatus 解析启停状态；空值默认启用。
func parseImportStatus(cell string) (int, string) {
	text := strings.TrimSpace(cell)
	if text == "" {
		return 1, ""
	}
	switch strings.ToLower(text) {
	case "启用", "enabled", "enable", "true", "yes", "1", "on":
		return 1, ""
	case "禁用", "停用", "disabled", "disable", "false", "no", "0", "off":
		return 0, ""
	default:
		return 0, fmt.Sprintf("状态不合法：%s（应为 启用/禁用）", text)
	}
}

// convertParamValue 把一个 Excel 单元格转换成协议参数字段的 JSON 原始值。
// 返回 nil 表示可选字段留空、不写入 protocol_json。
func convertParamValue(field formjson.Field, cell string) (json.RawMessage, string) {
	text := strings.TrimSpace(cell)
	if text == "" {
		if field.Required {
			return nil, fmt.Sprintf("「%s」为必填项", field.Title)
		}
		return nil, ""
	}

	switch {
	case field.IsNumber():
		if !isJSONNumber(text) {
			return nil, fmt.Sprintf("「%s」必须是数字：%s", field.Title, text)
		}
		return json.RawMessage(text), ""

	case field.IsSelect():
		if value, ok := matchSelectOption(field.Options, text); ok {
			return value, ""
		}
		return nil, fmt.Sprintf("「%s」取值不在可选项中：%s", field.Title, text)

	case field.IsBool():
		value, ok := parseImportBool(text)
		if !ok {
			return nil, fmt.Sprintf("「%s」应为 true/false：%s", field.Title, text)
		}
		if value {
			return json.RawMessage("true"), ""
		}
		return json.RawMessage("false"), ""

	default:
		// input / textarea / password 及其他标量类型：一律按字符串写入。
		encoded, err := json.Marshal(text)
		if err != nil {
			return nil, fmt.Sprintf("「%s」编码失败", field.Title)
		}
		return encoded, ""
	}
}

// matchSelectOption 先匹配本地化 label，再匹配选项原始值。
func matchSelectOption(options []formjson.Option, text string) (json.RawMessage, bool) {
	for _, option := range options {
		if strings.TrimSpace(option.Label) == text {
			return option.Value, true
		}
	}
	for _, option := range options {
		if formjson.StringValue(option.Value) == text {
			return option.Value, true
		}
	}
	for _, option := range options {
		if formjson.KindOf(option.Value) != formjson.KindNumber {
			continue
		}
		want, err1 := strconv.ParseFloat(string(option.Value), 64)
		got, err2 := strconv.ParseFloat(text, 64)
		if err1 == nil && err2 == nil && want == got {
			return option.Value, true
		}
	}
	return nil, false
}

func parseImportBool(text string) (bool, bool) {
	switch strings.ToLower(strings.TrimSpace(text)) {
	case "true", "1", "yes", "on", "是", "开":
		return true, true
	case "false", "0", "no", "off", "否", "关":
		return false, true
	default:
		return false, false
	}
}

func isJSONNumber(text string) bool {
	if text == "" {
		return false
	}
	first := text[0]
	if first != '-' && (first < '0' || first > '9') {
		return false
	}
	return json.Valid([]byte(text))
}

func buildProtocolJSON(values map[string]json.RawMessage) (string, error) {
	if len(values) == 0 {
		return "{}", nil
	}
	var buf bytes.Buffer
	encoder := json.NewEncoder(&buf)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(values); err != nil {
		return "", err
	}
	return strings.TrimSpace(buf.String()), nil
}

// parseDeviceImportRow 校验并组装一台设备。
// protocols 用于逐行确认协议仍然存在；models 用于物模型绑定；codeTaken 在文件内避让编码。
func parseDeviceImportRow(
	row []string,
	plan *deviceImportPlan,
	protocols map[string]po.IotProtocol,
	models thingModelIndex,
	codeTaken map[string]bool,
	now string,
) (*po.Device, string, string) {
	name := excelutil.Cell(row, plan.NameCol)
	if name == "" {
		return nil, name, "名称为空"
	}
	if utf8.RuneCountInString(name) > 100 {
		return nil, name, "名称不能超过100个字符"
	}

	protocolName := excelutil.Cell(row, plan.ProtocolCol)
	if protocolName == "" {
		return nil, name, "协议为空"
	}
	protocol, ok := protocols[protocolName]
	if !ok {
		return nil, name, "协议不存在：" + protocolName
	}
	if plan.hasParams() && protocol.Name != plan.Protocol.Name {
		return nil, name, fmt.Sprintf("协议「%s」与模板所选协议「%s」不一致", protocolName, plan.Protocol.Name)
	}

	code := excelutil.Cell(row, plan.CodeCol)
	if code == "" {
		return nil, name, "编码为空"
	}
	if !validateEntityCode(code) {
		return nil, name, enums.DeviceCodeInvalidEnum.GetMessage()
	}
	if codeTaken[code] {
		return nil, name, enums.DeviceCodeExistsEnum.GetMessage()
	}

	modelID := ""
	if plan.ModelCol >= 0 {
		resolvedID, reason := models.resolve(excelutil.Cell(row, plan.ModelCol))
		if reason != "" {
			return nil, name, reason
		}
		modelID = resolvedID
	}

	status := 1
	if plan.StatusCol >= 0 {
		parsed, reason := parseImportStatus(excelutil.Cell(row, plan.StatusCol))
		if reason != "" {
			return nil, name, reason
		}
		status = parsed
	}

	description := ""
	if plan.DescriptionCol >= 0 {
		description = excelutil.Cell(row, plan.DescriptionCol)
	}

	values := make(map[string]json.RawMessage, len(plan.Params))
	for _, param := range plan.Params {
		value, reason := convertParamValue(param.Field, excelutil.Cell(row, param.Col))
		if reason != "" {
			return nil, name, reason
		}
		if value != nil {
			values[param.Field.Key] = value
		}
	}
	protocolJSON, err := buildProtocolJSON(values)
	if err != nil {
		return nil, name, "协议参数编码失败：" + err.Error()
	}
	scanIdentity := deriveScanIdentity(protocol.Name, values)

	codeTaken[code] = true

	return &po.Device{
		Code:         code,
		Name:         name,
		ProtocolID:   protocol.ID,
		ProtocolJSON: protocolJSON,
		ModelID:      modelID,
		Description:  description,
		Status:       status,
		ScanIdentity: scanIdentity,
		CreatedAt:    now,
		UpdatedAt:    now,
	}, name, ""
}

// deriveScanIdentity 由导入行解析出的协议参数推导扫描身份签名。
//
// 只认官方协议表单统一的键名：host（网络地址）、port、unitId/dstNode（站号）。
// 串口协议没有 host，通用模板没有连接参数列，二者都返回空串 —— 空签名不参与
// 扫描判重，这正是既有语义。格式与大小写归一统一交给 scanIdentityOf，
// 避免导入与扫描两条路径写出两种签名。
func deriveScanIdentity(protocolName string, values map[string]json.RawMessage) string {
	host := strings.TrimSpace(formjson.StringValue(values["host"]))
	if host == "" {
		return ""
	}
	port, _ := intFromRaw(values["port"])
	unit := 0
	hasUnit := false
	for _, key := range []string{"unitId", "dstNode"} {
		if value, ok := intFromRaw(values[key]); ok {
			unit, hasUnit = value, true
			break
		}
	}
	// FINS 的站点号留空时，驱动按 IP 末段推导（见插件表单的 dstNode 说明）。
	// 扫描结果用的也是这个推导值；导入侧照同一规则补齐，判重才不会被漏掉。
	if !hasUnit && strings.HasPrefix(protocolName, "Omron.FINS.") {
		if last, ok := lastIPv4Octet(host); ok {
			unit = last
		}
	}
	return scanIdentityOf(protocolName, driver.DiscoveredDevice{
		Host:   host,
		Port:   port,
		UnitID: unit,
	})
}

// protocolNameByID 查协议名；协议行不存在时返回空串而不是报错 ——
// 设备创建/更新从不以“协议行是否存在”为前置条件（插件可能已被移除），
// 此时只是无法生成扫描身份。
func protocolNameByID(tx *gorm.DB, id string) (string, error) {
	if id == "" {
		return "", nil
	}
	var protocol po.IotProtocol
	if err := tx.Select("name").Where("id = ?", id).First(&protocol).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return "", nil
		}
		return "", err
	}
	return protocol.Name, nil
}

// deriveScanIdentityFromJSON 与 deriveScanIdentity 相同，但输入是库里的
// protocol_json 原文。解析失败或没有 host 时返回空串（空签名不参与判重）。
func deriveScanIdentityFromJSON(protocolName, protocolJSON string) string {
	if strings.TrimSpace(protocolName) == "" || strings.TrimSpace(protocolJSON) == "" {
		return ""
	}
	var values map[string]json.RawMessage
	if err := json.Unmarshal([]byte(protocolJSON), &values); err != nil {
		return ""
	}
	return deriveScanIdentity(protocolName, values)
}

func lastIPv4Octet(host string) (int, bool) {
	ip := net.ParseIP(host)
	if ip == nil {
		return 0, false
	}
	v4 := ip.To4()
	if v4 == nil {
		return 0, false
	}
	return int(v4[3]), true
}

// intFromRaw 读取数字型或字符串型的整数值；缺失/空串/非数字返回 false。
func intFromRaw(raw json.RawMessage) (int, bool) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return 0, false
	}
	switch formjson.KindOf(raw) {
	case formjson.KindNumber:
		value, err := strconv.ParseFloat(string(raw), 64)
		if err != nil {
			return 0, false
		}
		return int(value), true
	case formjson.KindString:
		text := strings.TrimSpace(formjson.StringValue(raw))
		if text == "" {
			return 0, false
		}
		value, err := strconv.Atoi(text)
		if err != nil {
			return 0, false
		}
		return value, true
	default:
		return 0, false
	}
}

// validateImportRow 校验单行设备导入数据（纯函数，便于单测；去重由 excelutil.Import 统一处理）。
// 设备扫描「一键建设备」与 Excel 导入共用同一套名称/协议规则。
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

func importTableError(message string) error {
	// msgKey 留空：表级错误需要带上具体行号/协议名，不能被通用「参数校验失败」覆盖。
	return appError.NewAppErrorCtx(enums.ParamValidEnum.GetCode(), message, "")
}

// deviceTemplateBase 模板的固定六列（名称/协议/描述/编码/物模型/状态）。
type deviceTemplateBase struct {
	headers      []string
	example      []string
	widths       []float64
	textCols     []int
	lists        []excelutil.ListValidation
	requiredCols []int
}

func newDeviceTemplateBase(lang string) deviceTemplateBase {
	enabled := i18n.T(lang, "device.import.status.enabled")
	disabled := i18n.T(lang, "device.import.status.disabled")

	return deviceTemplateBase{
		headers: []string{
			i18n.T(lang, "device.import.header.name") + " *",
			i18n.T(lang, "device.import.header.protocol") + " *",
			i18n.T(lang, "device.import.header.description"),
			i18n.T(lang, "device.import.header.code") + " *",
			i18n.T(lang, "device.import.header.model"),
			i18n.T(lang, "device.import.header.status"),
		},
		example: []string{
			i18n.T(lang, "device.import.example.name"),
			"", // 协议在模板生成时按需填入；通用模板留空，避免示例行看起来像可直接导入
			i18n.T(lang, "device.import.example.description"),
			i18n.T(lang, "device.import.example.code"),
			"",
			enabled,
		},
		widths:   []float64{22, 24, 32, 18, 20, 10},
		textCols: []int{importColName, importColDescription, importColCode, importColModel, importColStatus},
		lists: []excelutil.ListValidation{{
			Col:     importColStatus,
			Options: []string{enabled, disabled},
		}},
		requiredCols: []int{importColName, importColProtocol, importColCode},
	}
}

// importParamHeader 参数列表头：显示标题 + 字段键 + 必填标记。
// 字段键必须保留英文原文，它是导入端识别列的唯一依据；星号放在最末，
// 与基础列「名称 *」保持同一读法。
func importParamHeader(field formjson.Field) string {
	header := field.Title + " [" + field.Key + "]"
	if field.Required {
		header += " *"
	}
	return header
}

func importParamWidth(title string) float64 {
	width := float64(utf8.RuneCountInString(title)*2 + 8)
	if width < 14 {
		return 14
	}
	if width > 40 {
		return 40
	}
	return width
}

// importExampleValue 按字段默认值生成示例。密码字段与无默认值的必填字段留空，
// 避免把默认口令写进模板、或让示例行看起来可以直接导入。
func importExampleValue(field formjson.Field) string {
	if field.Secret || len(field.Default) == 0 {
		return ""
	}
	if field.IsSelect() {
		for _, option := range field.Options {
			if bytes.Equal(bytes.TrimSpace(option.Value), bytes.TrimSpace(field.Default)) {
				return option.Label
			}
		}
		return ""
	}
	switch formjson.KindOf(field.Default) {
	case formjson.KindString:
		return formjson.StringValue(field.Default)
	case formjson.KindNumber, formjson.KindBool:
		return string(field.Default)
	default:
		return ""
	}
}

// safeValidationOptions 过滤 Excel 内联下拉无法安全表达的选项：
// 空值、重复 label、含逗号（内联列表以逗号分隔）或总长超限时整列跳过下拉，
// 由导入端继续做完整校验。
// truncateRunes 截断过长的提示文案：Excel 输入提示有长度上限，超限会让
// 整个模板写入失败。
func truncateRunes(text string, limit int) string {
	if limit <= 0 || utf8.RuneCountInString(text) <= limit {
		return text
	}
	runes := []rune(text)
	return string(runes[:limit])
}

func safeValidationOptions(options []string) ([]string, bool) {
	if len(options) == 0 || len(options) > 200 {
		return nil, false
	}
	seen := make(map[string]bool, len(options))
	out := make([]string, 0, len(options))
	total := 0
	for _, raw := range options {
		option := strings.TrimSpace(raw)
		if option == "" || strings.ContainsAny(option, ",，") || seen[option] {
			return nil, false
		}
		seen[option] = true
		total += utf8.RuneCountInString(option) + 1
		if total > 200 {
			return nil, false
		}
		out = append(out, option)
	}
	return out, true
}
