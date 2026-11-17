// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package backup

import (
	"archive/zip"
	"context"
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
	"time"

	"iot-gateway/database/sqlite"
	"iot-gateway/utils"
)

const (
	identityTakeover = "takeover"
	identityClone    = "clone"
)

// VerifiedBundle 是校验通过、已把数据库快照解到临时目录的备份包。
type VerifiedBundle struct {
	Path     string
	Manifest Manifest
	DBPath   string

	zr    *zip.ReadCloser
	files map[string]*zip.File
	tmp   string
}

// Cleanup 关闭 zip 并删除临时目录。可重复调用。
func (v *VerifiedBundle) Cleanup() {
	if v == nil {
		return
	}
	if v.zr != nil {
		_ = v.zr.Close()
		v.zr = nil
	}
	if v.tmp != "" {
		_ = os.RemoveAll(v.tmp)
		v.tmp = ""
	}
}

// ReadFile 读取包内一个已校验文件的内容（用于恢复外部 yaml）。
func (v *VerifiedBundle) ReadFile(name string) ([]byte, error) {
	if v.zr == nil {
		return nil, fmt.Errorf("bundle is closed")
	}
	f, ok := v.files[name]
	if !ok {
		return nil, fmt.Errorf("bundle file %s not found", name)
	}
	rc, err := f.Open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	return io.ReadAll(rc)
}

// VerifyBundle 打开并校验一个 .igbak：
//   - manifest 可解析且格式版本受支持；
//   - 数据库与全部声明文件的 sha256 一致；
//   - 数据库 integrity_check 返回 ok；
//   - manifest 的 schemaVersion 与快照内 schema_migrations 一致，且不高于当前二进制。
//
// 返回的 VerifiedBundle 把数据库解到了系统临时目录，调用方必须 Cleanup。
func VerifyBundle(path string) (*VerifiedBundle, error) {
	zr, err := zip.OpenReader(path)
	if err != nil {
		return nil, fmt.Errorf("open bundle %s: %w", path, err)
	}
	v := &VerifiedBundle{Path: path, zr: zr, files: make(map[string]*zip.File)}
	success := false
	defer func() {
		if !success {
			v.Cleanup()
		}
	}()

	for _, f := range zr.File {
		v.files[f.Name] = f
	}
	mf, exists := v.files["manifest.json"]
	if !exists {
		return nil, fmt.Errorf("bundle %s: manifest.json missing", path)
	}
	raw, err := readZipFile(mf)
	if err != nil {
		return nil, fmt.Errorf("read manifest: %w", err)
	}
	if err := json.Unmarshal(raw, &v.Manifest); err != nil {
		return nil, fmt.Errorf("parse manifest: %w", err)
	}
	if v.Manifest.FormatVersion != FormatVersion {
		return nil, fmt.Errorf("unsupported bundle format %d (expected %d)", v.Manifest.FormatVersion, FormatVersion)
	}
	if v.Manifest.Scope != ScopeFull && v.Manifest.Scope != ScopeConfig {
		return nil, fmt.Errorf("unsupported bundle scope %q", v.Manifest.Scope)
	}

	// 数据库条目必须固定在 db/iot-gateway.db，避免路径穿越。
	if v.Manifest.DB.Path != "db/iot-gateway.db" {
		return nil, fmt.Errorf("unexpected database path %q in manifest", v.Manifest.DB.Path)
	}
	dbFile, ok := v.files[v.Manifest.DB.Path]
	if !ok {
		return nil, fmt.Errorf("database entry %s missing", v.Manifest.DB.Path)
	}
	if err := verifyZipEntry(dbFile, v.Manifest.DB); err != nil {
		return nil, err
	}
	for _, fe := range v.Manifest.Files {
		f, ok := v.files[fe.Path]
		if !ok {
			return nil, fmt.Errorf("bundle file %s missing", fe.Path)
		}
		if err := verifyZipEntry(f, fe); err != nil {
			return nil, err
		}
	}

	tmp, err := os.MkdirTemp("", "iot-gateway-restore-*")
	if err != nil {
		return nil, fmt.Errorf("create restore temp directory: %w", err)
	}
	v.tmp = tmp
	v.DBPath = filepath.Join(tmp, "iot-gateway.db")
	if err := extractZipFile(dbFile, v.DBPath); err != nil {
		return nil, fmt.Errorf("extract database snapshot: %w", err)
	}

	_, schema, err := InspectSnapshot(v.DBPath)
	if err != nil {
		return nil, err
	}
	if v.Manifest.SchemaVersion != "" && schema != v.Manifest.SchemaVersion {
		return nil, fmt.Errorf("schema version mismatch: manifest %q, snapshot %q",
			v.Manifest.SchemaVersion, schema)
	}
	// 以快照内 schema_migrations 为准：manifest 可被改写，数据库自己记录的
	// 已应用迁移才是真源。manifest 为空时更不能因此跳过降级保护。
	effectiveSchema := schema
	if effectiveSchema == "" {
		effectiveSchema = v.Manifest.SchemaVersion
	}
	if err := CheckSchemaCompatibility(effectiveSchema); err != nil {
		return nil, err
	}

	success = true
	return v, nil
}

