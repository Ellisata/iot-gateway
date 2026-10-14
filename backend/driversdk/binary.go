// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package driversdk

import (
	"iot-gateway/driver"
	"iot-gateway/pluginwire"
)

// ReadParams / ReadReply 的二进制载荷。
//
// 只有这两个结构体实现 pluginwire 的编解码接口 —— 它们是**唯一的热路径**：
// 每次采集轮询、每台设备各一次，且体积随点位数线性增长。其余载荷
// （ConnectParams、SessionParams、PingParams、Meta 及各应答）留在 JSON，
// 它们每设备一次甚至整个进程一次，可读性比那点 CPU 值钱。
//
// 分发是自动的：pluginwire.EncodeBody/DecodeBody 按类型判断，所以这里加上
// 两个方法就等于让 read 走二进制，client 与 serve 都不需要知道哪些方法特殊。
//
// 布局与 JSON 字段一一对应，不含任何版本号或魔数：帧魔数（IGP2）与
// APIVersion 已经承担了版本判据，载荷里再自带一份只会多一个漂移源。
//
// 字段顺序与长度前缀由解码端的 Done() 兜底：多一个字节都会报错，
// 因此改布局必然是有声的失败，而不是把后续字段整体错位成看似合法的值。

// EncodeBinary 值接收者 —— 见 pluginwire.BinaryEncoder 的注释：
// client/remote 都是以**值**把 ReadParams 交给 EncodeBody 的。
func (p ReadParams) EncodeBinary() []byte {
	// 每个点位 3 个短字符串，估价 48 字节；估小了只是多几次扩容。
	e := pluginwire.NewEncoder(32 + 48*len(p.Addrs))
	e.Uvarint(p.Session)
	e.Uvarint(uint64(len(p.Addrs)))
	for i := range p.Addrs {
		e.String(p.Addrs[i].ID)
		e.String(p.Addrs[i].Name)
		e.String(p.Addrs[i].DataType)
	}
	return e.Bytes()
}

// DecodeBinary 指针接收者 —— 见 pluginwire.BinaryDecoder 的注释。
func (p *ReadParams) DecodeBinary(b []byte) error {
	d := pluginwire.NewDecoder(b)
	p.Session = d.Uvarint()
	n := d.Uvarint()

	// 预分配量取 n 与「剩余字节能装下的最大条目数」的较小者。
	//
	// 直接用 n 不行：它来自对端的不可信输入，一个畸形的长度字段就能换来
	// 一次 TB 级 make（JSON 侧由 Unmarshal 的输入长度自带上界，二进制侧
	// 得自己补这一课）。而只取一个固定的小常数也不行：正常帧（n=1000）
	// 会被迫 append 扩容十来次，白白多拷几遍、多分一倍的量。
	//
	// 每个点位至少 3 个字节（3 个空字符串各带 1 字节长度前缀），故剩余
	// 字节数除以 3 是一个**由缓冲区本身给出**的、无需信任 n 的上界。
	addrs := make([]WireAddr, 0, int(min(n, uint64(d.Len()/3))))
	for i := uint64(0); i < n; i++ {
		var a WireAddr
		a.ID = d.String()
		a.Name = d.String()
		a.DataType = d.String()
		if d.Err() != nil {
			return d.Done()
		}
		addrs = append(addrs, a)
	}
	p.Addrs = addrs
	return d.Done()
}

func (r ReadReply) EncodeBinary() []byte {
	// 每条结果 4 个字符串 + 2 个 varint，估价 56 字节；估小了只是多几次扩容。
	e := pluginwire.NewEncoder(16 + 56*len(r.Results))
	e.Bool(r.Connected)
	e.Uvarint(uint64(len(r.Results)))
	for i := range r.Results {
		encodeReadResult(e, &r.Results[i])
	}
	return e.Bytes()
}

func (r *ReadReply) DecodeBinary(b []byte) error {
	d := pluginwire.NewDecoder(b)
	r.Connected = d.Bool()
	n := d.Uvarint()

	// 同 ReadParams：预分配量取 n 与剩余字节能装下的条目数的较小者。
	// 每条结果至少 5 个字节（4 个字符串各带 1 字节长度前缀 + 1 字节 quality）。
	results := make([]driver.ReadResult, 0, int(min(n, uint64(d.Len()/5))))
	for i := uint64(0); i < n; i++ {
		var res driver.ReadResult
		res.DeviceAddressID = d.String()
		res.Value = d.String()
		res.DataType = d.String()
		res.Kind = d.String()
		res.Quality = int(d.Varint())
		if d.Err() != nil {
			return d.Done()
		}
		results = append(results, res)
	}
	r.Results = results
	return d.Done()
}

// encodeReadResult 逐字段写出 driver.ReadResult。
//
// Kind 不做枚举压缩：它只有 6 个取值，但压缩省下的是 1000 点位里的 5 KB
// （不到 3%），代价是一张必须在两侧同步维护的映射表 —— 表一漂移就是
// 静默的类型错乱，正是最不该用 3% 去换的东西。Quality 同理：成因已经并进这个
// 整数（见 driver.QualityXxx），不再单独过一个字符串字段。
func encodeReadResult(e *pluginwire.Encoder, res *driver.ReadResult) {
	e.String(res.DeviceAddressID)
	e.String(res.Value)
	e.String(res.DataType)
	e.String(res.Kind)
	e.Varint(int64(res.Quality))
}
