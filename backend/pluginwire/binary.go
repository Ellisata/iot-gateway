// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package pluginwire

// 二进制载荷原语与编解码分发。
//
// 为什么在 JSON 之外还要一套：进程外插件每次 Read 都要把整批点位与整批结果
// 各过线一次，两侧各一次编解码。实测这批编解码占每次调用**可变开销的 ~87%**
// （网关侧单边 1.97 µs/点位，见 pluginhost/ipcbench_test.go）。而过线的热路径
// 载荷只有两个结构体，其余全是每设备一次甚至整个生命周期一次的冷路径。
//
// 分发靠**类型**而非方法名：实现了 BinaryEncoder/BinaryDecoder 的载荷自动走
// 二进制，其余原样走 JSON。于是新增冷方法不需要改这里，也不需要维护一张
// "哪些方法走哪种编码"的对照表——那种表迟早会与实现漂移。

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"unsafe"
)

// zeroCopyStrings 让 Decoder.String 返回**引用载荷缓冲**的字符串，省掉一次复制。
//
// 实测（driversdk/binary_bench_test.go，1000 点位）解码 4001 次分配里有 3000 次
// 就是这次复制。关掉即回到"每次复制一份"的保守实现，两者可随时 A/B。
//
// 开启的前提只有一条，且必须一直成立：**帧缓冲在解析之后绝不被改写、也绝不被复用**。
// 目前满足——pluginwire.ReadFrame 每帧新 make 一个载荷缓冲，从不池化。
// 一旦给 ReadFrame 加缓冲池，这里就会变成静默串数据的 bug（字符串会在下一帧
// 解析时被覆写），且不会有任何报错。要池化，必须先把本开关关掉。
//
// 零拷贝的代价是**内存驻留**：字符串盯住的是整个载荷缓冲，不是它自己那几个字节。
// 而且别名会一路传下去 —— collector.toRecords 的 `Value: r.Value` 是字符串赋值，
// 只复制头（指针+长度）、不复制字节，切不断它。
//
// 实测（pluginwire/zerocopy_test.go 与 push/queue_retention_test.go）：
//
//   - 单条存活：留一条记录就钉住整帧。1000 点位 56 KB，5000 点位 280 KB ——
//     实测每帧 65,631 B / 330,156 B，即载荷的 100%+。
//   - 整批存活（outbox 的真实形态，批次整进整出）：钉住的帧 ≈ 被省掉的那些
//     字符串副本，两者在 push 队列里几乎完全抵消（实测差 +0.7%，且可复现）。
//
// 结论：常规链路上这个代价是中性的 —— 零拷贝并不比"解完就拷贝"更占内存。
//
// 唯一需要提防的形态是「少数记录活得比它所属的帧更久」：那会把整帧钉住，
// 而代价与"留下的记录数"无关，只与"还有多少帧未释放"有关。当前代码里没有
// 这样的路径（记录始终整批流转：push.splitBatches 零拷贝切片、outbox 整批
// 进出、spool 落盘后即释放），但**任何"只扣住几条记录"的新代码都会引入它**，
// 且不会报错、只会慢慢涨内存。加这类缓存前，先回来看看这两条实测。
const zeroCopyStrings = true

// 载荷编码标记，随 Frame.Codec 过线。
const (
	CodecJSON   uint8 = 0
	CodecBinary uint8 = 1
)

// errTruncated 载荷在中途读完或长度字段越界。
var errTruncated = errors.New("pluginwire: 载荷截断或长度非法")

// BinaryEncoder 声明本载荷编码为二进制。
//
// 必须用**值接收者**实现：调用方以值形式把载荷交给 EncodeBody（如
// driversdk.ReadParams{...}），指针接收者会让值类型失去这个方法，
// 从而静默回落到 JSON —— 表现为"改了编解码但一点没变快"，且不报任何错。
type BinaryEncoder interface {
	EncodeBinary() []byte
}

// BinaryDecoder 声明本载荷以二进制解码。
//
// 必须用**指针接收者**：它要写回调用方。解码目标在调用侧一律是取址传来的
// （client.call 的 &reply、server.decode 的 &p），故不冲突。
type BinaryDecoder interface {
	DecodeBinary([]byte) error
}

// EncodeBody 把载荷编成过线字节，并给出该用哪个 Codec 标记。
//
// v 为 nil 返回空体 + CodecJSON：与"没有载荷"的既有语义一致（调用方本就
// 用 len(body)==0 判断有没有内容）。
func EncodeBody(v any) (body []byte, codec uint8, err error) {
	if v == nil {
		return nil, CodecJSON, nil
	}
	if be, ok := v.(BinaryEncoder); ok {
		return be.EncodeBinary(), CodecBinary, nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return nil, CodecJSON, err
	}
	return b, CodecJSON, nil
}

// DecodeBody 按 codec 解出载荷。
//
// 空体直接返回 nil、不触碰 v —— 与 client.call 里 len(Result)>0 的旧判断同义，
// 保证"对端没有回结果"与"结果是零值"仍是同一件事。
func DecodeBody(codec uint8, body []byte, v any) error {
	if v == nil || len(body) == 0 {
		return nil
	}
	switch codec {
	case CodecBinary:
		bd, ok := v.(BinaryDecoder)
		if !ok {
			// 编码端写了二进制、解码端类型接不住：必须报错而不是回落 JSON ——
			// 拿二进制字节当 JSON 解，报出来的错与真实原因相隔十万八千里。
			return fmt.Errorf("pluginwire: %T 未实现 BinaryDecoder，接不住二进制载荷", v)
		}
		return bd.DecodeBinary(body)
	case CodecJSON:
		return json.Unmarshal(body, v)
	default:
		return fmt.Errorf("pluginwire: 未知载荷编码 %d", codec)
	}
}

