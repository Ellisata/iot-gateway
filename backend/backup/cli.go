// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package backup

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"iot-gateway/configFile"
)

// RunCLI 执行 backup / restore 子命令，返回进程退出码。
//
// 子命令在 main() 里先于 Windows 服务检测解析；cfg 可能为 nil（磁盘 yaml
// 损坏且调用方又没给 --db/--dir 时），此时只用内置默认路径。
func RunCLI(args []string, cfg *configFile.Config) int {
	if len(args) == 0 {
		printUsage(os.Stderr)
		return 2
	}
	switch args[0] {
	case "backup":
		return runBackupCLI(args[1:], cfg)
	case "restore":
		return runRestoreCLI(args[1:], cfg)
	case "-h", "--help", "help":
		printUsage(os.Stdout)
		return 0
	default:
		fmt.Fprintf(os.Stderr, "未知子命令 %q\n\n", args[0])
		printUsage(os.Stderr)
		return 2
	}
}

func runBackupCLI(args []string, cfg *configFile.Config) int {
	if len(args) == 0 {
		printBackupUsage(os.Stderr)
		return 2
	}
	switch args[0] {
	case "create":
		return runCreateCLI(args[1:], cfg)
	case "list":
		return runListCLI(args[1:], cfg)
	case "verify":
		return runVerifyCLI(args[1:])
	case "-h", "--help", "help":
		printBackupUsage(os.Stdout)
		return 0
	default:
		fmt.Fprintf(os.Stderr, "未知 backup 子命令 %q\n\n", args[0])
		printBackupUsage(os.Stderr)
		return 2
	}
}

func runCreateCLI(args []string, cfg *configFile.Config) int {
	fs := flag.NewFlagSet("backup create", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	db := fs.String("db", "", "数据库文件路径（默认取 default.yaml 的 sqlite.path）")
	dir := fs.String("dir", "", "备份目录（默认取 default.yaml 的 backup.dir）")
	scope := fs.String("scope", DefaultScope(cfg), "备份范围：full（整库）| config（仅配置）")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if s := strings.TrimSpace(*scope); s != ScopeFull && s != ScopeConfig {
		fmt.Fprintf(os.Stderr, "未知备份范围 %q（可选 %q / %q）\n", s, ScopeFull, ScopeConfig)
		return 2
	}

	eff := EffectiveConfig(cfg)
	opts := BuildOptionsFromConfig(cfg, *db, *dir, *scope, "cli")
	opts.Now = time.Now()
	b, err := CreateBundle(context.Background(), opts)
	if err != nil {
		fmt.Fprintf(os.Stderr, "备份失败: %v\n", err)
		return 1
	}
	fmt.Printf("备份完成: %s\n", b.Path)
	fmt.Printf("范围: %s  大小: %s  schema: %s  网关: %s\n",
		b.Manifest.Scope, humanSize(fileSize(b.Path)), b.Manifest.SchemaVersion, displayGateway(b.Manifest.Gateway))

	deleted, err := ApplyRetention(BackupDir(cfg, *dir), eff.Keep, eff.MaxTotalMB)
	if err != nil {
		fmt.Fprintf(os.Stderr, "警告: 清理旧备份失败: %v\n", err)
	} else if len(deleted) > 0 {
		fmt.Printf("已清理 %d 个旧备份\n", len(deleted))
	}
	return 0
}

func runListCLI(args []string, cfg *configFile.Config) int {
	fs := flag.NewFlagSet("backup list", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	dir := fs.String("dir", "", "备份目录（默认取 default.yaml 的 backup.dir）")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	backupDir := BackupDir(cfg, *dir)
	bundles, err := ListBundles(backupDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "读取备份目录失败: %v\n", err)
		return 1
	}
	if len(bundles) == 0 {
		fmt.Printf("没有找到备份包（目录: %s）\n", backupDir)
		return 0
	}
	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "名称\t范围\t大小\t创建时间")
	for _, b := range bundles {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n",
			b.Name, b.Scope, humanSize(b.Size), b.ModTime.Format("2006-01-02 15:04:05"))
	}
	_ = tw.Flush()
	return 0
}

