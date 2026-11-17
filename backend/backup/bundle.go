// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package backup

import (
	"archive/zip"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"iot-gateway/configFile"
	"iot-gateway/logger"
)

const (
	// FormatVersion 是 .igbak 的格式版本。
	FormatVersion = 1

	// ScopeFull 整库快照（含报警历史、断网缓存等运行数据）。
	ScopeFull = "full"
	// ScopeConfig 仅配置：白名单表 + 外部 yaml/插件清单，不含运行与历史表。
	ScopeConfig = "config"

	bundlePrefix = "iot-gateway_"
	bundleSuffix = ".igbak"
	tmpDirName   = "tmp"
	stagingExt   = ".part"
)

// FileEntry 是包内一个文件的摘要。
type FileEntry struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}

// PluginEntry 是插件清单的一项（P0 记录文件名与摘要，版本留给 P2 从运行中的
// 插件管理器补全）。
type PluginEntry struct {
	File   string `json:"file"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

// Manifest 是 .igbak 的自描述元数据。
type Manifest struct {
	FormatVersion int           `json:"formatVersion"`
	Scope         string        `json:"scope"`
	CreatedAt     string        `json:"createdAt"`
	CreatedBy     string        `json:"createdBy"`
	Gateway       GatewayInfo   `json:"gateway"`
	SchemaVersion string        `json:"schemaVersion"`
	Env           []string      `json:"env,omitempty"`
	DB            FileEntry     `json:"db"`
	Files         []FileEntry   `json:"files,omitempty"`
	Plugins       []PluginEntry `json:"plugins,omitempty"`
	DriverVersion string        `json:"driverVersion,omitempty"`
	Encrypted     bool          `json:"encrypted"`
	Encryption    any           `json:"encryption,omitempty"`
}

// Bundle 是一次成功备份的结果。
type Bundle struct {
	Path     string
	Manifest Manifest
}

// BundleInfo 是备份目录里的一个包。
type BundleInfo struct {
	Name    string
	Path    string
	Scope   string
	Size    int64
	ModTime time.Time
}

// CreateOptions 是生成备份包的输入。
type CreateOptions struct {
	DBPath          string
	BackupDir       string
	Scope           string
	Version         string
	CreatedBy       string
	WorkingDir      string
	PluginDir       string
	PluginGlob      string
	SnapshotTimeout time.Duration
	Now             time.Time
}

// createMu 串行化同一进程内的备份生成。
//
// 调度器（full）、变更防抖（config）与页面手动备份可能同时到达。快照本身走
// 独立连接，但并发执行会争抢 WAL 与磁盘，且会生成多个同秒同名包。串行化后
// 一个进程内任何时刻最多只有一份快照在跑。
var createMu sync.Mutex

// CreateBundle 生成一个 .igbak 备份包。
//
// 流程：
//  1. full：独立连接 VACUUM INTO 整库快照；config：白名单表复制进临时库；
//  2. 对快照做 integrity_check 并读 schema 版本/网关身份；
//  3. 收集磁盘 default.yaml / APP_ENV yaml / drivers.version 与插件清单；
//  4. 写 zip 到 .part，fsync 后 rename 为最终文件（不会出现半包）。
func CreateBundle(ctx context.Context, opts CreateOptions) (*Bundle, error) {
	opts = normalizeCreateOptions(opts)
	if opts.Scope != ScopeFull && opts.Scope != ScopeConfig {
		return nil, fmt.Errorf("未知备份范围 %q（可选 %q / %q）", opts.Scope, ScopeFull, ScopeConfig)
	}

	createMu.Lock()
	defer createMu.Unlock()

	absBackupDir, err := filepath.Abs(opts.BackupDir)
	if err != nil {
		return nil, fmt.Errorf("resolve backup directory: %w", err)
	}
	if err := os.MkdirAll(absBackupDir, 0700); err != nil {
		return nil, fmt.Errorf("create backup directory %s: %w", absBackupDir, err)
	}
	// 目录权限在 Windows 上不生效，在 Unix 上必须收紧：包内含明文凭据。
	_ = os.Chmod(absBackupDir, 0700)

	tmpDir := filepath.Join(absBackupDir, tmpDirName)
	if err := os.MkdirAll(tmpDir, 0700); err != nil {
		return nil, fmt.Errorf("create backup temp directory: %w", err)
	}
	pruneTemp(tmpDir, opts.Now)

	snapPath := filepath.Join(tmpDir, fmt.Sprintf("snapshot-%d-%s.db", opts.Now.UnixNano(), randomHex(4)))
	snapCtx := ctx
	if opts.SnapshotTimeout > 0 {
		var cancel context.CancelFunc
		snapCtx, cancel = context.WithTimeout(ctx, opts.SnapshotTimeout)
		defer cancel()
	}
	var snap *SnapshotInfo
	if opts.Scope == ScopeConfig {
		snap, err = CreateConfigSnapshot(snapCtx, opts.DBPath, snapPath)
	} else {
		snap, err = CreateSnapshot(snapCtx, opts.DBPath, snapPath)
	}
	if err != nil {
		return nil, err
	}
	defer os.Remove(snapPath)

	configFiles, err := collectConfigFiles(opts.WorkingDir)
	if err != nil {
		return nil, err
	}

	plugins, driverVersion, err := collectPlugins(opts.PluginDir, opts.PluginGlob, opts.WorkingDir)
	if err != nil {
		return nil, err
	}
	pluginManifestJSON, err := json.MarshalIndent(struct {
		DriverVersion string        `json:"driverVersion,omitempty"`
		Plugins       []PluginEntry `json:"plugins"`
	}{DriverVersion: driverVersion, Plugins: plugins}, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal plugin manifest: %w", err)
	}
	pluginEntry := entryFromBytes("plugins/manifest.json", pluginManifestJSON)

	gateway := snap.Gateway
	if gateway.Version == "" {
		gateway.Version = opts.Version
	}

	manifest := Manifest{
		FormatVersion: FormatVersion,
		Scope:         opts.Scope,
		CreatedAt:     opts.Now.Format(time.RFC3339),
		CreatedBy:     opts.CreatedBy,
		Gateway:       gateway,
		SchemaVersion: snap.SchemaVersion,
		Env:           setEnvNames(),
		DB: FileEntry{
			Path:   "db/iot-gateway.db",
			SHA256: snap.SHA256,
			Size:   snap.Size,
		},
		Files:         append(configFileEntries(configFiles), pluginEntry),
		Plugins:       plugins,
		DriverVersion: driverVersion,
		Encrypted:     false,
	}
	manifestJSON, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal manifest: %w", err)
	}

	name := uniqueBundleName(absBackupDir, bundleName(manifest.Gateway.ID, opts.Scope, opts.Now))
	finalPath := filepath.Join(absBackupDir, name)
	stagingPath := filepath.Join(tmpDir, name+stagingExt)
	out, err := os.OpenFile(stagingPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
	if err != nil {
		return nil, fmt.Errorf("create bundle staging file: %w", err)
	}

	writeErr := writeZip(out, manifestJSON, snap.Path, configFiles, pluginManifestJSON, opts.Now)
	if writeErr == nil {
		writeErr = out.Sync()
	}
	closeErr := out.Close()
	if writeErr != nil {
		_ = os.Remove(stagingPath)
		return nil, fmt.Errorf("write bundle: %w", writeErr)
	}
	if closeErr != nil {
		_ = os.Remove(stagingPath)
		return nil, fmt.Errorf("close bundle: %w", closeErr)
	}
	_ = os.Chmod(stagingPath, 0600)
	if err := os.Rename(stagingPath, finalPath); err != nil {
		_ = os.Remove(stagingPath)
		return nil, fmt.Errorf("finalize bundle %s: %w", finalPath, err)
	}
	Audit(absBackupDir, AuditEvent{
		Action:   "create",
		Operator: opts.CreatedBy,
		Source:   "backup",
		Bundle:   name,
		SHA256:   manifest.DB.SHA256,
		Scope:    manifest.Scope,
		Result:   "ok",
	})
	logger.Info("backup: created %s (%s, scope=%s, schema=%s)",
		finalPath, humanSize(fileSize(finalPath)), manifest.Scope, manifest.SchemaVersion)
	return &Bundle{Path: finalPath, Manifest: manifest}, nil
}

func normalizeCreateOptions(opts CreateOptions) CreateOptions {
	if strings.TrimSpace(opts.DBPath) == "" {
		opts.DBPath = defaultDBPath
	}
	if strings.TrimSpace(opts.BackupDir) == "" {
		opts.BackupDir = defaultBackupDir
	}
	if strings.TrimSpace(opts.Scope) == "" {
		opts.Scope = ScopeFull
	}
	if strings.TrimSpace(opts.CreatedBy) == "" {
		opts.CreatedBy = "manual"
	}
	if strings.TrimSpace(opts.PluginGlob) == "" {
		opts.PluginGlob = "iot-driver-*"
	}
	if strings.TrimSpace(opts.WorkingDir) == "" {
		if wd, err := os.Getwd(); err == nil {
			opts.WorkingDir = wd
		}
	}
	if opts.Now.IsZero() {
		opts.Now = time.Now()
	}
	return opts
}

type externalConfigFile struct {
	zipPath string
	srcPath string
	entry   FileEntry
}

func collectConfigFiles(workDir string) ([]externalConfigFile, error) {
	if strings.TrimSpace(workDir) == "" {
		return nil, nil
	}
	candidates := []struct {
		src     string
		zipPath string
	}{
		{"default.yaml", "config/default.yaml"},
		{"drivers.version", "config/drivers.version"},
	}
	if env := strings.TrimSpace(os.Getenv("APP_ENV")); env != "" {
		candidates = append(candidates, struct {
			src     string
			zipPath string
		}{env + ".yaml", "config/" + env + ".yaml"})
	}

	var files []externalConfigFile
	for _, c := range candidates {
		src := filepath.Join(workDir, c.src)
		info, err := os.Stat(src)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, fmt.Errorf("stat external config %s: %w", src, err)
		}
		if !info.Mode().IsRegular() {
			continue
		}
		sum, size, err := fileSHA256(src)
		if err != nil {
			return nil, fmt.Errorf("hash external config %s: %w", src, err)
		}
		files = append(files, externalConfigFile{
			zipPath: c.zipPath,
			srcPath: src,
			entry:   FileEntry{Path: c.zipPath, SHA256: sum, Size: size},
		})
	}
	return files, nil
}

func collectPlugins(pluginDir, glob, workDir string) ([]PluginEntry, string, error) {
	var plugins []PluginEntry
	if strings.TrimSpace(pluginDir) != "" {
		if info, err := os.Stat(pluginDir); err == nil && info.IsDir() {
			matches, err := filepath.Glob(filepath.Join(pluginDir, glob))
			if err != nil {
				return nil, "", fmt.Errorf("glob plugins: %w", err)
			}
			sort.Strings(matches)
			for _, path := range matches {
				info, err := os.Stat(path)
				if err != nil {
					return nil, "", fmt.Errorf("stat plugin %s: %w", path, err)
				}
				if !info.Mode().IsRegular() {
					continue
				}
				sum, size, err := fileSHA256(path)
				if err != nil {
					return nil, "", fmt.Errorf("hash plugin %s: %w", path, err)
				}
				plugins = append(plugins, PluginEntry{
					File:   filepath.Base(path),
					Size:   size,
					SHA256: sum,
				})
			}
		}
	}
	return plugins, readDriverVersion(workDir), nil
}

func readDriverVersion(workDir string) string {
	if strings.TrimSpace(workDir) == "" {
		return ""
	}
	raw, err := os.ReadFile(filepath.Join(workDir, "drivers.version"))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(raw))
}

func configFileEntries(files []externalConfigFile) []FileEntry {
	if len(files) == 0 {
		return nil
	}
	out := make([]FileEntry, 0, len(files))
	for _, f := range files {
		out = append(out, f.entry)
	}
	return out
}

func entryFromBytes(name string, data []byte) FileEntry {
	return FileEntry{Path: name, SHA256: sha256Hex(data), Size: int64(len(data))}
}

func writeZip(out *os.File, manifestJSON []byte, snapPath string,
	configFiles []externalConfigFile, pluginManifestJSON []byte, mod time.Time) error {
	zw := zip.NewWriter(out)
	if err := addBytes(zw, "manifest.json", manifestJSON, mod); err != nil {
		return err
	}
	if err := addFile(zw, "db/iot-gateway.db", snapPath, mod); err != nil {
		return err
	}
	for _, f := range configFiles {
		if err := addFile(zw, f.zipPath, f.srcPath, mod); err != nil {
			return err
		}
	}
	if err := addBytes(zw, "plugins/manifest.json", pluginManifestJSON, mod); err != nil {
		return err
	}
	return zw.Close()
}

func addFile(zw *zip.Writer, name, src string, mod time.Time) error {
	f, err := os.Open(src)
	if err != nil {
		return err
	}
	defer f.Close()

	hdr := &zip.FileHeader{Name: name, Method: zip.Deflate, Modified: mod}
	hdr.SetMode(0600)
	w, err := zw.CreateHeader(hdr)
	if err != nil {
		return err
	}
	_, err = io.Copy(w, f)
	return err
}

func addBytes(zw *zip.Writer, name string, data []byte, mod time.Time) error {
	hdr := &zip.FileHeader{Name: name, Method: zip.Deflate, Modified: mod}
	hdr.SetMode(0600)
	w, err := zw.CreateHeader(hdr)
	if err != nil {
		return err
	}
	_, err = w.Write(data)
	return err
}

func bundleName(gatewayID, scope string, now time.Time) string {
	id8 := gatewayID
	if len(id8) > 8 {
		id8 = id8[:8]
	}
	if id8 == "" {
		id8 = "local"
	}
	return fmt.Sprintf("%s%s_%s_%s%s", bundlePrefix, id8, now.Format("20060102-150405"), scope, bundleSuffix)
}

// uniqueBundleName 在同秒内已有同名包时追加 -N，避免覆盖。
func uniqueBundleName(dir, base string) string {
	name := base
	for i := 1; ; i++ {
		if _, err := os.Stat(filepath.Join(dir, name)); os.IsNotExist(err) {
			return name
		}
		name = strings.TrimSuffix(base, bundleSuffix) + fmt.Sprintf("-%d%s", i, bundleSuffix)
	}
}

var bundleNameRe = regexp.MustCompile(`^iot-gateway_[A-Za-z0-9]+_\d{8}-\d{6}_(full|config)(-\d+)?\.igbak$`)

// ListBundles 列出备份目录里由本服务生成的包（按时间倒序）。
// 不符合命名规则的文件一律忽略 —— 保留策略绝不能删除用户手工放入的文件。
func ListBundles(dir string) ([]BundleInfo, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []BundleInfo
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		m := bundleNameRe.FindStringSubmatch(name)
		if m == nil {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		out = append(out, BundleInfo{
			Name:    name,
			Path:    filepath.Join(dir, name),
			Scope:   m[1],
			Size:    info.Size(),
			ModTime: info.ModTime(),
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].ModTime.Equal(out[j].ModTime) {
			return out[i].Name > out[j].Name
		}
		return out[i].ModTime.After(out[j].ModTime)
	})
	return out, nil
}

// ApplyRetention 按份数与总大小清理旧包。
//
// 删除优先级（§6.2）：先删最旧的 config，再删最旧的 full；full 至少保留
// min(4, keep) 份，避免高频 config 包把整库备份挤光。无论配额多小，至少保留
// 最新一份包。不符合命名规则的文件一律不动。
//
// 返回值是被删除的文件名。
func ApplyRetention(dir string, keep, maxTotalMB int) ([]string, error) {
	if keep < 1 {
		keep = 1
	}
	infos, err := ListBundles(dir)
	if err != nil {
		return nil, err
	}
	// ListBundles 是新到旧；删除总从尾部（最旧）挑。
	var total int64
	for _, b := range infos {
		total += b.Size
	}
	minFull := keep
	if minFull > 4 {
		minFull = 4
	}

	var deleted []string
	fullCount := func() int {
		n := 0
		for _, b := range infos {
			if b.Scope == ScopeFull {
				n++
			}
		}
		return n
	}
	// pick 返回应删除的下标；没有可删的（只剩受保护的 full）时返回 -1。
	pick := func() int {
		for i := len(infos) - 1; i >= 0; i-- {
			if infos[i].Scope == ScopeConfig {
				return i
			}
		}
		if fullCount() > minFull {
			for i := len(infos) - 1; i >= 0; i-- {
				if infos[i].Scope == ScopeFull {
					return i
				}
			}
		}
		return -1
	}
	removeAt := func(i int) bool {
		target := infos[i]
		if err := os.Remove(target.Path); err != nil && !os.IsNotExist(err) {
			return false
		}
		total -= target.Size
		infos = append(infos[:i], infos[i+1:]...)
		deleted = append(deleted, target.Name)
		return true
	}

	for len(infos) > keep {
		i := pick()
		if i < 0 || !removeAt(i) {
			break
		}
	}
	if maxTotalMB > 0 {
		limit := int64(maxTotalMB) * 1024 * 1024
		for total > limit && len(infos) > 1 {
			i := pick()
			if i < 0 || !removeAt(i) {
				break
			}
		}
	}
	return deleted, nil
}

// pruneTemp 清理临时目录里超过 24 小时的残留（崩溃留下的 .part / 快照）。
func pruneTemp(tmpDir string, now time.Time) {
	entries, err := os.ReadDir(tmpDir)
	if err != nil {
		return
	}
	for _, e := range entries {
		info, err := e.Info()
		if err != nil {
			continue
		}
		if now.Sub(info.ModTime()) > 24*time.Hour {
			_ = os.RemoveAll(filepath.Join(tmpDir, e.Name()))
		}
	}
}

// setEnvNames 只记录被设置过的环境变量名，不记录值。
func setEnvNames() []string {
	names := []string{
		"IOT_GATEWAY_JWT_SECRET",
		"IOT_GATEWAY_ADMIN_PASSWORD",
		"IOT_GATEWAY_NAME",
		"IOT_GATEWAY_VERSION",
		"APP_ENV",
	}
	var out []string
	for _, name := range names {
		if os.Getenv(name) != "" {
			out = append(out, name)
		}
	}
	return out
}

// sha256Hex 计算字节摘要。
func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// randomHex 生成 n 字节的十六进制随机串（用于临时文件名，避免并发撞名）。
func randomHex(n int) string {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(buf)
}

// BuildOptionsFromConfig 是 scheduler / CLI 共用的选项构造，避免两处默认值漂移。
func BuildOptionsFromConfig(cfg *configFile.Config, dbOverride, backupDirOverride, scope, createdBy string) CreateOptions {
	eff := EffectiveConfig(cfg)
	if scope == "" {
		scope = eff.Scope
	}
	return CreateOptions{
		DBPath:          DBPath(cfg, dbOverride),
		BackupDir:       BackupDir(cfg, backupDirOverride),
		Scope:           scope,
		Version:         configFile.ResolveGatewayVersion(cfg),
		CreatedBy:       createdBy,
		PluginDir:       ResolvePluginDir(cfg),
		PluginGlob:      ResolvePluginGlob(cfg),
		SnapshotTimeout: eff.SnapshotTimeout,
	}
}
