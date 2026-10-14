// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package driversdk_test

// JSON vs 二进制的编解码对比。
//
// 对照组是**同一份数据、同一个 API 形状**：JSON 那一侧直接调 json.Marshal /
// json.Unmarshal，正是 EncodeBody/DecodeBody 在载荷未实现二进制接口时会做的
// 事；二进制那一侧走 EncodeBinary/DecodeBinary。差值即插件边界上这层编解码
// 换掉的净开销（两侧各一份，故端到端收益约为本结果的 2 倍）。
//
// 运行：go test ./driversdk -run '^$' -bench BenchmarkCodec -benchtime 2000x

import (
	"encoding/json"
	"fmt"
	"testing"

	"iot-gateway/driversdk"
)

var benchPoints = []int{1, 10, 100, 1000}

// BenchmarkCodecReadParamsEncode 网关侧发请求 / 插件侧发应答的编码方向。
func BenchmarkCodecReadParamsEncode(b *testing.B) {
	for _, n := range benchPoints {
		p := driversdk.ReadParams{Session: 1, Addrs: sampleAddrs(n)}

		b.Run(fmt.Sprintf("JSON/points=%d", n), func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				if _, err := json.Marshal(p); err != nil {
					b.Fatal(err)
				}
			}
		})
		b.Run(fmt.Sprintf("Binary/points=%d", n), func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				_ = p.EncodeBinary()
			}
		})
	}
}

// BenchmarkCodecReadParamsDecode 插件侧收请求的解码方向（反向亦然）。
func BenchmarkCodecReadParamsDecode(b *testing.B) {
	for _, n := range benchPoints {
		p := driversdk.ReadParams{Session: 1, Addrs: sampleAddrs(n)}
		jsonBody, err := json.Marshal(p)
		if err != nil {
			b.Fatal(err)
		}
		binBody := p.EncodeBinary()

		b.Run(fmt.Sprintf("JSON/points=%d", n), func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				var out driversdk.ReadParams
				if err := json.Unmarshal(jsonBody, &out); err != nil {
					b.Fatal(err)
				}
			}
		})
		b.Run(fmt.Sprintf("Binary/points=%d", n), func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				var out driversdk.ReadParams
				if err := out.DecodeBinary(binBody); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkCodecReadReplyEncode(b *testing.B) {
	for _, n := range benchPoints {
		r := sampleReply(n)

		b.Run(fmt.Sprintf("JSON/points=%d", n), func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				if _, err := json.Marshal(r); err != nil {
					b.Fatal(err)
				}
			}
		})
		b.Run(fmt.Sprintf("Binary/points=%d", n), func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				_ = r.EncodeBinary()
			}
		})
	}
}

func BenchmarkCodecReadReplyDecode(b *testing.B) {
	for _, n := range benchPoints {
		r := sampleReply(n)
		jsonBody, err := json.Marshal(r)
		if err != nil {
			b.Fatal(err)
		}
		binBody := r.EncodeBinary()

		b.Run(fmt.Sprintf("JSON/points=%d", n), func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				var out driversdk.ReadReply
				if err := json.Unmarshal(jsonBody, &out); err != nil {
					b.Fatal(err)
				}
			}
		})
		b.Run(fmt.Sprintf("Binary/points=%d", n), func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				var out driversdk.ReadReply
				if err := out.DecodeBinary(binBody); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// BenchmarkCodecPayloadSize 顺带固化体积差：二进制不只是快，也明显更小 ——
// 管道搬运量同样按点位数线性增长，这部分省下来的是**两个进程**的带宽。
func BenchmarkCodecPayloadSize(b *testing.B) {
	for _, n := range benchPoints {
		p := driversdk.ReadParams{Session: 1, Addrs: sampleAddrs(n)}
		r := sampleReply(n)
		pj, _ := json.Marshal(p)
		rj, _ := json.Marshal(r)

		b.Run(fmt.Sprintf("points=%d", n), func(b *testing.B) {
			b.ReportMetric(float64(len(pj)), "json-params-B")
			b.ReportMetric(float64(len(p.EncodeBinary())), "bin-params-B")
			b.ReportMetric(float64(len(rj)), "json-reply-B")
			b.ReportMetric(float64(len(r.EncodeBinary())), "bin-reply-B")
		})
	}
}