func runVerifyCLI(args []string) int {
	fs := flag.NewFlagSet("backup verify", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	bundle := fs.String("bundle", "", "要校验的 .igbak 文件")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if strings.TrimSpace(*bundle) == "" {
		fmt.Fprintln(os.Stderr, "缺少 --bundle")
		return 2
	}
	vb, err := VerifyBundle(*bundle)
	if err != nil {
		fmt.Fprintf(os.Stderr, "校验失败: %v\n", err)
		return 1
	}
	defer vb.Cleanup()

	m := vb.Manifest
	fmt.Println("校验通过")
	fmt.Printf("  包:        %s\n", vb.Path)
	fmt.Printf("  创建时间:  %s（%s）\n", m.CreatedAt, m.CreatedBy)
	fmt.Printf("  范围:      %s\n", m.Scope)
	fmt.Printf("  网关:      %s\n", displayGateway(m.Gateway))
	fmt.Printf("  schema:    %s\n", m.SchemaVersion)
	fmt.Printf("  数据库:    sha256=%s size=%s\n", m.DB.SHA256, humanSize(m.DB.Size))
	fmt.Printf("  插件:      %d 个%s\n", len(m.Plugins), driverVersionSuffix(m.DriverVersion))
	return 0
}

func runRestoreCLI(args []string, cfg *configFile.Config) int {
	fs := flag.NewFlagSet("restore", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	bundle := fs.String("bundle", "", "要恢复的 .igbak 文件")
	pending := fs.Bool("pending", false, "应用 <data-dir>/restore-pending 中暂存的恢复任务")
	db := fs.String("db", "", "目标数据库路径（默认取 default.yaml 的 sqlite.path）")
	identity := fs.String("identity", identityTakeover, "网关身份模式：takeover（接管）| clone（克隆）")
	restoreConfig := fs.Bool("restore-config", false, "同时恢复包内的 default.yaml / <APP_ENV>.yaml")
	yes := fs.Bool("yes", false, "跳过交互确认（脚本/远程执行时使用）")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	if *pending {
		return runPendingRestoreCLI(cfg, *yes)
	}
	if strings.TrimSpace(*bundle) == "" {
		fmt.Fprintln(os.Stderr, "缺少 --bundle（或使用 --pending）")
		return 2
	}
	targetDB := DBPath(cfg, *db)
	if !*yes {
		fmt.Printf("即将用备份覆盖数据库 %s\n", targetDB)
		fmt.Printf("备份包: %s\n", *bundle)
		fmt.Print("此操作必须先停止网关服务。输入 yes 继续: ")
		reader := bufio.NewReader(os.Stdin)
		line, _ := reader.ReadString('\n')
		if strings.TrimSpace(line) != "yes" {
			fmt.Fprintln(os.Stderr, "已取消")
			return 1
		}
	}

	opts := RestoreOptions{
		BundlePath:     *bundle,
		DBPath:         targetDB,
		Identity:       *identity,
		RestoreConfig:  *restoreConfig,
		CurrentVersion: configFile.ResolveGatewayVersion(cfg),
		Now:            time.Now(),
	}
	result, err := Restore(context.Background(), opts)
	if err != nil {
		fmt.Fprintf(os.Stderr, "恢复失败: %v\n", err)
		return 1
	}

	fmt.Printf("恢复完成: %s（范围 %s）\n", targetDB, result.Manifest.Scope)
	if result.Identity == identityClone {
		fmt.Printf("克隆身份: id=%s name=%s\n", result.NewID, result.NewName)
	}
	if len(result.TablesRestored) > 0 {
		fmt.Printf("已恢复配置表 %d 张（%s）\n", len(result.TablesRestored), strings.Join(result.TablesRestored, ", "))
	}
	if len(result.PreRestore) > 0 {
		fmt.Println("恢复前的旧库已保留:")
		for _, p := range result.PreRestore {
			fmt.Printf("  %s\n", p)
		}
	}
	if len(result.ConfigRestored) > 0 {
		fmt.Println("已恢复外部配置:")
		for _, p := range result.ConfigRestored {
			fmt.Printf("  %s\n", p)
		}
	}
	for _, w := range result.Warnings {
		fmt.Printf("注意: %s\n", w)
	}
	fmt.Println("请启动网关服务，并检查设备数、插件加载与推送通道状态。")
	return 0
}

func runPendingRestoreCLI(cfg *configFile.Config, yes bool) int {
	info, err := ReadPending(DataDir(cfg, ""))
	if err != nil {
		fmt.Fprintf(os.Stderr, "读取待恢复任务失败: %v\n", err)
		return 1
	}
	if info == nil {
		fmt.Println("没有待恢复任务")
		return 0
	}
	if !yes {
		fmt.Printf("即将应用待恢复包 %s（范围 %s，身份 %s）\n", info.BundlePath, info.Scope, info.Identity)
		fmt.Print("此操作必须先停止网关服务。输入 yes 继续: ")
		reader := bufio.NewReader(os.Stdin)
		line, _ := reader.ReadString('\n')
		if strings.TrimSpace(line) != "yes" {
			fmt.Fprintln(os.Stderr, "已取消")
			return 1
		}
	}
	lock, err := AcquireLock(DataDir(cfg, ""))
	if err != nil {
		fmt.Fprintf(os.Stderr, "网关可能仍在运行，拒绝恢复（请先停止服务）: %v\n", err)
		return 1
	}
	defer lock.Release()
	if err := ApplyPendingRestore(context.Background(), cfg, configFile.ResolveGatewayVersion(cfg)); err != nil {
		fmt.Fprintf(os.Stderr, "恢复失败: %v\n", err)
		return 1
	}
	fmt.Println("待恢复任务已应用，请启动网关服务。")
	return 0
}

func printUsage(w io.Writer) {
	fmt.Fprintln(w, `用法:
  iot-gateway backup create [--scope full|config] [--db <path>] [--dir <dir>]
  iot-gateway backup list   [--dir <dir>]
  iot-gateway backup verify --bundle <file>
  iot-gateway restore       --bundle <file> [--db <path>] [--identity takeover|clone]
                            [--restore-config] [--yes]
  iot-gateway restore       --pending [--yes]

备份在网关运行时也可执行（快照走独立连接）；恢复必须先停止服务。
config 包只覆盖配置表，保留报警历史、控制日志与断网缓存。`)
}

func printBackupUsage(w io.Writer) {
	fmt.Fprintln(w, `用法:
  iot-gateway backup create [--scope full|config] [--db <path>] [--dir <dir>]
  iot-gateway backup list   [--dir <dir>]
  iot-gateway backup verify --bundle <file>`)
}

func displayGateway(g GatewayInfo) string {
	name := strings.TrimSpace(g.Name)
	if name == "" {
		name = "（未命名）"
	}
	id := g.ID
	if len(id) > 8 {
		id = id[:8]
	}
	if id == "" {
		id = "local"
	}
	return fmt.Sprintf("%s / %s / %s", name, id, g.Version)
}

func driverVersionSuffix(v string) string {
	if strings.TrimSpace(v) == "" {
		return ""
	}
	return "（drivers.version=" + v + "）"
}

func humanSize(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGTPE"[exp])
}
