// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package pluginhost

import (
	"bytes"
	"crypto/sha256"
	"debug/buildinfo"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"iot-gateway/logger"
)

// sha256Hex 计算内容的 SHA-256 十六进制摘要。
//
// 与 hashFile 同源：加载/热升级时记下的插件 SHA 也是这个算法，
// 上传前的「内容未变」判断要能与之直接比较。
func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

const (
	// MaxPluginFileSize 单个插件文件大小上限。发行包里的插件在 5~8MiB，
	// 留足余量即可；上限的意义是挡住「误传了一个镜像/压缩包」把内存与磁盘打满。
	// 导出是为了让上传接口能在**读取之前**按它卡住请求体。
	MaxPluginFileSize = 256 << 20

	// tempFilePrefix 落盘时的临时文件名前缀。
	//
	// **必须带前导点**：扫描用的 glob（默认 iot-driver-*）会匹配任何
	// "iot-driver-" 开头的名字，临时文件一旦匹配上就会在写一半时被扫描器
	// 当成插件拉起。前导点让默认 glob 匹配不到（且 Unix 下天然隐藏）。
	tempFilePrefix = ".plugin-upload-"

	// asideSuffix 替换运行中插件时旧文件的让位后缀，拼在**前导点之后**：
	// 最终形如 .iot-driver-abb.exe.old。同样是为了不匹配扫描 glob ——
	// 直接拼 ".old" 会得到 iot-driver-abb.exe.old，它**匹配** iot-driver-*，
	// 于是每次上传都会在插件目录里留下一个会被反复拉起并握手超时的垃圾文件。
	asideSuffix = ".old"

	// pluginFileMode 落盘后的权限位。Unix 上 os.CreateTemp 建出的是 0600，
	// 不显式提权的话插件不可执行、加载必然失败。Windows 上该权限位无意义。
	pluginFileMode = 0o755
)

// ErrPluginDisabled 插件总开关关闭时上传被拒。
//
// 写入目录里也不会被加载（见 Manager.Start 的 plugin.enabled 分支），
// 因此宁可明确报错，也不要让用户以为传上去了就生效。
var ErrPluginDisabled = errors.New("插件功能未启用（plugin.enabled=false），上传的插件不会被加载")

// ErrPluginUnchanged 目标位置已存在内容完全相同的插件，未做任何写入。
//
// 不算失败：重复上传同一版本的意图已经达成。单独区分是为了不产生一次
// 无谓的文件替换 —— 替换会改动指纹，进而触发一轮热升级与设备重连。
var ErrPluginUnchanged = errors.New("插件内容与现有文件完全相同，已跳过")

// PluginDir 返回解析后的插件目录绝对路径。
//
// 与扫描路径共用 resolvePluginDir：相对路径一律相对 **os.Executable() 所在
// 目录**，而非当前工作目录（Windows 服务由 scm 给的 cwd 是 System32）。
// 目录不存在不报错 —— 由 InstallPlugin 负责创建。
func (m *Manager) PluginDir() (string, error) {
	return resolvePluginDir(m.cfg.Dir)
}

// PluginGlob 返回插件文件名匹配规则（默认 iot-driver-*）。
func (m *Manager) PluginGlob() string {
	return m.cfg.Glob
}

// Enabled 报告插件功能总开关（plugin.enabled）。
func (m *Manager) Enabled() bool {
	return m.cfg.Enabled
}

// ScanInterval 返回目录扫描间隔。
//
// 上传接口用它告诉用户「多久之后生效」：安装本身不触发加载（见 InstallPlugin），
// 新文件要等下一轮扫描才被拉起。
func (m *Manager) ScanInterval() time.Duration {
	return m.scan
}

