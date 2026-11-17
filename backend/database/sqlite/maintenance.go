// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package sqlite

import "sync/atomic"

// externalMaintenance 记录进程内正在进行的长读事务（目前只有备份快照）。
//
// VACUUM INTO 会短暂钉住 WAL：PASSIVE checkpoint 合并不了尚未被读者释放的帧，
// WALCheckpointer 会把这种情况打成「被外部读者钉住」的 WARN。备份是预期行为，
// 不应每轮都误报，因此由备份方在快照期间置位，checkpoint 只降级为 Debug。
//
// 用计数器而不是 bool：两次快照重叠时，先结束的那次不能把另一个的标记清掉。
var externalMaintenance atomic.Int32

// BeginExternalMaintenance 标记一个进程内的维护读事务开始。
// 返回的函数必须调用（通常 defer），用于撤销标记。
func BeginExternalMaintenance() func() {
	externalMaintenance.Add(1)
	return func() {
		externalMaintenance.Add(-1)
	}
}

func externalMaintenanceActive() bool {
	return externalMaintenance.Load() > 0
}
