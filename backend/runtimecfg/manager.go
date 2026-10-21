// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package runtimecfg

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"gorm.io/gorm"

	"iot-gateway/configFile"
	"iot-gateway/logger"
	"iot-gateway/model/po"
)

// compensateTimeout 回滚补偿写库的上限。它与请求上下文无关：客户端断开不应该
// 让「已经改了一半的状态」停在数据库里。
const compensateTimeout = 5 * time.Second

var (
	// ErrUnknownKey 该 key 不在注册表里，不接受动态修改。
	ErrUnknownKey = errors.New("配置项不支持动态修改")
	// ErrInvalidValue 值的类型或范围不合法。
	ErrInvalidValue = errors.New("配置值不合法")
	// ErrClosed 管理器已关闭（网关正在停机）。
	ErrClosed = errors.New("配置管理已关闭")
	// ErrPersist 覆盖值写入 / 删除失败。
	ErrPersist = errors.New("配置保存失败")
	// ErrApplyFailed 已落库并发布新值，但 applier 应用失败；管理器已回滚。
	ErrApplyFailed = errors.New("配置应用失败")
)

// Reader 是组件消费动态开关的最小接口。
//
// 只暴露读方法：组件不该有机会改配置 —— 修改只能走 Manager.Update / Reset，
// 那里才有校验、持久化、applier 与回滚。
type Reader interface {
	Bool(Key) bool
}

// Applier 把变更应用到某个运行中的组件。
//
// 契约：
//   - 必须**原子**：返回 error 时组件应保持调用前的状态；
//   - 不得回调 Manager.Update / Reset（会形成更新锁重入）；
//   - 只在 ApplyHotAction / ApplyHotRead 的 key 上被调用，restart-only 的 key
//     不会触发 applier。
type Applier func(old, new any) error

// Runtime 供组件注册 applier 的接口。生产实现是 *Manager。
type Runtime interface {
	Reader
	RegisterApplier(key Key, fn Applier) (unregister func())
}

// snapshot 某一时刻的生效值。**只整体替换，不原地修改**：读方拿到指针后
// 可以无锁读取；写方总是先 clone 再发布。
type snapshot struct {
	values    map[Key]any
	overrides map[Key]po.SystemConfig // 有覆盖行的 key 才在这里
}

// Field 一项配置的当前状态（供列表接口与前端渲染）。
type Field struct {
	Spec Spec

	// Value 生效值（覆盖值优先，否则默认值）。
	Value bool
	// DefaultValue 来自 default.yaml（启动期配置）的值。
	DefaultValue bool
	// Source "default" 或 "override"。
	Source string
	// Hot 是否运行期生效。
	Hot bool
	// PendingRestart 仅对 ApplyRestart 有意义：已保存的值与本次启动读取的值不同。
	PendingRestart bool

	UpdatedBy string
	UpdatedAt string
}

// Manager 运行期配置管理器。
type Manager struct {
	base  *configFile.Config
	store overrideStore
	specs map[Key]Spec
	now   func() time.Time // 测试缝

	updateMu    sync.Mutex
	closed      bool
	appliers    map[Key][]registeredApplier
	nextApplyID uint64

	current atomic.Pointer[snapshot]
	// boot 是进程启动时加载的生效值，供 restart-only 项计算 PendingRestart。
	// 与 current 一样不可变。
	boot *snapshot
}

type registeredApplier struct {
	id uint64
	fn Applier
}

// NewManager 创建管理器并从 system_config 加载覆盖值。
//
// 加载失败（表缺失、行损坏）一律**非致命**：记 WARN 后按 default.yaml 运行。
// 配置表坏了不应该让网关起不来 —— 采集与上报不依赖这些开关的某个取值。
func NewManager(db *gorm.DB, cfg *configFile.Config) *Manager {
	return newManager(cfg, &gormStore{db: db})
}

func newManager(cfg *configFile.Config, store overrideStore) *Manager {
	m := &Manager{
		base:     cfg,
		store:    store,
		specs:    make(map[Key]Spec, len(specs)),
		now:      time.Now,
		appliers: make(map[Key][]registeredApplier),
	}
	for _, s := range specs {
		m.specs[s.Key] = s
	}
	m.reload(context.Background())
	m.boot = m.current.Load()
	return m
}

