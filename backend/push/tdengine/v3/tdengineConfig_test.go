package v3

import (
	"database/sql"
	"strings"
	"testing"
	"time"
)

func TestParseConfigDefaults(t *testing.T) {
	cfg, err := parseConfig(`{"host":"127.0.0.1"}`)
	if err != nil {
		t.Fatalf("parseConfig failed: %v", err)
	}
	if cfg.Username != "root" {
		t.Errorf("username default = %q, want root", cfg.Username)
	}
	if cfg.Password != "taosdata" {
		t.Errorf("password default = %q, want taosdata", cfg.Password)
	}
	if cfg.port() != "6041" {
		t.Errorf("port default = %q, want 6041", cfg.port())
	}
	if cfg.Database != defaultDatabase {
		t.Errorf("database default = %q, want %q", cfg.Database, defaultDatabase)
	}
	if cfg.safeDB != "iot" {
		t.Errorf("safeDB default = %q, want iot", cfg.safeDB)
	}
	if cfg.Stable != defaultStable {
		t.Errorf("stable default = %q, want %q", cfg.Stable, defaultStable)
	}
	if !cfg.spoolEnabled() {
		t.Error("spool should be enabled by default")
	}
}

func TestParseConfigFull(t *testing.T) {
	cfg, err := parseConfig(`{"host":"tdengine.internal","port":6042,"username":"u","password":"p@ss","database":"db","stable":"meter","batchWorkers":8,"useSSL":true,"spoolDisabled":true,"spoolMaxBatches":50}`)
	if err != nil {
		t.Fatalf("parseConfig failed: %v", err)
	}
	if cfg.port() != "6042" {
		t.Errorf("port = %q, want 6042", cfg.port())
	}
	if cfg.Username != "u" || cfg.Password != "p@ss" {
		t.Errorf("credentials = %q/%q", cfg.Username, cfg.Password)
	}
	if cfg.safeDB != "db" || cfg.safeStable != "meter" {
		t.Errorf("safe identifiers = %q/%q", cfg.safeDB, cfg.safeStable)
	}
	if cfg.batchWorkers() != 8 {
		t.Errorf("batchWorkers = %d, want 8", cfg.batchWorkers())
	}
	if !cfg.UseSSL {
		t.Error("useSSL should be true")
	}
	if cfg.spoolEnabled() {
		t.Error("spool should be disabled")
	}
	if cfg.spoolBatchCap() != 50 {
		t.Errorf("spoolBatchCap = %d, want 50", cfg.spoolBatchCap())
	}
}

func TestParseConfigErrors(t *testing.T) {
	cases := []struct {
		name string
		json string
	}{
		{"missing host", `{"database":"db"}`},
		{"invalid port", `{"host":"127.0.0.1","database":"db","port":"abc"}`},
		{"bad json", `{not json`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, err := parseConfig(c.json); err == nil {
				t.Error("expected error, got nil")
			}
		})
	}
}

func TestBatchWorkersClamp(t *testing.T) {
	cfg, _ := parseConfig(`{"host":"h","database":"db"}`)
	if cfg.batchWorkers() != defaultBatchWorkers {
		t.Errorf("empty batchWorkers = %d, want %d", cfg.batchWorkers(), defaultBatchWorkers)
	}
	cfg.BatchWorkers = 0
	if cfg.batchWorkers() != defaultBatchWorkers {
		t.Errorf("zero batchWorkers = %d, want default", cfg.batchWorkers())
	}
	cfg.BatchWorkers = 999
	if cfg.batchWorkers() != maxBatchWorkers {
		t.Errorf("oversized batchWorkers = %d, want %d", cfg.batchWorkers(), maxBatchWorkers)
	}
}

func TestBatchRowsClamp(t *testing.T) {
	cfg, _ := parseConfig(`{"host":"h","database":"db"}`)
	if cfg.batchRows() != defaultBatchRows {
		t.Errorf("empty batchRows = %d, want default %d", cfg.batchRows(), defaultBatchRows)
	}
	cfg.BatchRows = 0
	if cfg.batchRows() != defaultBatchRows {
		t.Errorf("zero batchRows = %d, want default", cfg.batchRows())
	}
	cfg.BatchRows = 999999
	if cfg.batchRows() != maxBatchRows {
		t.Errorf("oversized batchRows = %d, want %d", cfg.batchRows(), maxBatchRows)
	}
}

func TestBatchAggregationDefaults(t *testing.T) {
	cfg, _ := parseConfig(`{"host":"h","database":"db"}`)
	if cfg.batchRows() != defaultBatchRows {
		t.Errorf("empty batchRows = %d, want %d", cfg.batchRows(), defaultBatchRows)
	}
	if cfg.batchInterval() != defaultBatchInterval {
		t.Errorf("empty batchIntervalMs = %v, want %v", cfg.batchInterval(), defaultBatchInterval)
	}
}

