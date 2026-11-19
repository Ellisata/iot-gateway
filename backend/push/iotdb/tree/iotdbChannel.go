// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package tree

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/apache/iotdb-client-go/client"
	"gorm.io/gorm"

	"iot-gateway/logger"
	"iot-gateway/model/po"
	"iot-gateway/push"
)

func init() {
	push.RegisterType(push.ChannelTypeMeta{
		Type:  "iotdb-tree",
		Label: "Apache IoTDB Tree",
	}, func(db *gorm.DB, ch *po.PushChannel) (push.Channel, error) {
		return newIoTDBChannel(db, ch)
	})
}

const (
	reconnectInterval = 5 * time.Second
	connectTimeout    = 5 * time.Second
)

type iotdbChannel struct {
	id          string
	name        string
	channelType string
	cfg         *iotdbConfig
	sig         string

	ob        *push.Outbox
	recent    *push.RecentRing
	rowsCount atomic.Uint64
	connected atomic.Bool

	poolMu sync.RWMutex
	pool   *client.SessionPool

	schemaMu sync.Mutex
	schemas  map[string]struct{}

	done    chan struct{}
	mu      sync.Mutex
	running bool
}

func (ch *iotdbChannel) ChannelID() string            { return ch.id }
func (ch *iotdbChannel) ConfigSig() string            { return ch.sig }
func (ch *iotdbChannel) Connected() bool              { return ch.connected.Load() }
func (ch *iotdbChannel) IsTimeSeries() bool           { return true }
func (ch *iotdbChannel) Enqueue(batch push.PushBatch) { ch.ob.Enqueue(batch) }

func newIoTDBChannel(db *gorm.DB, stored *po.PushChannel) (*iotdbChannel, error) {
	cfg, err := parseConfig(stored.ConfigJSON)
	if err != nil {
		return nil, err
	}
	typ := stored.Type
	if typ == "" {
		typ = "iotdb-tree"
	}
	ch := &iotdbChannel{
		id:          stored.ID,
		name:        stored.Name,
		channelType: typ,
		cfg:         cfg,
		sig:         stored.ID + "|" + stored.Name + "|" + stored.ConfigJSON,
		recent:      push.NewRecentRing(push.RecentPushRingSize),
		schemas:     make(map[string]struct{}),
	}
	ch.ob = push.NewOutbox(push.OutboxConfig{
		Tag:          "iotdb",
		ID:           stored.ID,
		Name:         stored.Name,
		DB:           db,
		SpoolEnabled: cfg.spoolEnabled(),
		SpoolCap:     cfg.spoolBatchCap(),
		Conn:         ch,
	})
	return ch, nil
}

func (ch *iotdbChannel) TestConnectivity() error {
	session := client.NewSession(ch.sessionConfig())
	if err := session.Open(false, int(connectTimeout.Milliseconds())); err != nil {
		return fmt.Errorf("iotdb: connect failed: %w", err)
	}
	defer session.Close()
	if err := ch.ensureDatabase(&session); err != nil {
		return fmt.Errorf("iotdb: ensure database failed: %w", err)
	}
	return nil
}

func (ch *iotdbChannel) sessionConfig() *client.Config {
	return &client.Config{
		Host:            ch.cfg.Host,
		Port:            ch.cfg.port(),
		UserName:        ch.cfg.Username,
		Password:        ch.cfg.Password,
		FetchSize:       1024,
		TimeZone:        ch.cfg.TimeZone,
		ConnectRetryMax: 1,
	}
}

func (ch *iotdbChannel) Start() {
	ch.mu.Lock()
	if ch.running {
		ch.mu.Unlock()
		return
	}
	ch.running = true
	ch.done = make(chan struct{})
	ch.mu.Unlock()
	ch.ob.Start()
	go ch.run()
}

func (ch *iotdbChannel) Stop() {
	ch.mu.Lock()
	if !ch.running {
		ch.mu.Unlock()
		return
	}
	ch.running = false
	ch.mu.Unlock()

	ch.ob.Close()
	<-ch.done
	ch.ob.Wait()
	ch.connected.Store(false)

	ch.poolMu.Lock()
	if ch.pool != nil {
		ch.pool.Close()
		ch.pool = nil
	}
	ch.poolMu.Unlock()
}

