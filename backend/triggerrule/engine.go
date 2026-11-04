// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package triggerrule

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"sort"
	"sync"
	"time"

	"gorm.io/gorm"

	"iot-gateway/collector"
	"iot-gateway/logger"
	"iot-gateway/model/po"
	"iot-gateway/push"
	"iot-gateway/utils"
)

const (
	// defaultWorkerCount 全局分发协程数。它们只负责把 job 路由到设备执行者，
	// 不执行读点/推送，因此不会被慢设备占住。
	defaultWorkerCount = 2
	jobQueueSize       = 256
	dropQueueSize      = 64
	// deviceQueueSize 每台动作设备等待执行的 job 上限。规则本身单飞，
	// 同一设备通常只有"规则数"个并发 job；64 足够吸收突发。
	deviceQueueSize      = 64
	defaultTimeout       = 5 * time.Second
	defaultWatchInterval = 10 * time.Second
	stopWait             = 5 * time.Second
)

// PointReader 采集引擎提供的能力：用活会话读取点位并转换成采集记录。
// 生产实现是 *collector.Engine.ReadRecords。
type PointReader interface {
	ReadRecords(ctx context.Context, deviceID string, addrs []po.DeviceAddress) ([]collector.CollectedRecord, error)
}

// Pusher 推送引擎提供的能力：定向投递到指定通道实例。
// 生产实现是 *push.Engine.PushRecordsToChannel。
type Pusher interface {
	PushRecordsToChannel(channelID string, records []collector.CollectedRecord) (bool, error)
}

// Engine 事件触发规则引擎。
//
// 实现 collector.RecordSink：采集协程把每批记录交给 PushRecords，本引擎只做
// 内存样本更新与条件判定；真正读设备/推送/落库都在独立 worker 里完成。
//
// **绝不能在 PushRecords 里同步调用 ReadRecords**：串口设备的整轮轮询持有设备锁，
// 采集回调里再读会自锁；非串口设备也可能与 collector.Refresh/Stop 的写锁互等。
type Engine struct {
	db     *gorm.DB
	pusher Pusher

	mu     sync.RWMutex
	reader PointReader
	now    func() time.Time

	running       bool
	rules         map[string]*ruleState
	ruleByAddress map[string][]string
	conditionIDs  map[string]struct{}
	samples       map[string]sample
	ruleHash      uint64
	recent        *recentTriggerRing

	jobs   chan job
	drops  chan job
	stopCh chan struct{}

	// runners: deviceID -> 每设备串行执行者。读点/推送只在这里执行，
	// 慢设备只会占住自己的执行者，不会吃掉全局分发协程。
	// runnersMu 只保护 map 与懒创建；执行本身在 runner 协程里。
	runnersMu sync.Mutex
	runners   map[string]*deviceRunner
	runnerWG  sync.WaitGroup

	baseCtx     context.Context
	cancel      context.CancelFunc
	watchCtx    context.Context
	watchCancel context.CancelFunc

	workerWG sync.WaitGroup
	dropWG   sync.WaitGroup
	watchWG  sync.WaitGroup

	watchInterval   time.Duration
	cleanupInterval time.Duration
	lastCleanup     time.Time
}

// NewEngine 创建规则引擎。pusher 为 nil 表示只会判定，不会推送（测试/裁剪部署）。
func NewEngine(db *gorm.DB, pusher Pusher) *Engine {
	return &Engine{
		db:              db,
		pusher:          pusher,
		now:             time.Now,
		rules:           make(map[string]*ruleState),
		ruleByAddress:   make(map[string][]string),
		conditionIDs:    make(map[string]struct{}),
		samples:         make(map[string]sample),
		recent:          newRecentTriggerRing(RecentTriggerRingSize),
		watchInterval:   defaultWatchInterval,
		cleanupInterval: recordCleanupInterval,
	}
}

// BindCollector 绑定采集引擎（late binding，打破 Wire 构造环）。
// 必须在采集引擎启动前调用。
func (e *Engine) BindCollector(reader PointReader) {
	e.mu.Lock()
	e.reader = reader
	e.mu.Unlock()
}