// InstallPlugin 校验并落盘一个插件二进制。
//
// 流程：静态校验 → 内容未变则跳过 → 目录不存在则创建 → 原子写入。
// 校验针对**本机**平台（runtime.GOOS/GOARCH）：不能运行的插件不落盘，
// 否则它会静静躺在目录里，由扫描器反复拉起、等满握手超时，最后进 failed
// 抑制表 —— 症状只有日志里几行超时，极难归因。
//
// 落盘后**不**调用 Reload()：那个接口的耗时最坏可达「握手超时 + 2×进程回收
// 宽限」，不能挂在同步 HTTP handler 上（见 Reload 的说明）。交给既有的周期
// 扫描发现即可，新插件与「被替换的已加载插件」两条路径都已由 scanOnce 覆盖。
func (m *Manager) InstallPlugin(name string, data []byte) error {
	if !m.cfg.Enabled {
		return ErrPluginDisabled
	}
	if err := validatePluginBinary(runtime.GOOS, runtime.GOARCH, m.cfg.Glob, name, data); err != nil {
		return err
	}

	dir, err := m.PluginDir()
	if err != nil {
		return err
	}

	target := filepath.Join(dir, name)
	// 内容未变就短路。必须在替换之前判断：Windows 上替换运行中的 exe 要走
	// 「改名让位」两步，为一个字节都没变的文件付这个代价（并触发一轮热升级
	// 与设备重连）没有意义。
	if sha, err := hashFile(target); err == nil {
		if sha == sha256Hex(data) {
			return ErrPluginUnchanged
		}
	}

	if err := ensurePluginDir(dir); err != nil {
		return err
	}
	return m.writePluginBinary(dir, target, data)
}

// ensurePluginDir 确保插件目录存在。
//
// 这与 checkDirSecurity 里「不自动创建」的立场**有意不同**：那条针对的是
// 启动期的静默副作用（网关不该自作主张造出一个可执行目录）；而这里是管理员
// 的一次显式上传动作，不创建反而让「点了按钮却什么都没发生」。因此创建时
// 打一条 WARN，把「这个目录是我建的」留在日志里。
func ensurePluginDir(dir string) error {
	info, err := os.Stat(dir)
	if err == nil {
		if !info.IsDir() {
			return fmt.Errorf("%s 已存在但不是目录", dir)
		}
		return nil
	}
	if !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("检查插件目录: %w", err)
	}

	logger.Warn("pluginhost: 插件目录 %s 不存在，因管理端上传插件而创建。"+
		"该目录下的可执行文件会以网关自身权限运行，请确保仅管理员可写", dir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("创建插件目录 %s: %w", dir, err)
	}
	return nil
}

// writePluginBinary 原子地把 data 写到 target。
//
// 全程持 scanMu：临时文件在插件目录内的整个生命周期内都不会有扫描发生，
// 因此扫描器绝不会观察到「写了一半的文件」——它顶多看到替换前或替换后的
// 完整文件，两者都是合法的插件二进制。
func (m *Manager) writePluginBinary(dir, target string, data []byte) error {
	var err error
	m.withScanLock(func() {
		err = func() error {
			tmp, cErr := os.CreateTemp(dir, tempFilePrefix)
			if cErr != nil {
				return fmt.Errorf("创建临时文件: %w", cErr)
			}
			tmpName := tmp.Name()
			// 成功时已经被 rename 走，这里是空操作；失败路径负责收尾。
			defer os.Remove(tmpName)

			if _, wErr := tmp.Write(data); wErr != nil {
				tmp.Close()
				return fmt.Errorf("写入临时文件: %w", wErr)
			}
			// 必须显式提权：CreateTemp 建出的是 0600，Unix 下不可执行。
			if chErr := tmp.Chmod(pluginFileMode); chErr != nil {
				tmp.Close()
				return fmt.Errorf("设置插件权限位: %w", chErr)
			}
			if clErr := tmp.Close(); clErr != nil {
				return fmt.Errorf("关闭临时文件: %w", clErr)
			}
			return replaceFile(target, tmpName)
		}()
	})
	return err
}