// CheckSchemaCompatibility 拒绝“用旧二进制恢复更新 schema 的包”。
// 旧包（schema 更旧）允许，启动时由迁移器补齐。
func CheckSchemaCompatibility(bundleVersion string) error {
	latest := sqlite.LatestMigration()
	if bundleVersion == "" || latest == "" {
		return nil
	}
	// 迁移文件名是零填充顺序编号，字典序即执行序。
	if bundleVersion > latest {
		return fmt.Errorf("备份包 schema %s 比当前二进制 %s 新，请使用对应或更新的网关版本恢复",
			bundleVersion, latest)
	}
	return nil
}

// RestoreOptions 是恢复动作的输入。
type RestoreOptions struct {
	BundlePath     string
	DBPath         string
	Identity       string // takeover（默认）| clone
	RestoreConfig  bool
	CurrentVersion string
	WorkingDir     string
	Now            time.Time
}

// RestoreResult 描述一次恢复的结果。
type RestoreResult struct {
	Manifest       Manifest
	Identity       string
	NewID          string
	NewName        string
	PreRestore     []string
	ConfigRestored []string
	TablesRestored []string
	Warnings       []string
}

// Restore 校验并恢复一个 .igbak。
//
// 必须满足两个前提：
//   - 网关已停止（通过数据目录排他锁确认）；
//   - 目标目录可写。
//
// full 包：db / -wal / -shm 三件套一起改名，再放入新库，任何一步失败都尝试
// 回滚；旧库保留为 *.pre-restore-<时间戳>。
//
// config 包：**不做整库替换**，只把白名单表在一个事务里复制回当前库，运行与
// 历史表（alarm / push_outbox / control_log ...）原样保留。恢复前先用
// VACUUM INTO 生成一份完整库副本，事务失败时天然回滚。
func Restore(ctx context.Context, opts RestoreOptions) (*RestoreResult, error) {
	absDB, vb, err := prepareRestore(opts)
	if err != nil {
		return nil, err
	}
	defer vb.Cleanup()

	lock, err := AcquireLock(filepath.Dir(absDB))
	if err != nil {
		return nil, fmt.Errorf("网关可能仍在运行，拒绝恢复（请先停止服务）: %w", err)
	}
	defer lock.Release()

	return restoreVerified(ctx, vb, absDB, opts)
}

// prepareRestore 归一化参数并校验包，返回绝对库路径与已解出快照的包。
func prepareRestore(opts RestoreOptions) (string, *VerifiedBundle, error) {
	if opts.Now.IsZero() {
		opts.Now = time.Now()
	}
	if strings.TrimSpace(opts.DBPath) == "" {
		opts.DBPath = defaultDBPath
	}
	if strings.TrimSpace(opts.Identity) == "" {
		opts.Identity = identityTakeover
	}
	if opts.Identity != identityTakeover && opts.Identity != identityClone {
		return "", nil, fmt.Errorf("未知的身份模式 %q（可选 takeover / clone）", opts.Identity)
	}
	absDB, err := filepath.Abs(opts.DBPath)
	if err != nil {
		return "", nil, fmt.Errorf("resolve database path: %w", err)
	}
	vb, err := VerifyBundle(opts.BundlePath)
	if err != nil {
		return "", nil, err
	}
	return absDB, vb, nil
}