func (m *Manager) reload(ctx context.Context) {
	snap := &snapshot{
		values:    make(map[Key]any, len(specs)),
		overrides: make(map[Key]po.SystemConfig),
	}
	for _, s := range specs {
		snap.values[s.Key] = m.baseValue(s)
	}

	rows, err := m.store.List(ctx)
	if err != nil {
		logger.Warn("runtimecfg: 读取 system_config 失败，全部使用 default.yaml 的值: %v", err)
		m.current.Store(snap)
		return
	}

	for _, row := range rows {
		key := Key(row.Key)
		spec, ok := m.specs[key]
		if !ok {
			// 多出来的行可能来自更高版本的配置表。忽略但不删除：降级后再升级
			// 时它还有效，而删掉就再也找不回来了。
			logger.Warn("runtimecfg: 忽略未登记的配置项 %q（可能来自其他版本）", row.Key)
			continue
		}
		value, err := decode(spec, json.RawMessage(row.Value))
		if err != nil {
			logger.Warn("runtimecfg: 配置项 %s 的覆盖值 %q 不合法，回退默认值: %v", row.Key, row.Value, err)
			continue
		}
		snap.values[key] = value
		snap.overrides[key] = row
	}

	m.current.Store(snap)
	if len(snap.overrides) > 0 {
		logger.Info("runtimecfg: 已加载 %d 项运行期覆盖配置", len(snap.overrides))
	}
}

func (m *Manager) baseValue(s Spec) bool {
	if m.base == nil || s.Base == nil {
		return false
	}
	return s.Base(m.base)
}

// decode 严格解析覆盖值。只接受与注册类型完全一致的字面量：
// "true" / "1" / "on" / null 都不算布尔值。
func decode(s Spec, raw json.RawMessage) (any, error) {
	if len(raw) == 0 {
		return nil, fmt.Errorf("%w: 值为空", ErrInvalidValue)
	}
	switch s.Type {
	case ValueTypeBool:
		var v any
		if err := json.Unmarshal(raw, &v); err != nil {
			return nil, fmt.Errorf("%w: %v", ErrInvalidValue, err)
		}
		b, ok := v.(bool)
		if !ok {
			return nil, fmt.Errorf("%w: %s 只接受 true / false", ErrInvalidValue, s.Key)
		}
		return b, nil
	default:
		return nil, fmt.Errorf("%w: 未知类型 %s", ErrInvalidValue, s.Type)
	}
}

// Bool 读取生效值。key 未登记时返回 false（调用方只应传常量）。
func (m *Manager) Bool(key Key) bool {
	snap := m.current.Load()
	if snap == nil {
		return false
	}
	b, _ := snap.values[key].(bool)
	return b
}

// BootBool 返回进程启动时的生效值。
func (m *Manager) BootBool(key Key) bool {
	if m.boot == nil {
		return false
	}
	b, _ := m.boot.values[key].(bool)
	return b
}

// RegisterApplier 注册一个 key 的应用回调，返回注销函数。
//
// 多个回调按注册顺序执行；任一个失败即视为应用失败，Manager 会回滚。
// 注册发生在 Wire 构造期，此时还没有 HTTP 请求，不需要额外的同步。
func (m *Manager) RegisterApplier(key Key, fn Applier) func() {
	m.updateMu.Lock()
	defer m.updateMu.Unlock()
	if m.closed {
		return func() {}
	}
	m.nextApplyID++
	id := m.nextApplyID
	m.appliers[key] = append(m.appliers[key], registeredApplier{id: id, fn: fn})
	return func() {
		m.updateMu.Lock()
		defer m.updateMu.Unlock()
		list := m.appliers[key]
		for i := range list {
			if list[i].id == id {
				m.appliers[key] = append(list[:i], list[i+1:]...)
				return
			}
		}
	}
}

