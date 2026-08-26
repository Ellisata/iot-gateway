package mitsubishi

import (
	"fmt"
	"strconv"
	"strings"
)

// ParseMCAddress 解析 MC 地址字符串（大小写不敏感）。
//
// 支持的格式（TrimSpace + ToUpper 后解析）：
//
//	字设备:  D{n} / W{n} / R{n} / ZR{n} / Z{n} / SD{n} / SW{n} / TC{n} / CC{n}
//	位设备:  M{n} / L{n} / X{n} / Y{n} / B{n} / F{n} / V{n} / S{n} / SB{n} / TS{n} / TN{n} / CS{n} / CN{n}
//	字设备位访问: D{n}.{bit}（bool 点位，bit 0..15，读取所在字后本地取位）
//
// 设备号：X/Y 按八进制解释（X10 = 八进制 10 = 十进制 8），其余按十进制；
// 首地址号 3 字节上限 0xFFFFFF。位设备禁止 .bit 后缀（位设备本身即为位）。
func ParseMCAddress(name string) (MCAddress, bool) {
	s := strings.ToUpper(strings.TrimSpace(name))
	if s == "" {
		return MCAddress{}, false
	}

	// 设备匹配：按设备表顺序（长度降序）取第一个前缀命中
	for _, dev := range mcDevices {
		if !strings.HasPrefix(s, dev.name) {
			continue
		}
		body := s[len(dev.name):]

		// 拆分 .bit 后缀（仅字设备允许）
		bit := -1
		if dot := strings.IndexByte(body, '.'); dot >= 0 {
			b, err := strconv.Atoi(body[dot+1:])
			if err != nil || b < 0 || b > 15 {
				return MCAddress{}, false
			}
			bit = b
			body = body[:dot]
		}
		if dev.isBit && bit >= 0 {
			// 位设备本身就是位，不接受 .bit 后缀
			return MCAddress{}, false
		}
		if body == "" {
			return MCAddress{}, false
		}

		var num uint64
		var err error
		if dev.octal {
			num, err = parseOctal(body)
		} else {
			num, err = strconv.ParseUint(body, 10, 32)
		}
		if err != nil || num > maxDeviceNumber {
			return MCAddress{}, false
		}

		return MCAddress{Device: dev, Number: uint32(num), Bit: bit}, true
	}
	return MCAddress{}, false
}

// parseOctal 解析八进制字符串（仅允许 0..7）。
func parseOctal(s string) (uint64, error) {
	if s == "" {
		return 0, fmt.Errorf("empty octal")
	}
	var v uint64
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c < '0' || c > '7' {
			return 0, fmt.Errorf("invalid octal digit %q", c)
		}
		if v > (maxDeviceNumber >> 3) {
			return 0, fmt.Errorf("octal value overflow")
		}
		v = v*8 + uint64(c-'0')
	}
	return v, nil
}