// restoreVerified 执行恢复。调用方必须已持有数据目录锁。
func restoreVerified(ctx context.Context, vb *VerifiedBundle, absDB string, opts RestoreOptions) (*RestoreResult, error) {
	result := &RestoreResult{Manifest: vb.Manifest, Identity: opts.Identity}
	if opts.Now.IsZero() {
		opts.Now = time.Now()
	}
	if strings.TrimSpace(opts.Identity) == "" {
		opts.Identity = identityTakeover
		result.Identity = opts.Identity
	}

	// 版本/插件提示不阻塞恢复：现场往往就是要在版本不完全一致时先救回来。
	if opts.CurrentVersion != "" && vb.Manifest.Gateway.Version != "" &&
		vb.Manifest.Gateway.Version != opts.CurrentVersion {
		result.Warnings = append(result.Warnings,
			fmt.Sprintf("备份包由网关版本 %s 创建，当前二进制为 %s",
				vb.Manifest.Gateway.Version, opts.CurrentVersion))
	}
	result.Warnings = append(result.Warnings,
		"备份包不包含插件二进制；请确认备机的 plugins/ 与备份时同版本")

	ts := opts.Now.Format("20060102-150405")
	if vb.Manifest.Scope == ScopeConfig {
		result.Warnings = append(result.Warnings,
			"config 范围恢复只覆盖配置表；报警历史、控制日志与断网缓存保持当前状态")
		if err := restoreConfigTables(ctx, vb, absDB, opts, ts, result); err != nil {
			return nil, err
		}
		prunePreRestore(absDB, 2)
		return result, nil
	}

	if opts.Identity == identityClone {
		id, name, err := cloneGatewayIdentity(vb.DBPath)
		if err != nil {
			return nil, err
		}
		result.NewID, result.NewName = id, name
	}

	renamed, err := renameTriple(absDB, ts)
	if err != nil {
		return nil, err
	}
	for _, r := range renamed {
		result.PreRestore = append(result.PreRestore, r.to)
	}

	if err := copyFile(vb.DBPath, absDB, 0600); err != nil {
		rollbackRestore(absDB, renamed)
		return nil, fmt.Errorf("install restored database: %w", err)
	}
	// 旧 WAL/SHM 已随三件套改名；如果新库旁边又出现它们，说明状态不对。
	for _, suffix := range []string{"-wal", "-shm"} {
		if _, err := os.Stat(absDB + suffix); err == nil {
			rollbackRestore(absDB, renamed)
			return nil, fmt.Errorf("unexpected stale %s after restore; aborted", absDB+suffix)
		}
	}

	if opts.RestoreConfig {
		restored, warns := restoreConfigFiles(vb, opts.WorkingDir, ts)
		result.ConfigRestored = restored
		result.Warnings = append(result.Warnings, warns...)
	}

	prunePreRestore(absDB, 2)
	return result, nil
}

type renameRecord struct {
	from string
	to   string
}

func renameTriple(dbPath, ts string) ([]renameRecord, error) {
	preBase := dbPath + ".pre-restore-" + ts
	var recs []renameRecord
	for _, suffix := range []string{"", "-wal", "-shm"} {
		from := dbPath + suffix
		if _, err := os.Stat(from); err != nil {
			if os.IsNotExist(err) {
				continue
			}
			rollbackRenames(recs)
			return nil, fmt.Errorf("stat %s: %w", from, err)
		}
		to := uniquePath(preBase + suffix)
		if err := os.Rename(from, to); err != nil {
			rollbackRenames(recs)
			return nil, fmt.Errorf("move %s aside: %w", from, err)
		}
		recs = append(recs, renameRecord{from: from, to: to})
	}
	return recs, nil
}

func rollbackRenames(recs []renameRecord) {
	for i := len(recs) - 1; i >= 0; i-- {
		_ = os.Rename(recs[i].to, recs[i].from)
	}
}

func rollbackRestore(dbPath string, recs []renameRecord) {
	_ = os.Remove(dbPath)
	_ = os.Remove(dbPath + "-wal")
	_ = os.Remove(dbPath + "-shm")
	rollbackRenames(recs)
}

func uniquePath(path string) string {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return path
	}
	for i := 1; ; i++ {
		candidate := fmt.Sprintf("%s-%d", path, i)
		if _, err := os.Stat(candidate); os.IsNotExist(err) {
			return candidate
		}
	}
}

func cloneGatewayIdentity(dbPath string) (string, string, error) {
	db, err := sqlite.OpenStandalone(dbPath)
	if err != nil {
		return "", "", err
	}
	defer sqlite.CloseDB(db)

	var row struct {
		ID   string `gorm:"column:id"`
		Name string `gorm:"column:name"`
	}
	if err := db.Raw("SELECT id, name FROM iot_gateway LIMIT 1").Scan(&row).Error; err != nil {
		// 空库（首次启动前）没有身份行，启动时 seedGateway 会建一条。
		return "", "", nil
	}
	if row.ID == "" {
		return "", "", nil
	}
	newID := utils.GenerateUUIDV7()
	base := strings.TrimSpace(row.Name)
	if base == "" {
		base = "iot-gateway"
	}
	newName := fmt.Sprintf("%s-copy-%s", base, newID[:4])
	if err := db.Exec("UPDATE iot_gateway SET id = ?, name = ?", newID, newName).Error; err != nil {
		return "", "", fmt.Errorf("clone gateway identity: %w", err)
	}
	return newID, newName, nil
}