// List 返回所有已登记配置项的状态，顺序与注册表一致。
func (m *Manager) List() []Field {
	snap := m.current.Load()
	out := make([]Field, 0, len(specs))
	for _, s := range specs {
		f := Field{
			Spec:         s,
			DefaultValue: m.baseValue(s),
			Source:       "default",
			Hot:          s.Mode.Hot(),
		}
		if snap != nil {
			if b, ok := snap.values[s.Key].(bool); ok {
				f.Value = b
			}
			if row, ok := snap.overrides[s.Key]; ok {
				f.Source = "override"
				f.UpdatedBy = row.UpdatedBy
				f.UpdatedAt = row.UpdatedAt
			}
		}
		if s.Mode == ApplyRestart {
			f.PendingRestart = f.Value != m.BootBool(s.Key)
		}
		out = append(out, f)
	}
	return out
}

// Update 修改一个配置项。
//
// 顺序是「校验 → 落库 → 发布快照 → apply」：
//   - 落库失败时运行行为不变；
//   - 发布快照必须在 apply 之前 —— discovery 的 applier 调 Start()，而 Start
//     会从本管理器读取 enabled，它必须看到新值；
//   - apply 失败时恢复旧快照并补偿写回旧行，然后返回 ErrApplyFailed。
//
// requiresRestart 为 true 表示已保存但需要重启网关才生效。
func (m *Manager) Update(ctx context.Context, key Key, raw json.RawMessage, updatedBy string) (bool, error) {
	spec, ok := m.specs[key]
	if !ok {
		return false, fmt.Errorf("%w: %s", ErrUnknownKey, key)
	}
	value, err := decode(spec, raw)
	if err != nil {
		return false, err
	}

	m.updateMu.Lock()
	defer m.updateMu.Unlock()
	if m.closed {
		return false, ErrClosed
	}
	if err := m.checkApplier(spec); err != nil {
		return false, err
	}

	oldSnap := m.current.Load()
	oldValue, _ := oldSnap.values[key].(bool)
	oldRow, hadOld := oldSnap.overrides[key]
	newValue := value.(bool)

	now := m.now().Format("2006-01-02 15:04:05")
	encoded, err := json.Marshal(newValue)
	if err != nil {
		return false, fmt.Errorf("%w: %v", ErrPersist, err)
	}
	if err := m.store.Save(ctx, key, string(encoded), updatedBy, now); err != nil {
		return false, fmt.Errorf("%w: %v", ErrPersist, err)
	}

	next := cloneSnapshot(oldSnap)
	next.values[key] = newValue
	next.overrides[key] = po.SystemConfig{
		Key:       string(key),
		Value:     string(encoded),
		UpdatedBy: updatedBy,
		UpdatedAt: now,
	}
	m.current.Store(next)

	if err := m.apply(spec, oldValue, newValue); err != nil {
		m.rollback(spec, oldSnap, oldRow, hadOld, newValue, oldValue)
		return false, fmt.Errorf("%w: %v", ErrApplyFailed, err)
	}

	logger.Info("runtimecfg: %s %v -> %v（操作人 %s）", key, oldValue, newValue, displayUser(updatedBy))
	return spec.Mode == ApplyRestart, nil
}

// Reset 删除覆盖行，恢复 default.yaml 的值。没有覆盖行时是幂等的无操作。
func (m *Manager) Reset(ctx context.Context, key Key, updatedBy string) (bool, error) {
	spec, ok := m.specs[key]
	if !ok {
		return false, fmt.Errorf("%w: %s", ErrUnknownKey, key)
	}

	m.updateMu.Lock()
	defer m.updateMu.Unlock()
	if m.closed {
		return false, ErrClosed
	}

	oldSnap := m.current.Load()
	oldRow, hadOld := oldSnap.overrides[key]
	if !hadOld {
		return false, nil
	}
	if err := m.checkApplier(spec); err != nil {
		return false, err
	}
	oldValue, _ := oldSnap.values[key].(bool)

	if err := m.store.Delete(ctx, key); err != nil {
		return false, fmt.Errorf("%w: %v", ErrPersist, err)
	}

	base := m.baseValue(spec)
	next := cloneSnapshot(oldSnap)
	delete(next.overrides, key)
	next.values[key] = base
	m.current.Store(next)

	if err := m.apply(spec, oldValue, base); err != nil {
		// 补偿：把旧覆盖行写回去，并恢复旧快照。
		// 补偿用独立的上下文：请求可能已因超时 / 客户端断开而取消，
		// 但回滚必须落库。
		m.current.Store(oldSnap)
		compCtx, cancel := context.WithTimeout(context.Background(), compensateTimeout)
		if cerr := m.store.Save(compCtx, key, oldRow.Value, oldRow.UpdatedBy, oldRow.UpdatedAt); cerr != nil {
			logger.Error("runtimecfg: 恢复默认失败后写回覆盖行也失败（%s），数据库与运行状态可能不一致: %v", key, cerr)
		}
		cancel()
		_ = m.apply(spec, base, oldValue)
		return false, fmt.Errorf("%w: %v", ErrApplyFailed, err)
	}

	logger.Info("runtimecfg: %s 恢复默认值 %v（操作人 %s）", key, base, displayUser(updatedBy))
	return spec.Mode == ApplyRestart, nil
}