// Start 加载规则并启动 worker 与热加载 watcher。
func (e *Engine) Start() error {
	e.mu.Lock()
	if e.running {
		e.mu.Unlock()
		return nil
	}
	e.mu.Unlock()

	snap, err := e.loadSnapshot()
	if err != nil {
		return err
	}

	e.mu.Lock()
	// Start 视为一次全新运行：清空上一轮的状态，所有规则走预热。
	e.rules = make(map[string]*ruleState)
	e.ruleByAddress = make(map[string][]string)
	e.conditionIDs = make(map[string]struct{})
	e.samples = make(map[string]sample)
	// 最近触发只是观测窗口，重启后从空开始。
	e.recent = newRecentTriggerRing(RecentTriggerRingSize)
	e.applySnapshotLocked(snap)
	e.ruleHash = snap.hash
	e.running = true
	e.jobs = make(chan job, jobQueueSize)
	e.drops = make(chan job, dropQueueSize)
	e.stopCh = make(chan struct{})
	e.runners = make(map[string]*deviceRunner)
	e.baseCtx, e.cancel = context.WithCancel(context.Background())
	e.watchCtx, e.watchCancel = context.WithCancel(context.Background())
	workerCount := defaultWorkerCount
	e.mu.Unlock()

	for i := 0; i < workerCount; i++ {
		e.workerWG.Add(1)
		go e.worker()
	}
	e.dropWG.Add(1)
	go e.dropWorker()
	e.watchWG.Add(1)
	go e.watchLoop()

	logger.Info("trigger rule engine started (%d rules)", len(snap.rules))
	return nil
}

// Stop 停止接收新触发、排空已入队任务并停止 watcher。
// 调用方必须**先停采集引擎**：停止后不会再有新的 sink 回调。
func (e *Engine) Stop() {
	e.mu.Lock()
	if !e.running {
		e.mu.Unlock()
		return
	}
	e.running = false
	jobs, drops, stopCh := e.jobs, e.drops, e.stopCh
	cancel, watchCancel := e.cancel, e.watchCancel
	e.mu.Unlock()

	close(stopCh)
	if watchCancel != nil {
		watchCancel()
	}
	close(jobs)

	done := make(chan struct{})
	go func() {
		// 先等分发协程把 jobs 路由完，才能关闭设备执行者的入口与 drop 队列：
		// 分发期间仍可能写 drops，执行者期间不会（running=false 后 finishJob
		// 不再补发）。
		e.workerWG.Wait()
		e.closeRunners()
		close(drops)
		e.runnerWG.Wait()
		e.dropWG.Wait()
		e.watchWG.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(stopWait):
		// 插件读取可能正卡在对端超时（默认 60s），不强杀，交给进程退出。
		logger.Warn("trigger rule engine stop timed out after %s", stopWait)
	}
	if cancel != nil {
		cancel()
	}
	logger.Info("trigger rule engine stopped")
}

