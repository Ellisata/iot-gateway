// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package dlt645

import "testing"

const problemDI = "00000000"

// 同一问题重复上报只能算一次变化——这是抑制刷屏的全部依据。
func TestProblemLogSuppressesRepeats(t *testing.T) {
	var p problemLog
	const msg = "数据标识 00000000 读取被拒绝，点位保持异常: 电表异常应答（错误码 01：其他错误）"

	if !p.report(problemDI, msg) {
		t.Error("首次上报应判定为问题变化")
	}
	for i := 2; i <= 4; i++ {
		if p.report(problemDI, msg) {
			t.Errorf("第 %d 轮上报不应判定为变化（否则每轮采集都会刷一条 WARN）", i)
		}
	}
	// 问题内容变了（例如错误码 01 → 02）必须重新报，否则错误码变化会被静默吞掉
	if !p.report(problemDI, msg+"（错误码 02）") {
		t.Error("问题描述变化应重新判定为变化")
	}
	if !p.abnormal(problemDI) {
		t.Error("上报后应处于异常状态")
	}
}

// 不同数据标识的抑制状态互不干扰：一块表上多个点位都异常时，每个都要各报一次。
func TestProblemLogIsPerKey(t *testing.T) {
	var p problemLog
	p.report(problemDI, "A 异常")
	p.report("00000001", "B 异常")
	if !p.abnormal(problemDI) || !p.abnormal("00000001") {
		t.Fatal("两个数据标识都应处于异常状态")
	}

	p.resolve(problemDI, "数据标识 "+problemDI)
	if p.abnormal(problemDI) {
		t.Error("已恢复的数据标识不应仍处于异常状态")
	}
	if !p.abnormal("00000001") {
		t.Error("恢复一个数据标识不应影响另一个")
	}
}

// 恢复正常只报一次：正常轮次每轮都会调用 resolve，不能变成新的刷屏源；
// 恢复之后再次出问题要重新告警。
func TestProblemLogResolveOnce(t *testing.T) {
	var p problemLog

	if p.resolve(problemDI, "数据标识 "+problemDI) {
		t.Error("从未异常时不应判定为恢复（会每轮刷一条 INFO）")
	}
	p.report(problemDI, "读到一半失败")
	if !p.resolve(problemDI, "数据标识 "+problemDI) {
		t.Error("异常后恢复应判定为恢复")
	}
	if p.resolve(problemDI, "数据标识 "+problemDI) {
		t.Error("重复调用不应再次判定为恢复")
	}
	if !p.report(problemDI, "读到一半失败") {
		t.Error("恢复后再次出问题应重新告警")
	}
}

// 重连可能换了表：遗留的问题状态必须清掉，否则新表的首次告警会被旧表压掉。
func TestProblemLogClear(t *testing.T) {
	var p problemLog
	p.report(problemDI, "旧表的异常")
	p.clear()
	if p.abnormal(problemDI) {
		t.Fatal("clear 后不应保留任何异常状态")
	}
	if !p.report(problemDI, "旧表的异常") {
		t.Error("clear 后同样的描述应被视为首次出现")
	}
}