// checkApplier 校验「需要热应用」的 key 确实注册了回调。
//
// 配错线时宁可当场失败，也不能让接口返回成功而行为没变 —— 后者只会在
// 用户以为已经关掉某个功能时暴露出来。
func (m *Manager) checkApplier(spec Spec) error {
	if spec.Mode == ApplyHotAction && len(m.appliers[spec.Key]) == 0 {
		return fmt.Errorf("runtimecfg: %s 标记为热应用但没有注册 applier（接线错误）", spec.Key)
	}
	return nil
}

func (m *Manager) apply(spec Spec, oldValue, newValue any) error {
	if spec.Mode == ApplyRestart {
		return nil
	}
	for _, a := range m.appliers[spec.Key] {
		if err := a.fn(oldValue, newValue); err != nil {
			return err
		}
	}
	return nil
}

// rollback 在 apply 失败后恢复快照、补偿写回旧行，并尽力把旧值重新应用一遍。
// 补偿失败只记 ERROR：此时数据库是唯一还能自证的状态，不能因为补偿失败再抛一层错。
//
// 补偿写用独立上下文：请求可能已超时 / 客户端断开，但回滚必须落库。
func (m *Manager) rollback(spec Spec, oldSnap *snapshot,
	oldRow po.SystemConfig, hadOld bool, newValue, oldValue any) {
	m.current.Store(oldSnap)
	compCtx, cancel := context.WithTimeout(context.Background(), compensateTimeout)
	defer cancel()
	if hadOld {
		if err := m.store.Save(compCtx, spec.Key, oldRow.Value, oldRow.UpdatedBy, oldRow.UpdatedAt); err != nil {
			logger.Error("runtimecfg: %s 应用失败后写回旧覆盖值也失败，数据库与运行状态可能不一致: %v", spec.Key, err)
		}
	} else {
		if err := m.store.Delete(compCtx, spec.Key); err != nil {
			logger.Error("runtimecfg: %s 应用失败后删除新覆盖行也失败，数据库与运行状态可能不一致: %v", spec.Key, err)
		}
	}
	if err := m.apply(spec, newValue, oldValue); err != nil {
		logger.Error("runtimecfg: %s 回滚到旧值 %v 失败: %v", spec.Key, oldValue, err)
	}
}

// Close 在停机时关闭更新入口，阻止停机过程中还有请求把组件重新拉起来。
// 它不负责停止组件 —— 那些仍由 main.go 的既有 defer 顺序负责。
func (m *Manager) Close() {
	m.updateMu.Lock()
	m.closed = true
	m.appliers = make(map[Key][]registeredApplier)
	m.updateMu.Unlock()
}

func cloneSnapshot(s *snapshot) *snapshot {
	next := &snapshot{
		values:    make(map[Key]any, len(s.values)),
		overrides: make(map[Key]po.SystemConfig, len(s.overrides)),
	}
	for k, v := range s.values {
		next.values[k] = v
	}
	for k, v := range s.overrides {
		next.overrides[k] = v
	}
	return next
}

func displayUser(user string) string {
	if user == "" {
		return "-"
	}
	return user
}
