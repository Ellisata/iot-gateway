// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

// Package pluginwire 是进程外插件的**传输层**：长度前缀 + 魔数的帧编解码、
// 方法名常量与跨进程错误类型。
//
// 它只依赖标准库，且刻意不认识任何一种插件：驱动插件（driversdk）与将来的
// 推送插件（pushsdk）共用同一套帧。网关侧实现（pluginhost）也直接复用它，
// 因此两侧永远不会各自漂移出一份"差不多"的协议。
//
// 信封与载荷是两件事：本文件定信封（头 / ID / 方法名 / 标志位），载荷怎么编
// 由 Frame.Codec 决定，见 binary.go —— 冷路径 JSON，热路径（read）二进制。
//
// 各插件种类的**语义**（Meta、会话、具体的方法参数）不在本包，见 driversdk。
package pluginwire

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

const (
	// FrameMagic 帧头魔数 "IGP2"（IoT Gateway Plugin v2）。
	//
	// 用长度前缀 + 魔数而非换行分隔或裸 json.Decoder：魔数是**失步锚点**——
	// 插件里一句误写的 fmt.Println 会让换行方案永久且静默地错位，
	// 而魔数不匹配能立刻判定"流已损坏"并杀进程。
	// 配套措施见 driversdk.Serve：它把 os.Stdout 重定向到 stderr，从源头消除失步来源。
	//
	// v2 改了信封布局（见 AppendFrame）：魔数尾数随之进位，使**新旧二进制混部
	// 时在最外层就判定为流损坏**。这层保护是冗余的——driversdk.Meta.APIVersion
	// 本就会在 describe 握手时拒绝版本不符的插件（pluginhost/process.go 调
	// meta.Validate）——但携带版本号的魔数让"帧"本身自描述，不再依赖任何一方
	// 记得去查版本号。冗余的代价是 1 个字节。
	FrameMagic uint32 = 0x49475032

	// HeaderSize 帧头字节数：4 字节魔数 + 4 字节大端长度。
	HeaderSize = 8

	// MaxFrameBytes 单帧载荷上限，防御性内存上界。
	MaxFrameBytes = 64 << 20
)

// 方法名。
const (
	MethodDescribe    = "describe"
	MethodConnect     = "connect"
	MethodRead        = "read"
	MethodIsConnected = "isConnected"
	MethodClose       = "close"
	MethodPing        = "ping"
	// MethodProbe 局域网扫描发现的单目标探测。与 ping 一样**不带会话**：
	// 探测不分配会话资源，调用方也永远不会为它调 Close。
	MethodProbe = "probe"
	// MethodDiscoverPoints 设备点位范围扫描。
	//
	// 与 probe 相反，它**要复用会话**：目标是网关上一台已存在的设备，扫描要读的是
	// 那台设备，因此插件侧会优先复用正在采集的那个会话（块与块之间释放锁），
	// 串口独占的场景下这是唯一可行的路径。
	MethodDiscoverPoints = "discoverPoints"
	MethodShutdown       = "shutdown"
)

// ErrFrameDesync 帧流已损坏（通常是插件往 stdout 写了非帧数据）。
var ErrFrameDesync = errors.New("pluginwire: frame desync (plugin wrote to stdout?)")

// ErrFrameTooLarge 帧载荷超过 MaxFrameBytes。
var ErrFrameTooLarge = errors.New("pluginwire: frame too large")

// RemoteError 插件侧返回的错误。Code 目前恒为 0（普通 error），
// 保留字段以便将来区分可重试/不可重试。
type RemoteError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *RemoteError) Error() string { return e.Message }

// NewRemoteError 把插件内部返回的 error 包装成可过线形式；nil 入参返回 nil。
func NewRemoteError(err error) *RemoteError {
	if err == nil {
		return nil
	}
	return &RemoteError{Message: err.Error()}
}

// Frame 双向复用的信封。
//
// 请求：ID>0 + Method + Params
// 应答：ID 相同 + (Result 或 Error)
//
// 注意 Result 与 Error 可同时存在：read 在驱动报错时既要回传 error，
// 也要回传驱动当前的 connected 状态（这是网关感知链路掉线的唯一通道）。
//
// Params/Result 是**已编码的载荷字节**，怎么编由 Codec 决定（见 binary.go）：
// 冷路径是 JSON 文本，热路径（read）是二进制。信封自己不解释载荷。
type Frame struct {
	ID     uint64
	Method string
	Codec  uint8
	Params []byte
	Result []byte
	Error  *RemoteError
}