// Refresh 立即重新加载规则（服务层 CRUD 后调用；失败只由调用方记录）。
func (e *Engine) Refresh() error {
	snap, err := e.loadSnapshot()
	if err != nil {
		return err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if !e.running {
		return nil
	}
	if snap.hash == e.ruleHash {
		return nil
	}
	e.applySnapshotLocked(snap)
	e.ruleHash = snap.hash
	return nil
}

// PushRecords 实现 collector.RecordSink。
// 只更新条件点位的样本并评估受影响规则；不做 DB、网络或设备读取。
func (e *Engine) PushRecords(records []collector.CollectedRecord) {
	if len(records) == 0 {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if !e.running {
		return
	}

	now := e.now()
	affected := make(map[string]struct{})
	for i := range records {
		r := &records[i]
		if _, ok := e.conditionIDs[r.DeviceAddressID]; !ok {
			continue
		}
		e.samples[r.DeviceAddressID] = sample{
			value:   r.Value,
			kind:    r.Kind,
			quality: r.Quality,
			at:      now,
		}
		for _, ruleID := range e.ruleByAddress[r.DeviceAddressID] {
			affected[ruleID] = struct{}{}
		}
	}
	if len(affected) == 0 {
		return
	}
	ids := make([]string, 0, len(affected))
	for id := range affected {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		e.evaluateRuleLocked(id, now)
	}
}

// evaluateRuleLocked 三值判定 + 上升沿/冷却/单飞。
func (e *Engine) evaluateRuleLocked(ruleID string, now time.Time) {
	st := e.rules[ruleID]
	if st == nil || st.rule == nil || st.rule.status != 1 {
		return
	}
	switch evaluate(st.rule, e.samples, now) {
	case triUnknown:
		// 中性：冻结状态，不当成"条件不成立"。
		return
	case triFalse:
		st.initialized = true
		st.lastMet = false
		st.pending = false
	case triTrue:
		if !st.initialized {
			// 预热：规则新建/网关启动时条件已经为真，不算一次事件。
			st.initialized = true
			st.lastMet = true
			return
		}
		if !st.lastMet {
			st.lastMet = true
			e.enqueueLocked(st, now)
		}
	}
}

// enqueueLocked 入队一次触发；调用方持有 e.mu。
func (e *Engine) enqueueLocked(st *ruleState, now time.Time) {
	if !e.running {
		return // Stop 已关闭队列：在飞任务结束后不再补发
	}
	if st.inFlight {
		st.pending = true
		return
	}
	if st.rule.cooldown > 0 && !st.lastFireAt.IsZero() && now.Sub(st.lastFireAt) < st.rule.cooldown {
		return
	}

	j := job{
		state:             st,
		ruleID:            st.rule.id,
		ruleCode:          st.rule.code,
		ruleName:          st.rule.name,
		triggerID:         utils.GenerateUUIDV7(),
		firedAt:           now,
		actionDeviceID:    st.rule.actionDeviceID,
		actionAddresses:   st.rule.actionAddresses,
		targetChannelID:   st.rule.targetChannelID,
		timeout:           st.rule.timeout,
		conditionSnapshot: buildConditionSnapshot(st.rule, e.samples, now),
	}
	select {
	case e.jobs <- j:
		st.inFlight = true
		st.lastFireAt = now
	default:
		// 队列满：记一条 dropped，让运维知道这次边沿被丢弃。
		st.lastFireAt = now
		j.presetStatus = StatusDropped
		j.presetError = "trigger queue full"
		select {
		case e.drops <- j:
		default:
			logger.Warn("triggerrule: rule %s trigger dropped: job and drop queues are full", st.rule.id)
		}
	}
}

type job struct {
	state *ruleState

	ruleID    string
	ruleCode  string
	ruleName  string
	triggerID string
	firedAt   time.Time

	actionDeviceID  string
	actionAddresses []po.DeviceAddress
	targetChannelID string
	timeout         time.Duration

	conditionSnapshot string
	presetStatus      string
	presetError       string
}

// deviceRunner 一台动作设备的串行执行者。同一设备的触发按入队顺序执行，
// 慢设备只会占住自己的 runner，不会消耗全局分发协程。
type deviceRunner struct {
	deviceID string
	jobs     chan job
}

func (e *Engine) worker() {
	defer e.workerWG.Done()
	for j := range e.jobs {
		e.dispatchJob(j)
	}
}

// dispatchJob 把 job 路由到动作设备的执行者；设备队列满时按 dropped 处理。
func (e *Engine) dispatchJob(j job) {
	r := e.runnerFor(j.actionDeviceID)
	select {
	case r.jobs <- j:
		return
	default:
		j.presetStatus = StatusDropped
		j.presetError = "device trigger queue full"
		select {
		case e.drops <- j:
		default:
			logger.Warn("triggerrule: rule %s trigger dropped: device and drop queues are full", j.ruleID)
		}
		// 入队时已置 inFlight；丢弃后必须清除，否则该规则永远不会再触发。
		e.finishJob(j)
	}
}

func (e *Engine) runnerFor(deviceID string) *deviceRunner {
	e.runnersMu.Lock()
	defer e.runnersMu.Unlock()
	if e.runners == nil {
		e.runners = make(map[string]*deviceRunner)
	}
	r := e.runners[deviceID]
	if r == nil {
		r = &deviceRunner{deviceID: deviceID, jobs: make(chan job, deviceQueueSize)}
		e.runners[deviceID] = r
		e.runnerWG.Add(1)
		go e.runDevice(r)
	}
	return r
}

func (e *Engine) runDevice(r *deviceRunner) {
	defer e.runnerWG.Done()
	for j := range r.jobs {
		e.executeJob(j)
	}
}

// closeRunners 关闭所有设备执行者入口，让它们在排空已入队 job 后退出。
// 只能由 Stop 在分发协程全部结束后调用，否则会有向已关闭 channel 发送的竞态。
func (e *Engine) closeRunners() {
	e.runnersMu.Lock()
	rs := make([]*deviceRunner, 0, len(e.runners))
	for _, r := range e.runners {
		rs = append(rs, r)
	}
	e.runners = make(map[string]*deviceRunner)
	e.runnersMu.Unlock()
	for _, r := range rs {
		close(r.jobs)
	}
}

func (e *Engine) dropWorker() {
	defer e.dropWG.Done()
	for j := range e.drops {
		e.persistRecord(&po.TriggerRuleRecord{
			TriggerID:     j.triggerID,
			RuleID:        j.ruleID,
			RuleCode:      j.ruleCode,
			RuleName:      j.ruleName,
			FiredAt:       j.firedAt.Format("2006-01-02 15:04:05"),
			Status:        StatusDropped,
			ConditionJSON: j.conditionSnapshot,
			Error:         j.presetError,
		})
		e.recordRecent(j, StatusDropped, 0, j.presetError, nil)
	}
}

func (e *Engine) executeJob(j job) {
	if j.presetStatus != "" {
		e.persistRecord(&po.TriggerRuleRecord{
			TriggerID:     j.triggerID,
			RuleID:        j.ruleID,
			RuleCode:      j.ruleCode,
			RuleName:      j.ruleName,
			FiredAt:       j.firedAt.Format("2006-01-02 15:04:05"),
			Status:        j.presetStatus,
			ConditionJSON: j.conditionSnapshot,
			Error:         j.presetError,
		})
		e.recordRecent(j, j.presetStatus, 0, j.presetError, nil)
		return
	}

	start := e.now()
	status, errText, actionJSON := StatusReadFailed, "", ""
	var records []collector.CollectedRecord
	func() {
		defer func() {
			if r := recover(); r != nil {
				status = StatusReadFailed
				errText = fmt.Sprintf("worker panic: %v", r)
			}
		}()
		status, errText, actionJSON, records = e.runJob(j)
	}()
	duration := int(e.now().Sub(start) / time.Millisecond)

	e.persistRecord(&po.TriggerRuleRecord{
		TriggerID:        j.triggerID,
		RuleID:           j.ruleID,
		RuleCode:         j.ruleCode,
		RuleName:         j.ruleName,
		FiredAt:          j.firedAt.Format("2006-01-02 15:04:05"),
		Status:           status,
		ConditionJSON:    j.conditionSnapshot,
		ActionResultJSON: actionJSON,
		DurationMs:       duration,
		Error:            errText,
	})
	e.recordRecent(j, status, duration, errText, records)

	e.finishJob(j)
}

// runJob 读取动作点位并定向推送。records 即使推送失败也会返回（供最近触发视图
// 展示"读到了什么、卡在哪一步"）；读取失败时才为空。
func (e *Engine) runJob(j job) (status, errText, actionJSON string, records []collector.CollectedRecord) {
	e.mu.RLock()
	reader := e.reader
	baseCtx := e.baseCtx
	e.mu.RUnlock()

	if reader == nil {
		return StatusOffline, "collector not bound", "", nil
	}
	if baseCtx == nil {
		baseCtx = context.Background()
	}
	timeout := j.timeout
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	ctx, cancel := context.WithTimeout(baseCtx, timeout)
	defer cancel()

	records, err := reader.ReadRecords(ctx, j.actionDeviceID, j.actionAddresses)
	if err != nil {
		switch {
		case errors.Is(err, collector.ErrDeviceBusy):
			// 超时前没拿到设备锁：本次读根本没开始，单独记 timeout。
			return StatusTimeout, err.Error(), "", nil
		case errors.Is(err, collector.ErrNotRunning),
			errors.Is(err, collector.ErrDeviceNotActive),
			errors.Is(err, collector.ErrDeviceOffline):
			return StatusOffline, err.Error(), "", nil
		default:
			return StatusReadFailed, err.Error(), "", nil
		}
	}
	if len(records) == 0 {
		return StatusReadFailed, "no records returned", "", nil
	}

	stamp := e.now().Format("2006-01-02 15:04:05.000")
	// 事件来源只挂在动作记录上：正常采集记录不带，载荷因此保持原样。
	// FiredAt 用条件上升沿时刻，而不是读取完成时刻，便于下游与触发历史对齐。
	trigger := &collector.RecordTrigger{
		Source:    "trigger",
		RuleID:    j.ruleID,
		RuleCode:  j.ruleCode,
		RuleName:  j.ruleName,
		TriggerID: j.triggerID,
		FiredAt:   j.firedAt.Format("2006-01-02 15:04:05.000"),
	}
	for i := range records {
		records[i].CollectedAt = stamp
		records[i].Trigger = trigger
	}
	actionJSON = buildActionResult(j.actionDeviceID, j.targetChannelID, records)

	// 读已经完成但规则超时已到：不再推送迟到的结果。驱动 Read 本身不可
	// 被 context 中断，这里是防止"超时后仍然推送"的最后一道闸。
	if ctxErr := ctx.Err(); ctxErr != nil {
		return StatusTimeout, fmt.Sprintf("read finished after timeout: %v", ctxErr), actionJSON, records
	}

	if e.pusher == nil {
		return StatusPushFailed, "push engine not bound", actionJSON, records
	}
	ok, pushErr := e.pusher.PushRecordsToChannel(j.targetChannelID, records)
	if pushErr != nil {
		if errors.Is(pushErr, push.ErrChannelNotFound) {
			return StatusDropped, pushErr.Error(), actionJSON, records
		}
		return StatusPushFailed, pushErr.Error(), actionJSON, records
	}
	if !ok {
		return StatusPushFailed, "push rejected", actionJSON, records
	}
	return StatusSuccess, "", actionJSON, records
}

// finishJob 清单飞标记；若在飞行期间又出现了新的上升沿，补发一次。
func (e *Engine) finishJob(j job) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.rules[j.ruleID] != j.state {
		return // 规则已被删除/替换：新状态不应被旧任务影响
	}
	j.state.inFlight = false
	if !j.state.pending {
		return
	}
	j.state.pending = false
	if !j.state.lastMet {
		return
	}
	now := e.now()
	if j.state.rule.cooldown > 0 && now.Sub(j.state.lastFireAt) < j.state.rule.cooldown {
		return
	}
	e.enqueueLocked(j.state, now)
}

func (e *Engine) watchLoop() {
	defer e.watchWG.Done()
	ticker := time.NewTicker(e.watchInterval)
	defer ticker.Stop()
	for {
		select {
		case <-e.watchCtx.Done():
			return
		case <-ticker.C:
			snap, err := e.loadSnapshot()
			if err != nil {
				logger.Warn("triggerrule: reload rules failed: %v", err)
				continue
			}
			e.mu.Lock()
			if e.running && snap.hash != e.ruleHash {
				e.applySnapshotLocked(snap)
				e.ruleHash = snap.hash
			}
			e.mu.Unlock()
		}
	}
}

// applySnapshotLocked 用新快照替换规则；签名未变的规则保留边沿/冷却状态。
func (e *Engine) applySnapshotLocked(snap *ruleSnapshot) {
	newRules := make(map[string]*ruleState, len(snap.rules))
	for _, cr := range snap.rules {
		if old := e.rules[cr.id]; old != nil && old.rule.sig == cr.sig {
			old.rule = cr
			newRules[cr.id] = old
			continue
		}
		newRules[cr.id] = &ruleState{rule: cr}
	}

	byAddr := make(map[string]map[string]struct{})
	condIDs := make(map[string]struct{})
	for _, cr := range snap.rules {
		if cr.status != 1 {
			continue
		}
		for _, g := range cr.groups {
			for _, c := range g.conditions {
				condIDs[c.addressID] = struct{}{}
				set := byAddr[c.addressID]
				if set == nil {
					set = make(map[string]struct{})
					byAddr[c.addressID] = set
				}
				set[cr.id] = struct{}{}
			}
		}
	}

	newByAddr := make(map[string][]string, len(byAddr))
	for addrID, set := range byAddr {
		ids := make([]string, 0, len(set))
		for id := range set {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		newByAddr[addrID] = ids
	}

	for id := range e.samples {
		if _, ok := condIDs[id]; !ok {
			delete(e.samples, id)
		}
	}

	e.rules = newRules
	e.ruleByAddress = newByAddr
	e.conditionIDs = condIDs
}

// ---------------- 规则加载与编译 ----------------

type ruleSnapshot struct {
	rules []*compiledRule
	hash  uint64
}

type parsedRule struct {
	row       po.TriggerRule
	actionIDs []string
	conds     Conditions
}

func (e *Engine) loadSnapshot() (*ruleSnapshot, error) {
	if e.db == nil {
		return &ruleSnapshot{}, nil
	}

	var rows []po.TriggerRule
	if err := e.db.Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("load trigger rules: %w", err)
	}
	// 稳定排序：哈希包含原始行，DB 返回顺序变化不应被当成一次配置变更。
	sort.Slice(rows, func(i, j int) bool { return rows[i].ID < rows[j].ID })

	parsed := make([]parsedRule, 0, len(rows))
	idSet := make(map[string]struct{})
	for _, row := range rows {
		var actionIDs []string
		if err := json.Unmarshal([]byte(row.ActionAddressIDs), &actionIDs); err != nil {
			logger.Warn("triggerrule: rule %s action_address_ids is not a JSON array, skipped: %v", row.ID, err)
			continue
		}
		conds, err := ParseConditions([]byte(row.ConditionsJSON))
		if err != nil {
			logger.Warn("triggerrule: rule %s conditions invalid, skipped: %v", row.ID, err)
			continue
		}
		for _, id := range actionIDs {
			idSet[id] = struct{}{}
		}
		for _, g := range conds.Groups {
			for _, c := range g.Conditions {
				idSet[c.AddressID] = struct{}{}
			}
		}
		parsed = append(parsed, parsedRule{row: row, actionIDs: actionIDs, conds: conds})
	}

	addrByID := make(map[string]po.DeviceAddress, len(idSet))
	if len(idSet) > 0 {
		ids := make([]string, 0, len(idSet))
		for id := range idSet {
			ids = append(ids, id)
		}
		var addrs []po.DeviceAddress
		if err := e.db.Where("id IN ?", ids).Find(&addrs).Error; err != nil {
			return nil, fmt.Errorf("load trigger addresses: %w", err)
		}
		for _, a := range addrs {
			addrByID[a.ID] = a
		}
	}

	devSet := make(map[string]struct{})
	for _, a := range addrByID {
		devSet[a.DeviceID] = struct{}{}
	}
	devByID := make(map[string]po.Device, len(devSet))
	if len(devSet) > 0 {
		ids := make([]string, 0, len(devSet))
		for id := range devSet {
			ids = append(ids, id)
		}
		var devs []po.Device
		if err := e.db.Where("id IN ?", ids).Find(&devs).Error; err != nil {
			return nil, fmt.Errorf("load trigger devices: %w", err)
		}
		for _, d := range devs {
			devByID[d.ID] = d
		}
	}

	var groups []po.CollectorGroup
	if err := e.db.Find(&groups).Error; err != nil {
		return nil, fmt.Errorf("load collector groups: %w", err)
	}
	groupByID := make(map[string]po.CollectorGroup, len(groups))
	for _, g := range groups {
		groupByID[g.ID] = g
	}

	out := &ruleSnapshot{rules: make([]*compiledRule, 0, len(parsed))}
	for _, p := range parsed {
		cr := &compiledRule{
			id:              p.row.ID,
			code:            p.row.Code,
			name:            p.row.Name,
			status:          p.row.Status,
			actionDeviceID:  p.row.ActionDeviceID,
			targetChannelID: p.row.TargetChannelID,
			cooldown:        time.Duration(p.row.CooldownMs) * time.Millisecond,
			timeout:         time.Duration(p.row.TimeoutMs) * time.Millisecond,
		}
		if cr.timeout <= 0 {
			cr.timeout = defaultTimeout
		}

		actionAddrs := make([]po.DeviceAddress, 0, len(p.actionIDs))
		for _, id := range p.actionIDs {
			a, ok := addrByID[id]
			if !ok {
				logger.Warn("triggerrule: rule %s action address %s not found", p.row.ID, id)
				continue
			}
			if a.DeviceID != p.row.ActionDeviceID {
				// 防御直接改库：动作点位必须属于动作设备，否则会把别处的地址
				// 塞进这台设备的驱动会话里。
				logger.Warn("triggerrule: rule %s action address %s belongs to device %s, want %s",
					p.row.ID, id, a.DeviceID, p.row.ActionDeviceID)
				continue
			}
			actionAddrs = append(actionAddrs, a)
		}
		cr.actionAddresses = actionAddrs

		condAddrs := make([]po.DeviceAddress, 0)
		seenCond := make(map[string]struct{})
		for _, g := range p.conds.Groups {
			cg := compiledGroup{conditions: make([]compiledCondition, 0, len(g.Conditions))}
			for _, c := range g.Conditions {
				a, ok := addrByID[c.AddressID]
				meta := addressMeta{deviceID: a.DeviceID, effectiveInterval: defaultSampleInterval}
				if ok {
					meta.deviceName = devByID[a.DeviceID].Name
					meta.addrName = a.Label
					if meta.addrName == "" {
						meta.addrName = a.Name
					}
					meta.effectiveInterval = effectiveInterval(a, groupByID)
					if _, seen := seenCond[a.ID]; !seen {
						seenCond[a.ID] = struct{}{}
						condAddrs = append(condAddrs, a)
					}
				} else {
					logger.Warn("triggerrule: rule %s condition address %s not found", p.row.ID, c.AddressID)
				}
				cg.conditions = append(cg.conditions, compiledCondition{
					addressID: c.AddressID,
					op:        c.Op,
					values:    c.Values,
					meta:      meta,
				})
			}
			cr.groups = append(cr.groups, cg)
		}

		// 引用到的设备/组按首次出现顺序去重，参与签名；地址元数据变化也会重置状态。
		devs := make([]po.Device, 0)
		seenDev := make(map[string]struct{})
		groupsUsed := make([]po.CollectorGroup, 0)
		seenGroup := make(map[string]struct{})
		for _, a := range append(append([]po.DeviceAddress{}, actionAddrs...), condAddrs...) {
			if d, ok := devByID[a.DeviceID]; ok {
				if _, seen := seenDev[d.ID]; !seen {
					seenDev[d.ID] = struct{}{}
					devs = append(devs, d)
				}
			}
			if g, ok := groupByID[a.GroupID]; ok && a.GroupID != "" {
				if _, seen := seenGroup[g.ID]; !seen {
					seenGroup[g.ID] = struct{}{}
					groupsUsed = append(groupsUsed, g)
				}
			}
		}
		cr.sig = ruleSignature(p.row, actionAddrs, condAddrs, devs, groupsUsed)
		out.rules = append(out.rules, cr)
	}

	// 全局哈希：原始规则行（含坏行）+ 每条规则的签名，稳定排序。
	h := fnv.New64a()
	rawRows, _ := json.Marshal(rows)
	_, _ = h.Write(rawRows)
	sort.Slice(out.rules, func(i, j int) bool { return out.rules[i].id < out.rules[j].id })
	for _, cr := range out.rules {
		_, _ = fmt.Fprintf(h, "|%s:%d", cr.id, cr.sig)
	}
	out.hash = h.Sum64()
	return out, nil
}

func ruleSignature(row po.TriggerRule, actions, conditions []po.DeviceAddress, devices []po.Device, groups []po.CollectorGroup) uint64 {
	input := struct {
		Rule       po.TriggerRule      `json:"rule"`
		Actions    []po.DeviceAddress  `json:"actions"`
		Conditions []po.DeviceAddress  `json:"conditions"`
		Devices    []po.Device         `json:"devices"`
		Groups     []po.CollectorGroup `json:"groups"`
	}{
		Rule:       row,
		Actions:    actions,
		Conditions: conditions,
		Devices:    devices,
		Groups:     groups,
	}
	raw, err := json.Marshal(input)
	if err != nil {
		// 理论不可达（都是可序列化结构）；退化成只按规则本身签名。
		raw, _ = json.Marshal(row)
	}
	h := fnv.New64a()
	_, _ = h.Write(raw)
	return h.Sum64()
}