// restoreConfigFiles 把包内的 config/*.yaml 恢复到工作目录（默认不启用）。
// 只处理 .yaml：config/drivers.version 是构建期真源，不属于运行目录。
func restoreConfigFiles(vb *VerifiedBundle, workDir, ts string) ([]string, []string) {
	if strings.TrimSpace(workDir) == "" {
		wd, err := os.Getwd()
		if err != nil {
			return nil, []string{fmt.Sprintf("解析工作目录失败，未恢复外部配置: %v", err)}
		}
		workDir = wd
	}
	var restored []string
	var warns []string
	for _, fe := range vb.Manifest.Files {
		if !strings.HasPrefix(fe.Path, "config/") || !strings.HasSuffix(strings.ToLower(fe.Path), ".yaml") {
			continue
		}
		name := filepath.Base(fe.Path)
		data, err := vb.ReadFile(fe.Path)
		if err != nil {
			warns = append(warns, fmt.Sprintf("读取包内配置 %s 失败: %v", fe.Path, err))
			continue
		}
		target := filepath.Join(workDir, name)
		if _, err := os.Stat(target); err == nil {
			backup := uniquePath(target + ".pre-restore-" + ts)
			if err := os.Rename(target, backup); err != nil {
				warns = append(warns, fmt.Sprintf("备份现有配置 %s 失败: %v", target, err))
				continue
			}
		}
		if err := writeFileSynced(target, data, 0600); err != nil {
			warns = append(warns, fmt.Sprintf("恢复配置 %s 失败: %v", target, err))
			continue
		}
		restored = append(restored, target)
	}
	return restored, warns
}

// 匹配 <db>.pre-restore-<yyyyMMdd-HHmmss>[-N][-wal|-shm]。
var preRestoreRe = regexp.MustCompile(`^(.*\.pre-restore-)(\d{8}-\d{6})(.*)$`)

// prunePreRestore 只保留最近 keepSets 组 *.pre-restore-* 副本。
// 不匹配该模式的文件一律不动。
func prunePreRestore(dbPath string, keepSets int) {
	if keepSets < 1 {
		keepSets = 1
	}
	dir := filepath.Dir(dbPath)
	base := filepath.Base(dbPath) + ".pre-restore-"
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	sets := map[string][]string{}
	for _, e := range entries {
		if e.IsDir() || !strings.HasPrefix(e.Name(), base) {
			continue
		}
		m := preRestoreRe.FindStringSubmatch(e.Name())
		if m == nil {
			continue
		}
		sets[m[2]] = append(sets[m[2]], filepath.Join(dir, e.Name()))
	}
	if len(sets) <= keepSets {
		return
	}
	keys := make([]string, 0, len(sets))
	for k := range sets {
		keys = append(keys, k)
	}
	sort.Sort(sort.Reverse(sort.StringSlice(keys)))
	for _, k := range keys[keepSets:] {
		for _, path := range sets[k] {
			_ = os.Remove(path)
		}
	}
}

func verifyZipEntry(f *zip.File, want FileEntry) error {
	sum, size, err := hashZipFile(f)
	if err != nil {
		return fmt.Errorf("hash bundle entry %s: %w", f.Name, err)
	}
	if !strings.EqualFold(sum, want.SHA256) {
		return fmt.Errorf("bundle entry %s checksum mismatch", f.Name)
	}
	if want.Size > 0 && size != want.Size {
		return fmt.Errorf("bundle entry %s size mismatch: got %d, want %d", f.Name, size, want.Size)
	}
	return nil
}

func hashZipFile(f *zip.File) (string, int64, error) {
	rc, err := f.Open()
	if err != nil {
		return "", 0, err
	}
	defer rc.Close()
	h := sha256.New()
	n, err := io.Copy(h, rc)
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(h.Sum(nil)), n, nil
}

func readZipFile(f *zip.File) ([]byte, error) {
	rc, err := f.Open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	return io.ReadAll(rc)
}

func extractZipFile(f *zip.File, dst string) error {
	rc, err := f.Open()
	if err != nil {
		return err
	}
	defer rc.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, rc); err != nil {
		_ = out.Close()
		_ = os.Remove(dst)
		return err
	}
	if err := out.Sync(); err != nil {
		_ = out.Close()
		_ = os.Remove(dst)
		return err
	}
	if err := out.Close(); err != nil {
		_ = os.Remove(dst)
		return err
	}
	return nil
}

func copyFile(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		_ = os.Remove(dst)
		return err
	}
	if err := out.Sync(); err != nil {
		_ = out.Close()
		_ = os.Remove(dst)
		return err
	}
	if err := out.Close(); err != nil {
		_ = os.Remove(dst)
		return err
	}
	return nil
}

func writeFileSynced(path string, data []byte, mode os.FileMode) error {
	out, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	if _, err := out.Write(data); err != nil {
		_ = out.Close()
		_ = os.Remove(path)
		return err
	}
	if err := out.Sync(); err != nil {
		_ = out.Close()
		_ = os.Remove(path)
		return err
	}
	return out.Close()
}
