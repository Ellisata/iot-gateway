package vo

// DataTypeOptionVO 协议可配置数据类型选项（配置界面类型下拉）。
// CommonTypes 为该协议支持的通用类型（CommonDataTypes 顺序）；
// ExtendedTypes 为该协议专属的扩展类型（如 Modbus 的 Int / Real），
// 不在通用列表中、仅该协议可见。
// AllTypes 为 CommonTypes 与 ExtendedTypes 的合并集合，便于集中展示。
type DataTypeOptionVO struct {
	Protocol      string   `json:"protocol"`
	CommonTypes   []string `json:"commonTypes"`
	ExtendedTypes []string `json:"extendedTypes"`
	AllTypes      []string `json:"allTypes"`
}
