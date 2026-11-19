// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package tree

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
)

const (
	defaultPort            = 6667
	defaultDatabase        = "root.dbeco"
	defaultBatchWorkers    = 1
	maxBatchWorkers        = 32
	defaultBatchRows       = 1000
	maxBatchRows           = 5000
	defaultBatchInterval   = 200 * time.Millisecond
	defaultSpoolMaxBatches = 100000
)

// iotdbConfig describes an Apache IoTDB Tree-model RPC connection.
type iotdbConfig struct {
	Host            string    `json:"host"`
	Port            portValue `json:"port"`
	Username        string    `json:"username"`
	Password        string    `json:"password"`
	Database        string    `json:"database"`
	BatchWorkers    int       `json:"batchWorkers"`
	BatchRows       int       `json:"batchRows"`
	BatchIntervalMs int       `json:"batchIntervalMs"`
	SpoolDisabled   bool      `json:"spoolDisabled"`
	SpoolMaxBatches int       `json:"spoolMaxBatches"`
	TimeZone        string    `json:"timeZone"`

	safeDatabase string
}

func parseConfig(raw string) (*iotdbConfig, error) {
	var cfg iotdbConfig
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		return nil, fmt.Errorf("iotdb: parse config failed: %w", err)
	}
	if strings.TrimSpace(cfg.Host) == "" {
		return nil, fmt.Errorf("iotdb: config host is required")
	}
	if cfg.Port != "" {
		port, err := strconv.Atoi(string(cfg.Port))
		if err != nil || port < 1 || port > 65535 {
			return nil, fmt.Errorf("iotdb: config port %q invalid", cfg.Port)
		}
	}
	if cfg.Username == "" {
		cfg.Username = "root"
	}
	if cfg.Password == "" {
		cfg.Password = "root"
	}
	if strings.TrimSpace(cfg.Database) == "" {
		cfg.Database = defaultDatabase
	}
	var err error
	cfg.safeDatabase, err = sanitizeDatabase(cfg.Database)
	if err != nil {
		return nil, err
	}
	if cfg.TimeZone == "" {
		cfg.TimeZone = "Asia/Shanghai"
	}
	return &cfg, nil
}

func (c *iotdbConfig) port() string {
	if c.Port != "" {
		return string(c.Port)
	}
	return strconv.Itoa(defaultPort)
}

func (c *iotdbConfig) endpoint() string {
	return fmt.Sprintf("%s:%s", c.Host, c.port())
}

func (c *iotdbConfig) batchWorkers() int {
	if c.BatchWorkers < 1 {
		return defaultBatchWorkers
	}
	if c.BatchWorkers > maxBatchWorkers {
		return maxBatchWorkers
	}
	return c.BatchWorkers
}

func (c *iotdbConfig) batchRows() int {
	if c.BatchRows < 1 {
		return defaultBatchRows
	}
	if c.BatchRows > maxBatchRows {
		return maxBatchRows
	}
	return c.BatchRows
}

func (c *iotdbConfig) batchInterval() time.Duration {
	if c.BatchIntervalMs < 1 {
		return defaultBatchInterval
	}
	return time.Duration(c.BatchIntervalMs) * time.Millisecond
}

func (c *iotdbConfig) spoolEnabled() bool { return !c.SpoolDisabled }

func (c *iotdbConfig) spoolBatchCap() int {
	if c.SpoolMaxBatches < 1 {
		return defaultSpoolMaxBatches
	}
	return c.SpoolMaxBatches
}

// sanitizeDatabase accepts either "sg" or "root.sg" and builds a safe path.
// Node components are restricted to ASCII identifiers so configuration cannot
// inject SQL or change the hierarchy. A database path must not name root itself.
func sanitizeDatabase(database string) (string, error) {
	database = strings.TrimSpace(database)
	database = strings.TrimPrefix(database, "root.")
	if database == "" || database == "root" {
		return "", fmt.Errorf("iotdb: database must be a path below root")
	}
	parts := strings.Split(database, ".")
	for i, part := range parts {
		if part == "" {
			return "", fmt.Errorf("iotdb: database path contains an empty node")
		}
		parts[i] = sanitizeNode(part)
	}
	return "root." + strings.Join(parts, "."), nil
}

func sanitizeNode(value string) string {
	var out strings.Builder
	for i, r := range value {
		switch {
		case r == '_' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z':
			out.WriteRune(r)
		case r >= '0' && r <= '9':
			if i == 0 {
				out.WriteByte('_')
			}
			out.WriteRune(r)
		default:
			out.WriteByte('_')
		}
	}
	if out.Len() == 0 {
		return "_"
	}
	return out.String()
}

type portValue string

func (p *portValue) UnmarshalJSON(raw []byte) error {
	var value string
	if err := json.Unmarshal(raw, &value); err == nil {
		*p = portValue(value)
		return nil
	}
	var number int64
	if err := json.Unmarshal(raw, &number); err == nil {
		*p = portValue(strconv.FormatInt(number, 10))
		return nil
	}
	return fmt.Errorf("iotdb: invalid port %s", string(raw))
}