func (ch *iotdbChannel) run() {
	defer close(ch.done)
	writer := &push.AggregateWriter{
		New:      func() push.Aggregator { return newInsertBuilder(ch.cfg.safeDatabase) },
		Write:    ch.write,
		MaxRows:  ch.cfg.batchRows(),
		Interval: ch.cfg.batchInterval(),
		Observe:  ch.observeWrite,
	}
	var workers sync.WaitGroup
	for i := 0; i < ch.cfg.batchWorkers(); i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			writer.Run(ch.ob)
		}()
	}
	if ch.ob.SpoolEnabled() {
		drainer := &push.GroupedDrainer{
			Tag:     "iotdb",
			Spool:   ch.ob.Spool(),
			New:     func() push.Aggregator { return newInsertBuilder(ch.cfg.safeDatabase) },
			Write:   ch.write,
			MaxRows: ch.cfg.batchRows(),
			Observe: ch.observeWrite,
		}
		workers.Add(1)
		go func() {
			defer workers.Done()
			ch.ob.DrainLoop(drainer.Drain, push.DrainPolicy{Poison: true, Narrow: true})
		}()
	}
	workers.Add(1)
	go func() {
		defer workers.Done()
		ch.ob.ReconnectLoop(reconnectInterval, ch.tryConnect)
	}()
	workers.Wait()
}

func (ch *iotdbChannel) tryConnect() {
	session := client.NewSession(ch.sessionConfig())
	if err := session.Open(false, int(connectTimeout.Milliseconds())); err != nil {
		ch.setConnectionError(fmt.Errorf("connect failed: %w", err))
		return
	}
	defer session.Close()
	if err := ch.ensureDatabase(&session); err != nil {
		ch.setConnectionError(fmt.Errorf("ensure database failed: %w", err))
		return
	}

	ch.poolMu.Lock()
	if ch.pool == nil {
		pool := client.NewSessionPool(&client.PoolConfig{
			Host:            ch.cfg.Host,
			Port:            ch.cfg.port(),
			UserName:        ch.cfg.Username,
			Password:        ch.cfg.Password,
			FetchSize:       1024,
			TimeZone:        ch.cfg.TimeZone,
			ConnectRetryMax: 1,
		}, ch.cfg.batchWorkers(), int(connectTimeout.Milliseconds()), int(connectTimeout.Milliseconds()), false)
		ch.pool = &pool
	}
	ch.poolMu.Unlock()
	ch.connected.Store(true)
	ch.ob.SetLastErr("")
	ch.ob.Notify()
}

func (ch *iotdbChannel) ensureDatabase(session *client.Session) error {
	return createTreeSchema(session, "CREATE DATABASE "+ch.cfg.safeDatabase, client.DatabaseAlreadyExists)
}

// The Tree dialect reports existing schema with a status code instead of
// accepting IF NOT EXISTS. Propagate every other DDL or transport failure.
func createTreeSchema(session *client.Session, stmt string, existsCode int32) error {
	err := session.ExecuteNonQueryStatement(stmt)
	var failure *client.ExecutionError
	if errors.As(err, &failure) {
		if failure.Code == existsCode {
			return nil
		}
		if existsCode == client.PathAlreadyExist && failure.Code == 503 {
			path := strings.Fields(stmt)[2]
			if strings.TrimRight(failure.Message, "s.") == "Path ["+path+"] already exist" {
				return nil
			}
		}
	}
	return err
}