// ==================== 编解码原语 ====================

// Encoder 追加式写入。
//
// 方法不返回错误：写入只可能因扩容失败而 panic，与 append 同性质，
// 逐字段判错只会把调用方淹在无意义的错误分支里。
type Encoder struct{ b []byte }

// NewEncoder capacity 只影响分配次数，给足即可（宁大勿小不成立：
// 载荷仍是逐字节 clone 进管道，多分配的空隙只是白占内存）。
func NewEncoder(capacity int) *Encoder { return &Encoder{b: make([]byte, 0, capacity)} }

func (e *Encoder) Uvarint(v uint64) { e.b = binary.AppendUvarint(e.b, v) }
func (e *Encoder) Varint(v int64)   { e.b = binary.AppendVarint(e.b, v) }
func (e *Encoder) Byte(v byte)      { e.b = append(e.b, v) }
func (e *Encoder) Bool(v bool) {
	if v {
		e.b = append(e.b, 1)
		return
	}
	e.b = append(e.b, 0)
}

// String 写「长度前缀 + 原始字节」。
func (e *Encoder) String(s string) {
	e.b = binary.AppendUvarint(e.b, uint64(len(s)))
	e.b = append(e.b, s...)
}

// Raw 写「长度前缀 + 原始字节」。留着长度前缀是为了与 String 同构，
// 也为了让同一帧里出现第二段字节时仍有明确边界。
func (e *Encoder) Raw(p []byte) {
	e.b = binary.AppendUvarint(e.b, uint64(len(p)))
	e.b = append(e.b, p...)
}

func (e *Encoder) Bytes() []byte { return e.b }

// Decoder 顺序读取，错误**粘性**：一处失败之后所有读都返回零值，
// 调用方只需在最后查一次 Done()，不必逐字段判错。
//
// 粘性不是图省事：二进制载荷没有 JSON 那样的自描述边界，一旦错位，
// 后续每个字段都会"读出个东西来"，逐字段判错既拦不住也说不清。
type Decoder struct {
	b   []byte
	i   int
	err error
}

func NewDecoder(b []byte) *Decoder { return &Decoder{b: b} }

// Len 剩余字节数。
//
// 调用方用它给「元素个数」设上界：长度字段来自不可信输入，不用它做约束，
// 一个畸形的 n 就能换来一次巨量分配。
func (d *Decoder) Len() int { return len(d.b) - d.i }

func (d *Decoder) fail(err error) {
	if d.err == nil {
		d.err = err
	}
}

func (d *Decoder) Uvarint() uint64 {
	if d.err != nil {
		return 0
	}
	v, n := binary.Uvarint(d.b[d.i:])
	if n <= 0 { // n==0 缓冲不足，n<0 溢出
		d.fail(errTruncated)
		return 0
	}
	d.i += n
	return v
}

func (d *Decoder) Varint() int64 {
	if d.err != nil {
		return 0
	}
	v, n := binary.Varint(d.b[d.i:])
	if n <= 0 {
		d.fail(errTruncated)
		return 0
	}
	d.i += n
	return v
}

func (d *Decoder) Byte() byte {
	if d.err != nil {
		return 0
	}
	if d.i >= len(d.b) {
		d.fail(errTruncated)
		return 0
	}
	v := d.b[d.i]
	d.i++
	return v
}

func (d *Decoder) Bool() bool { return d.Byte() != 0 }

func (d *Decoder) String() string {
	if d.err != nil {
		return ""
	}
	n := d.Uvarint()
	if d.err != nil {
		return ""
	}
	if n > uint64(d.Len()) {
		d.fail(errTruncated)
		return ""
	}
	raw := d.b[d.i : d.i+int(n)]
	d.i += int(n)
	return aliasString(raw)
}

// aliasString 按 zeroCopyStrings 把一段字节转成字符串。
func aliasString(b []byte) string {
	if !zeroCopyStrings {
		return string(b)
	}
	if len(b) == 0 {
		// 显式挡掉空串：unsafe.SliceData 对空切片给出的指针没有意义，
		// 而空串（Value 为 ""、可选字段缺省）在载荷里很常见。
		return ""
	}
	// 合法性来自调用点：b 一定是 d.b 的子切片，而 d.b 是 ReadFrame 新分配的
	// 载荷缓冲。unsafe.String 在这里是把"不可变视图"盖在一段我们保证不再写的
	// 内存上，不是在做类型双关。
	return unsafe.String(unsafe.SliceData(b), len(b))
}

// Raw 返回载荷里的一段字节，**不复制**（同上：帧缓冲不池化）。
func (d *Decoder) Raw() []byte {
	if d.err != nil {
		return nil
	}
	n := d.Uvarint()
	if d.err != nil {
		return nil
	}
	if n > uint64(d.Len()) {
		d.fail(errTruncated)
		return nil
	}
	p := d.b[d.i : d.i+int(n)]
	d.i += int(n)
	return p
}

func (d *Decoder) Err() error { return d.err }

// Done 确认恰好读到末尾。有多余字节即判错：
// 与 JSON 解到尾随垃圾会报错保持一致，也防止两侧对同一帧的长度理解
// 出现分歧时被静默截断——那正是最难查的一类"字段莫名其妙是零值"。
func (d *Decoder) Done() error {
	if d.err != nil {
		return d.err
	}
	if d.i != len(d.b) {
		return fmt.Errorf("pluginwire: 载荷尾部残留 %d 字节", len(d.b)-d.i)
	}
	return nil
}
