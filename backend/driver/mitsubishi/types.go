package mitsubishi

import "time"

// 协议注册名。
// 传输层由协议注册名固定（TCP=3E 帧二进制、Serial=4C 帧 Format5 二进制），
// 不随 protocol_json 的 transport 字段切换；两个协议共享同一 MC 应用层
// （设备码、地址解析、区间计算、数据解码），仅底层 I/O 与封帧不同。
const (
	ProtocolMCTCP    = "Mitsubishi.MC.TCP"
	ProtocolMCSerial = "Mitsubishi.MC.Serial"
)

// 传输层标识（cfg.Transport 取值）。
const (
	TransportTCP    = "TCP"
	TransportSerial = "SERIAL"
)

// 默认值与硬上限
const (
	defaultPort         = 5007 // Q/L 系列内置以太网模块默认端口
	defaultTimeoutMS    = 5000
	defaultStringLen    = 16       // string 类型读取字数（每字 2 个 ASCII 字符）
	defaultMergeGap     = 8        // 区间合并最大字间隙
	defaultMaxReadWords = 100      // 单请求最大读取点数（字模式按字、位模式按位）
	maxReadWordsLimit   = 960      // 单请求读取点数硬上限（3E 帧字单位批量读上限）
	maxDeviceNumber     = 0xFFFFFF // 首地址号为 3 字节
)

// mcDevice MC 设备定义。
type mcDevice struct {
	name  string // 规范名（大写，匹配时最长前缀优先）
	code  uint16 // 设备码（二进制 3E/4C 帧共用）
	isBit bool   // 位设备（X/Y/M/L/F/V/B/S/SB/TS/TN/CS/CN）
	octal bool   // X/Y 八进制寻址
}

// mcDevices 设备码表（QnA 兼容 3E 帧二进制）。
// 顺序即匹配优先级：长度降序（先匹配 ZR/SB/SD/SW/TS/TN/TC/CS/CN/CC 等 2 字符设备，
// 再匹配 1 字符设备），避免 ZR/Z、SB/S、SD/S、TS/S 等前缀歧义。
var mcDevices = []mcDevice{
	// ---- 2 字符设备 ----
	{name: "ZR", code: 0xB0},              // 文件寄存器（扩展）
	{name: "SB", code: 0xA1, isBit: true}, // 链接特殊继电器
	{name: "SD", code: 0xA9},              // 特殊寄存器
	{name: "SW", code: 0xB5},              // 链接特殊寄存器
	{name: "TS", code: 0xC1, isBit: true}, // 定时器触点
	{name: "TN", code: 0xC2, isBit: true}, // 定时器线圈
	{name: "TC", code: 0xC0},              // 定时器当前值
	{name: "CS", code: 0xC4, isBit: true}, // 计数器触点
	{name: "CN", code: 0xC5, isBit: true}, // 计数器线圈
	{name: "CC", code: 0xC3},              // 计数器当前值
	// ---- 1 字符设备 ----
	{name: "X", code: 0x9C, isBit: true, octal: true}, // 输入（八进制）
	{name: "Y", code: 0x9D, isBit: true, octal: true}, // 输出（八进制）
	{name: "M", code: 0x90, isBit: true},              // 内部继电器
	{name: "L", code: 0x92, isBit: true},              // 锁存继电器
	{name: "F", code: 0x93, isBit: true},              // 报警器
	{name: "V", code: 0x94, isBit: true},              // 边沿继电器
	{name: "B", code: 0xA0, isBit: true},              // 链接继电器
	{name: "S", code: 0x98, isBit: true},              // 步进继电器
	{name: "Z", code: 0xCC},                           // 变址寄存器
	{name: "D", code: 0xA8},                           // 数据寄存器
	{name: "W", code: 0xB4},                           // 链接寄存器
	{name: "R", code: 0xAF},                           // 文件寄存器
}

// lookupDevice 按规范名查找设备定义，未找到返回 false。
func lookupDevice(name string) (mcDevice, bool) {
	for _, d := range mcDevices {
		if d.name == name {
			return d, true
		}
	}
	return mcDevice{}, false
}