func TestBatchAggregationOverride(t *testing.T) {
	cfg, _ := parseConfig(`{"host":"h","database":"db","batchRows":5000,"batchIntervalMs":500}`)
	if cfg.batchRows() != 5000 {
		t.Errorf("batchRows = %d, want 5000", cfg.batchRows())
	}
	if cfg.batchInterval() != 500*time.Millisecond {
		t.Errorf("batchInterval = %v, want 500ms", cfg.batchInterval())
	}
	// ≤0 回落默认值
	cfg.BatchRows = 0
	cfg.BatchIntervalMs = -1
	if cfg.batchRows() != defaultBatchRows || cfg.batchInterval() != defaultBatchInterval {
		t.Errorf("non-positive values should fall back to defaults, got %d/%v", cfg.batchRows(), cfg.batchInterval())
	}
}

func TestDsn(t *testing.T) {
	cfg, _ := parseConfig(`{"host":"127.0.0.1","database":"db","username":"root","password":"taosdata"}`)
	dsn := cfg.dsn()
	if !strings.HasPrefix(dsn, "root:taosdata@ws(127.0.0.1:6041)/?") {
		t.Errorf("dsn = %q, want ws protocol and no db in path", dsn)
	}
	if strings.Contains(dsn, "/db") {
		t.Errorf("dsn should not carry database name (pool-safe qualified names), got %q", dsn)
	}
	if !strings.Contains(dsn, "autoReconnect=true") {
		t.Errorf("dsn should enable autoReconnect, got %q", dsn)
	}
}

func TestDsnSSLAndEscapedCredential(t *testing.T) {
	cfg, _ := parseConfig(`{"host":"h","database":"db","username":"root","password":"p@ss:w0rd","useSSL":true}`)
	dsn := cfg.dsn()
	if !strings.HasPrefix(dsn, "root:p%40ss%3Aw0rd@wss(h:6041)/?") {
		t.Errorf("dsn = %q, want wss and escaped password", dsn)
	}
}

func TestEnsureSchemaSQL(t *testing.T) {
	cfg, _ := parseConfig(`{"host":"h","database":"iot_db","stable":"s_hand"}`)
	stmts := cfg.ensureSchemaSQL()
	if len(stmts) != 2 {
		t.Fatalf("schema statements = %d, want 2", len(stmts))
	}
	if stmts[0] != "CREATE DATABASE IF NOT EXISTS iot_db" {
		t.Errorf("create db stmt = %q", stmts[0])
	}
	want := "CREATE STABLE IF NOT EXISTS iot_db.s_hand (`time` TIMESTAMP, value_bool BOOL, value_int BIGINT, value_float DOUBLE, value_str NCHAR(255), value_kind NCHAR(16), quality INT) TAGS (device_id NCHAR(64), device_address_id NCHAR(64))"
	if stmts[1] != want {
		t.Errorf("create stable stmt = %q\nwant %q", stmts[1], want)
	}
}

func TestEnsureSchemaSQLDefaultPrefix(t *testing.T) {
	cfg, _ := parseConfig(`{"host":"h","database":"iot_db"}`)
	stmts := cfg.ensureSchemaSQL()
	if cfg.Stable != "s_hand" {
		t.Errorf("default stable = %q, want s_hand", cfg.Stable)
	}
	if !strings.Contains(stmts[1], "CREATE STABLE IF NOT EXISTS iot_db.s_hand ") {
		t.Errorf("default stable should be s_hand, got %q", stmts[1])
	}
}

func TestDriverRegistered(t *testing.T) {
	// 回归:taosWS 驱动必须经空导入注册,否则运行时 sql.Open 报 unknown driver。
	// sql.Open 仅校验驱动注册与 DSN 解析,不真正建连,无需 TDengine 环境。
	cfg, err := parseConfig(`{"host":"127.0.0.1","database":"db"}`)
	if err != nil {
		t.Fatalf("parseConfig failed: %v", err)
	}
	db, err := sql.Open("taosWS", cfg.dsn())
	if err != nil {
		t.Fatalf("sql.Open(taosWS) failed (driver not registered?): %v", err)
	}
	db.Close()
}

func TestSanitizeIdent(t *testing.T) {
	cases := map[string]string{
		"collected_data": "collected_data",
		"123abc":         "_123abc", // 首字符数字:补下划线(合法标识符不能以数字开头)
		"a.b/c":          "a_b_c",
		"中文表":            "___", // 按 rune 处理,每个汉字一个下划线
		"":               "_",
	}
	for in, want := range cases {
		if got := sanitizeIdent(in); got != want {
			t.Errorf("sanitizeIdent(%q) = %q, want %q", in, got, want)
		}
	}
}