func (ch *iotdbChannel) write(payload string) bool {
	if payload == "" {
		return true
	}
	if !ch.connected.Load() {
		return false
	}
	var rows []iotdbRow
	if err := json.Unmarshal([]byte(payload), &rows); err != nil {
		ch.setWriteError(fmt.Errorf("decode write batch: %w", err), false)
		return false
	}
	if len(rows) == 0 {
		return true
	}

	ch.poolMu.RLock()
	pool := ch.pool
	ch.poolMu.RUnlock()
	if pool == nil {
		return false
	}
	session, err := pool.GetSession()
	if err != nil {
		ch.setWriteError(fmt.Errorf("get session: %w", err), true)
		return false
	}
	defer pool.PutBack(session)

	for _, row := range rows {
		if err := ch.ensurePointSchema(&session, row.Path); err != nil {
			ch.setWriteError(fmt.Errorf("ensure schema for %s: %w", row.Path, err), true)
			return false
		}
	}
	devices, measurements, dataTypes, values, timestamps := splitInsertRows(rows)
	if err := session.InsertRecords(devices, measurements, dataTypes, values, timestamps); err != nil {
		ch.setWriteError(fmt.Errorf("insert records: %w", err), true)
		return false
	}
	ch.ob.MarkPublished(1)
	return true
}

type schemaField struct {
	name     string
	dataType string
	encoding string
}

var pointSchema = []schemaField{
	{name: "value_bool", dataType: "BOOLEAN", encoding: "RLE"},
	{name: "value_int", dataType: "INT64", encoding: "TS_2DIFF"},
	{name: "value_float", dataType: "DOUBLE", encoding: "GORILLA"},
	{name: "value_str", dataType: "TEXT", encoding: "PLAIN"},
	{name: "value_kind", dataType: "TEXT", encoding: "PLAIN"},
	{name: "quality", dataType: "INT32", encoding: "RLE"},
	{name: "protocol", dataType: "TEXT", encoding: "PLAIN"},
	{name: "thing_model_code", dataType: "TEXT", encoding: "PLAIN"},
	{name: "property_code", dataType: "TEXT", encoding: "PLAIN"},
}

func (ch *iotdbChannel) ensurePointSchema(session *client.Session, path string) error {
	ch.schemaMu.Lock()
	defer ch.schemaMu.Unlock()
	if _, exists := ch.schemas[path]; exists {
		return nil
	}
	for _, field := range pointSchema {
		stmt := fmt.Sprintf("CREATE TIMESERIES %s.%s WITH DATATYPE=%s, ENCODING=%s, COMPRESSOR=SNAPPY",
			path, field.name, field.dataType, field.encoding)
		if err := createTreeSchema(session, stmt, client.PathAlreadyExist); err != nil {
			return err
		}
	}
	ch.schemas[path] = struct{}{}
	return nil
}

func (ch *iotdbChannel) setConnectionError(err error) {
	errMsg := "iotdb: " + err.Error()
	logger.Error("%s", errMsg)
	ch.ob.SetLastErr(errMsg)
	ch.connected.Store(false)
}

func (ch *iotdbChannel) setWriteError(err error, connectionFailure bool) {
	errMsg := "iotdb: " + err.Error()
	logger.Error("%s", errMsg)
	ch.ob.SetLastErr(errMsg)
	if connectionFailure {
		ch.connected.Store(false)
	}
}

func (ch *iotdbChannel) Snapshot() push.ChannelStatusVO {
	vo := push.ChannelStatusVO{
		Type:   ch.channelType,
		Broker: ch.cfg.endpoint(),
		Topic:  ch.cfg.safeDatabase,
	}
	ch.ob.FillStatus(&vo)
	vo.RowsCount = ch.rowsCount.Load()
	return vo
}

func (ch *iotdbChannel) observeWrite(observation push.WriteObservation) {
	if ch.recent == nil {
		return
	}
	errMsg := ""
	if observation.OK {
		ch.rowsCount.Add(uint64(observation.Rows))
	} else {
		errMsg = ch.ob.LastErr()
		if errMsg == "" {
			errMsg = "write failed"
		}
	}
	ch.recent.AddString(push.RecentPushMessage{
		At:         time.Now(),
		Target:     ch.cfg.safeDatabase,
		Records:    observation.Rows,
		DurationMs: observation.Duration.Milliseconds(),
		Error:      errMsg,
		FromDrain:  observation.FromDrain,
	}, observation.Payload)
}

func (ch *iotdbChannel) RecentMessages(limit int) []push.RecentPushMessage {
	if ch.recent == nil {
		return []push.RecentPushMessage{}
	}
	return ch.recent.Snapshot(limit)
}