// MCConfig MC 协议配置（对应 device.protocol_json 反序列化）。
// 传输层由协议注册名固定，transport 字段保留仅作兼容。
type MCConfig struct {
	Transport string `json:"transport"` // 传输层（由协议注册名固定，此字段值被覆盖）
	Host      string `json:"host"`      // 以太网：PLC IP 地址
	Port      int    `json:"port"`      // 以太网端口，默认 5007

	ComPort  string `json:"comPort"`  // 串口：名称，如 "COM1"、"/dev/ttyUSB0"
	BaudRate int    `json:"baudRate"` // 串口波特率，默认 9600
	DataBits int    `json:"dataBits"` // 数据位（5/6/7/8），默认 8
	StopBits int    `json:"stopBits"` // 停止位（1 或 2），默认 1
	Parity   string `json:"parity"`   // 校验位："N"=无、"E"=偶、"O"=奇，默认 "N"

	TimeoutMS    int           `json:"timeoutMs"`    // 超时毫秒数，默认 5000
	Timeout      time.Duration `json:"-"`            // 超时时间（由 TimeoutMS 转换）
	StringLen    int           `json:"stringLen"`    // string 类型单点读取字数（每字 2 字符），默认 16
	MaxGap       int           `json:"maxGap"`       // 区间合并最大字间隙（0=关闭），默认 8
	MaxReadWords int           `json:"maxReadWords"` // 单请求最大读取点数（字按字、位按位），默认 100
}

// DefaultMCConfig 返回默认 MC 配置。
func DefaultMCConfig() *MCConfig {
	return &MCConfig{
		Transport:    TransportTCP,
		Host:         "127.0.0.1",
		Port:         defaultPort,
		ComPort:      "COM1",
		BaudRate:     9600,
		DataBits:     8,
		StopBits:     1,
		Parity:       "N",
		TimeoutMS:    defaultTimeoutMS,
		Timeout:      time.Duration(defaultTimeoutMS) * time.Millisecond,
		StringLen:    defaultStringLen,
		MaxGap:       defaultMergeGap,
		MaxReadWords: defaultMaxReadWords,
	}
}

// MCAddress 解析后的 MC 地址。
// 由 ParseMCAddress 从点位 name 解析得到结构信息（Device/Number/Bit/IsString/StringLen），
// Word/BitMode/SpanWords 由 CalcMCRanges 结合 DataType 计算后填充。
type MCAddress struct {
	Device    mcDevice
	Number    uint32 // 设备号（字设备=字地址；位设备=位地址；X/Y 为八进制解释后的十进制）
	Bit       int    // 字设备位访问 0..15（bool 点位），非位地址为 -1
	IsString  bool   // 是否 string 点位
	StringLen int    // string 读取字数

	// 由 CalcMCRanges 填充
	Word      uint32 // 字地址（字模式读取的偏移基准；位设备字访问时 = floor(Number/16)*16）
	BitMode   bool   // 位单位读取（bool 位设备，子命令 0x0001）
	SpanWords uint32 // 字模式读取跨度（点数，字为单位）
}

// IsBit 是否为字设备位访问（bool 点位，读取所在字后本地取位）。
func (a *MCAddress) IsBit() bool {
	return a.Bit >= 0
}

// MCRange 读取区间计算结果。
// 由 CalcMCRanges 返回，供 Read 读取整段数据后按偏移解码。
type MCRange struct {
	Device  mcDevice
	BitMode bool   // 位单位读取（Points 为位数，数据 1 字节/点）；false 为字单位（Points 为字数，2 字节/点）
	Start   uint32 // 起始地址（BitMode=false: 字地址；BitMode=true: 位地址）
	Points  uint16 // 读取点数
	// AddressMap 将 DeviceAddress.ID 映射为 MCAddress。
	// 解码函数直接查此 map 避免重复解析 name。
	AddressMap map[string]MCAddress
}