// replaceFile 把 src 改名到 target，目标已存在时先改名让位。
//
// Windows 上正在运行的 exe **不能**被覆盖或删除（共享冲突），但**可以改名** ——
// 这就是替换运行中插件、让热升级接管的可行路径。改名让位后，运行中的旧进程
// 仍持有旧 inode/文件句柄继续服务，直到扫描器发现新二进制并完成进程交接。
//
// 让位文件在 Windows 上删不掉是**预期内**的：刚让位的那个文件正被仍在服务的
// 旧进程占用。因此每次替换的稳定状态是「目录里留下恰好一个 .old」——
// 上一轮残留的会在开头的 os.Remove 里被清掉，不会累积。
//
// 让位文件名带前导点（.iot-driver-xxx.exe.old），因此不匹配扫描 glob，
// 不会被当成插件拉起。
func replaceFile(target, src string) error {
	aside := filepath.Join(filepath.Dir(target), "."+filepath.Base(target)+asideSuffix)

	_, err := os.Lstat(target)
	if errors.Is(err, fs.ErrNotExist) {
		// 全新插件，直接改名到位。
		if rErr := os.Rename(src, target); rErr != nil {
			return fmt.Errorf("写入插件文件: %w", rErr)
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("检查目标位置: %w", err)
	}

	// 上一轮留下的让位文件（若其进程已退出则能删掉），失败不阻塞本次替换。
	_ = os.Remove(aside)

	if rErr := os.Rename(target, aside); rErr != nil {
		return fmt.Errorf("旧插件文件无法让位（可能正被占用），请稍后重试: %w", rErr)
	}
	if rErr := os.Rename(src, target); rErr != nil {
		// 回滚：旧文件必须回到原位，否则插件目录里凭空少了一个可用插件。
		if rbErr := os.Rename(aside, target); rbErr != nil {
			return fmt.Errorf("写入插件失败（%v），且旧文件回滚失败（%v）；"+
				"请手工把 %s 改回 %s", rErr, rbErr, aside, target)
		}
		return fmt.Errorf("写入插件文件: %w", rErr)
	}

	// 成功。Windows 上这里几乎必然失败（旧进程仍占用），Unix 上会成功。
	_ = os.Remove(aside)
	return nil
}

// ==================== 校验（纯函数，便于跨平台测试） ====================

// validatePluginBinary 校验一个待落盘的插件是否适用于 (goos, goarch) 平台。
//
// goos/goarch 显式传入而不是直接读 runtime：否则本函数里非当前平台的分支
// 在开发机上永远进不去（Windows 上 runtime.GOOS 恒为 windows），跨平台校验
// 这个**核心逻辑本身**就成了测不到的代码。调用方传 runtime.GOOS/GOARCH。
func validatePluginBinary(goos, goarch, glob, name string, data []byte) error {
	if err := validatePluginFileName(goos, glob, name); err != nil {
		return err
	}
	if len(data) == 0 {
		return errors.New("插件文件为空")
	}
	// 报上限而不报实际大小：上传路径会把超限文件截断到 limit+1 再交过来，
	// 打印那个截断值会得到「256 MiB 超过上限 256 MiB」这种自相矛盾的文案。
	if int64(len(data)) > MaxPluginFileSize {
		return fmt.Errorf("插件文件超过单文件上限 %d MiB", int64(MaxPluginFileSize)>>20)
	}
	if err := validatePluginImage(goos, goarch, data); err != nil {
		return err
	}
	return validateBuildTarget(goos, goarch, name, data)
}

// validatePluginFileName 校验插件文件名。
func validatePluginFileName(goos, glob, name string) error {
	if name == "" || name == "." || name == ".." {
		return fmt.Errorf("插件文件名无效：%q", name)
	}
	// 路径穿越防护。filepath.Base 在 Unix 上不把反斜杠当分隔符，所以要显式
	// 检查两种分隔符，不能只靠 Base。
	if strings.ContainsAny(name, `/\`) || filepath.Base(name) != name {
		return fmt.Errorf("插件文件名 %q 不能包含路径分隔符", name)
	}
	if strings.Contains(name, ":") {
		// Windows 盘符（C:）与 NTFS 备用数据流（file:stream）都靠冒号。
		return fmt.Errorf("插件文件名 %q 不能包含冒号", name)
	}

	matched, err := filepath.Match(glob, name)
	if err != nil {
		return fmt.Errorf("配置项 plugin.glob=%q 非法: %w", glob, err)
	}
	if !matched {
		// 静默无效是最坏的结果：文件确实传上去了，但扫描器根本不会看它一眼。
		return fmt.Errorf("文件名 %q 不匹配插件匹配规则 %q，放入目录也不会被加载", name, glob)
	}

	isExe := strings.EqualFold(filepath.Ext(name), ".exe")
	if goos == "windows" && !isExe {
		return fmt.Errorf("Windows 插件文件名必须以 .exe 结尾（当前为 %q）", name)
	}
	if goos != "windows" && isExe {
		return fmt.Errorf("%s 平台的插件文件名不应以 .exe 结尾（当前为 %q）", goos, name)
	}
	return nil
}

// validatePluginImage 按魔数校验可执行文件容器格式与目标平台一致。
func validatePluginImage(goos, goarch string, data []byte) error {
	if len(data) < 4 {
		return errors.New("文件过小，不是有效的可执行文件")
	}

	got := detectExecutableImage(data)
	want, known := imageForOS(goos)
	if !known {
		// 目标平台不在已知表里（GOOS 为计划外取值），不做魔数校验，
		// 由下面的 buildinfo 校验兜底。
		return nil
	}
	if got.goos == want.goos {
		return nil
	}

	if got.label == "" {
		return fmt.Errorf("该文件不是可执行文件（%s 平台需要 %s），无法作为插件加载", goos, want.label)
	}
	return fmt.Errorf("该文件是 %s 格式，本网关运行于 %s/%s，无法加载（需要 %s）",
		got.label, goos, goarch, want.label)
}

// validateBuildTarget 按下述规则校验 Go 构建信息里的目标平台：
//
//   - 能解析且与目标平台不符 → **拒绝**。这是最有力的判据：容器格式可能对
//     而架构不对（如 amd64 的 ELF 传到 arm64 网关），魔数看不出来。
//   - 解析不了（非 Go 二进制、UPX 加壳破坏了 buildinfo）→ **放行**，只告警。
//     魔数已经硬校验过一轮，这里不该因为读不到元信息就误杀一个本可运行的插件。
func validateBuildTarget(goos, goarch, name string, data []byte) error {
	bi, err := buildinfo.Read(bytes.NewReader(data))
	if err != nil {
		logger.Warn("pluginhost: 插件 %s 不含可解析的 Go 构建信息（%v），"+
			"跳过目标平台元数据校验，仅依据容器格式判断", name, err)
		return nil
	}

	var biOS, biArch string
	for _, s := range bi.Settings {
		switch s.Key {
		case "GOOS":
			biOS = s.Value
		case "GOARCH":
			biArch = s.Value
		}
	}

	if biOS != "" && biOS != goos {
		return fmt.Errorf("该插件是为 %s 构建的，本网关运行于 %s/%s，无法加载", biOS, goos, goarch)
	}
	if biArch != "" && biArch != goarch {
		return fmt.Errorf("该插件是为 %s/%s 构建的，本网关运行于 %s/%s，无法加载",
			biOS, biArch, goos, goarch)
	}
	return nil
}

// executableImage 可执行文件容器格式。
type executableImage struct {
	goos  string // 该格式对应的目标操作系统；"" 表示未识别
	label string // 展示用描述，如 "linux/ELF"
}

// detectExecutableImage 按魔数识别可执行文件容器格式。
//
// 只覆盖插件实际可能出现的三种：Go 交叉编译出的 PE / ELF / Mach-O。
// 识别不出来时返回空值，调用方据此给出「不是可执行文件」的提示。
func detectExecutableImage(data []byte) executableImage {
	if image, ok := imageForOS(osWindows); ok && bytes.HasPrefix(data, imageMagic[osWindows]) {
		return image
	}
	if image, ok := imageForOS(osLinux); ok && bytes.HasPrefix(data, imageMagic[osLinux]) {
		return image
	}
	for _, magic := range machOMagics {
		if bytes.HasPrefix(data, magic) {
			return executableImage{goos: osDarwin, label: "darwin/Mach-O"}
		}
	}
	return executableImage{}
}

const (
	osWindows = "windows"
	osLinux   = "linux"
	osDarwin  = "darwin"
)

// imageMagic 各平台可执行文件的首字节。
var imageMagic = map[string][]byte{
	osWindows: {'M', 'Z'},            // PE / COFF
	osLinux:   {0x7f, 'E', 'L', 'F'}, // ELF
}

// machOMagics Mach-O 的各种字节序 / 位宽 / fat 头的魔数。
var machOMagics = [][]byte{
	{0xcf, 0xfa, 0xed, 0xfe}, // 64 位小端
	{0xfe, 0xed, 0xfa, 0xcf}, // 64 位大端
	{0xce, 0xfa, 0xed, 0xfe}, // 32 位小端
	{0xfe, 0xed, 0xfa, 0xce}, // 32 位大端
	{0xca, 0xfe, 0xba, 0xbe}, // fat（大端）
	{0xbe, 0xba, 0xfe, 0xca}, // fat（小端）
}

// imageForOS 返回某目标平台期望的容器格式。
func imageForOS(goos string) (executableImage, bool) {
	switch goos {
	case osWindows:
		return executableImage{goos: osWindows, label: "windows/PE"}, true
	case osLinux:
		return executableImage{goos: osLinux, label: "linux/ELF"}, true
	case osDarwin:
		return executableImage{goos: osDarwin, label: "darwin/Mach-O"}, true
	}
	return executableImage{}, false
}