// 信封标志位。Params 与 Result 各占一位而不是共用一位：read 的应答确实会
// 同时带 Result 与 Error，而"带的是请求还是应答"必须能明确区分 ——
// 共用一位时一个写错的帧会被当成另一种方向静默解掉。
const (
	flagMethod uint8 = 1 << 0
	flagError  uint8 = 1 << 1
	flagParams uint8 = 1 << 2
	flagResult uint8 = 1 << 3
)

// AppendFrame 把一帧（含 8 字节头）追加到 dst 并返回。
//
// 单独暴露出来是为了让调用方能复用缓冲区；「单次 Write 提交整帧」的约束
// 由 WriteFrame 保持，绕过它自行分片写会破坏帧的原子性。
func AppendFrame(dst []byte, f Frame) ([]byte, error) {
	// 先留出帧头，载荷长度最后才知道，回填。
	start := len(dst)
	dst = append(dst, 0, 0, 0, 0, 0, 0, 0, 0)

	b := NewEncoder(64 + len(f.Params) + len(f.Result))
	b.Uvarint(f.ID)
	b.Byte(f.Codec)

	var flags uint8
	if f.Method != "" {
		flags |= flagMethod
	}
	if f.Error != nil {
		flags |= flagError
	}
	if len(f.Params) > 0 {
		flags |= flagParams
	}
	if len(f.Result) > 0 {
		flags |= flagResult
	}
	b.Byte(flags)

	if flags&flagMethod != 0 {
		b.String(f.Method)
	}
	if flags&flagError != 0 {
		b.Varint(int64(f.Error.Code))
		b.String(f.Error.Message)
	}
	if flags&flagParams != 0 {
		b.Raw(f.Params)
	}
	if flags&flagResult != 0 {
		b.Raw(f.Result)
	}

	payload := b.Bytes()
	if len(payload) > MaxFrameBytes {
		return dst[:start], fmt.Errorf("%w: %d bytes", ErrFrameTooLarge, len(payload))
	}
	binary.BigEndian.PutUint32(dst[start:start+4], FrameMagic)
	binary.BigEndian.PutUint32(dst[start+4:start+8], uint32(len(payload)))
	return append(dst, payload...), nil
}

// WriteFrame 写一帧。调用方须自行串行化（见 client/server 的写锁）。
func WriteFrame(w io.Writer, f Frame) error {
	buf, err := AppendFrame(make([]byte, 0, HeaderSize+64+len(f.Params)+len(f.Result)), f)
	if err != nil {
		return err
	}
	// 单次 Write 提交整帧，避免头与载荷之间被其它写者插入。
	if _, err := w.Write(buf); err != nil {
		return fmt.Errorf("pluginwire: write frame: %w", err)
	}
	return nil
}

// ReadFrame 读一帧。io.EOF 表示对端正常关闭。
func ReadFrame(r io.Reader) (Frame, error) {
	var header [HeaderSize]byte
	if _, err := io.ReadFull(r, header[:]); err != nil {
		return Frame{}, err
	}
	if magic := binary.BigEndian.Uint32(header[0:4]); magic != FrameMagic {
		return Frame{}, fmt.Errorf("%w: bad magic 0x%08x", ErrFrameDesync, magic)
	}
	size := binary.BigEndian.Uint32(header[4:8])
	if size > MaxFrameBytes {
		return Frame{}, fmt.Errorf("%w: %d bytes", ErrFrameTooLarge, size)
	}
	// 每帧新分配的载荷缓冲，且**永不池化** —— Decoder 的 String/Raw 直接切片
	// 引用它，池化会让已发出的字符串突然变成另一帧的内容。
	payload := make([]byte, size)
	if _, err := io.ReadFull(r, payload); err != nil {
		return Frame{}, err
	}
	return ParseFrame(payload)
}

// ParseFrame 解析帧头之后的载荷。
//
// 解析失败一律折算成 ErrFrameDesync：调用方（client.readLoop）据此判定
// "流已损坏"并杀进程重启，而这个处置对"魔数不对"与"载荷解不出来"是同一个 ——
// 两者的共同点是对端在按另一套规矩说话，继续读只会越错越远。
func ParseFrame(payload []byte) (Frame, error) {
	d := NewDecoder(payload)
	var f Frame
	f.ID = d.Uvarint()
	f.Codec = d.Byte()
	flags := d.Byte()

	if flags&flagMethod != 0 {
		f.Method = d.String()
	}
	if flags&flagError != 0 {
		code := d.Varint()
		msg := d.String()
		f.Error = &RemoteError{Code: int(code), Message: msg}
	}
	if flags&flagParams != 0 {
		f.Params = d.Raw()
	}
	if flags&flagResult != 0 {
		f.Result = d.Raw()
	}

	if err := d.Done(); err != nil {
		return Frame{}, fmt.Errorf("%w: %v", ErrFrameDesync, err)
	}
	return f, nil
}
