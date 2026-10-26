// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package pluginhost

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// ==================== 测试用二进制样本 ====================
//
// validatePluginBinary 显式接收 goos/goarch，因此下面这些「像某个平台」的
// 字节串足以驱动全部分支 —— 不需要真的交叉编译，也不受测试机自身 GOOS 影响。
// 这正是该校验函数不读 runtime 的原因（见 install.go 的说明）。

// fakePE 以 MZ 开头的伪 PE。buildinfo 解析不了 → 走「放行但告警」分支。
var fakePE = append([]byte{'M', 'Z'}, bytes.Repeat([]byte{0}, 512)...)

// fakeELF 以 \x7fELF 开头的伪 ELF。同样没有 buildinfo。
var fakeELF = append([]byte{0x7f, 'E', 'L', 'F'}, bytes.Repeat([]byte{0}, 512)...)

// fakeMachO64 64 位小端 Mach-O 魔数。
var fakeMachO64 = append([]byte{0xcf, 0xfa, 0xed, 0xfe}, bytes.Repeat([]byte{0}, 512)...)

// fakeSelfImage 返回一个按**本机** GOOS 能通过容器格式校验的伪二进制。
//
// InstallPlugin 按 runtime.GOOS 校验，固定用 fakePE 在 Linux CI 上会被判成
// 「windows/PE 传到 linux」而拒绝。落盘类用例要验的是写入路径，不是跨平台校验
// （后者由 TestValidatePluginBinary / TestInstallPluginRejectsWithoutWriting 覆盖）。
func fakeSelfImage() []byte {
	switch runtime.GOOS {
	case "windows":
		return fakePE
	case "darwin":
		return fakeMachO64
	default:
		return fakeELF
	}
}

// realSelf 本测试二进制自身：一个货真价实的 Go 可执行文件，
// buildinfo 可解析，且其 GOOS/GOARCH 必然等于 runtime 的值。
func realSelf(t *testing.T) []byte {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}
	data, err := os.ReadFile(self)
	if err != nil {
		t.Fatalf("读取本测试二进制: %v", err)
	}
	return data
}

// ==================== 文件名校验 ====================

func TestValidatePluginFileName(t *testing.T) {
	const glob = "iot-driver-*"

	cases := []struct {
		name    string
		goos    string
		file    string
		wantErr string // 期望错误里包含的片段；"" 表示应当通过
	}{
		{name: "windows 正常", goos: "windows", file: "iot-driver-modbus.exe"},
		{name: "linux 正常", goos: "linux", file: "iot-driver-modbus"},
		{name: "darwin 正常", goos: "darwin", file: "iot-driver-abb"},

		{name: "windows 缺 .exe", goos: "windows", file: "iot-driver-modbus", wantErr: "必须以 .exe 结尾"},
		{name: "linux 多了 .exe", goos: "linux", file: "iot-driver-modbus.exe", wantErr: "不应以 .exe 结尾"},
		{name: "darwin 多了 .exe", goos: "darwin", file: "iot-driver-abb.exe", wantErr: "不应以 .exe 结尾"},

		{name: "不匹配 glob", goos: "windows", file: "myplugin.exe", wantErr: "不匹配插件匹配规则"},
		{name: "只差前缀", goos: "linux", file: "modbus", wantErr: "不匹配插件匹配规则"},

		{name: "空文件名", goos: "linux", file: "", wantErr: "文件名无效"},
		{name: "单点", goos: "linux", file: ".", wantErr: "文件名无效"},
		{name: "双点", goos: "linux", file: "..", wantErr: "文件名无效"},

		{name: "正斜杠穿越", goos: "linux", file: "../iot-driver-x", wantErr: "路径分隔符"},
		{name: "反斜杠穿越", goos: "windows", file: `..\iot-driver-x.exe`, wantErr: "路径分隔符"},
		{name: "子目录", goos: "linux", file: "sub/iot-driver-x", wantErr: "路径分隔符"},
		{name: "绝对路径", goos: "linux", file: "/tmp/iot-driver-x", wantErr: "路径分隔符"},

		// 冒号：Windows 盘符与 NTFS 备用数据流都靠它。
		{name: "冒号", goos: "windows", file: "iot-driver-x:evil.exe", wantErr: "冒号"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validatePluginFileName(tc.goos, glob, tc.file)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("期望通过，实际报错: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("期望报错（含 %q），实际通过", tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("错误信息 %q 未包含 %q", err.Error(), tc.wantErr)
			}
		})
	}
}

// 自定义 glob 时，命名规则要跟着变（glob 是配置项，不是硬编码）。
func TestValidatePluginFileNameCustomGlob(t *testing.T) {
	if err := validatePluginFileName("linux", "iot-plugin-*", "iot-plugin-foo"); err != nil {
		t.Fatalf("自定义 glob 下应通过: %v", err)
	}
	if err := validatePluginFileName("linux", "iot-driver-*", "iot-plugin-foo"); err == nil {
		t.Fatal("默认 glob 下 iot-plugin-foo 应被拒绝")
	}
}

// ==================== 容器格式校验 ====================

func TestValidatePluginImage(t *testing.T) {
	cases := []struct {
		name    string
		goos    string
		data    []byte
		wantErr string
	}{
		{name: "PE 传 windows", goos: "windows", data: fakePE},
		{name: "ELF 传 linux", goos: "linux", data: fakeELF},
		{name: "Mach-O 传 darwin", goos: "darwin", data: fakeMachO64},

		// 本需求的核心场景：跨平台误传必须被拦下，并告诉用户实际格式。
		{name: "ELF 传 windows", goos: "windows", data: fakeELF, wantErr: "linux/ELF"},
		{name: "PE 传 linux", goos: "linux", data: fakePE, wantErr: "windows/PE"},
		{name: "ELF 传 darwin", goos: "darwin", data: fakeELF, wantErr: "linux/ELF"},
		{name: "Mach-O 传 windows", goos: "windows", data: fakeMachO64, wantErr: "darwin/Mach-O"},

		{name: "根本不是可执行文件", goos: "windows", data: []byte("hello, world! this is not a binary"), wantErr: "不是可执行文件"},
		{name: "zip 当插件传", goos: "linux", data: []byte("PK\x03\x04rest"), wantErr: "不是可执行文件"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validatePluginImage(tc.goos, "amd64", tc.data)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("期望通过，实际报错: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("期望报错（含 %q），实际通过", tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("错误信息 %q 未包含 %q", err.Error(), tc.wantErr)
			}
		})
	}
}

// ==================== 构建信息校验 ====================

// buildinfo 可解析时以它为准：容器格式对、架构不对（amd64 的 ELF 传到 arm64）
// 这种情况魔数看不出来，只有构建信息能发现。
//
// 这条用例与本机平台绑定（用 runtime.GOOS/GOARCH 对照测试二进制自身），
// 但两个方向都验到，因此在任何平台上都成立。
func TestValidateBuildTargetSelf(t *testing.T) {
	self := realSelf(t)

	// 一致 → 通过。若这里失败，说明 buildinfo 校验会误杀正常插件。
	if err := validateBuildTarget(runtime.GOOS, runtime.GOARCH, "self", self); err != nil {
		t.Fatalf("自测二进制应被判定为与自身平台一致，实际报错: %v", err)
	}

	// 架构不符 → 拒绝，且错误信息点明是哪一侧不符。
	wrongArch := "mips64"
	if runtime.GOARCH == wrongArch {
		wrongArch = "riscv64"
	}
	err := validateBuildTarget(runtime.GOOS, wrongArch, "self", self)
	if err == nil {
		t.Fatal("架构不符时应被拒绝，实际通过")
	}
	if !strings.Contains(err.Error(), wrongArch) {
		t.Fatalf("错误信息应点明本机架构 %s，实际: %v", wrongArch, err)
	}

	// GOOS 不符 → 拒绝。
	wrongOS := "plan9"
	if runtime.GOOS == wrongOS {
		wrongOS = "aix"
	}
	if err := validateBuildTarget(wrongOS, runtime.GOARCH, "self", self); err == nil {
		t.Fatal("操作系统不符时应被拒绝，实际通过")
	}
}

// 没有 buildinfo（非 Go 二进制 / UPX 加壳破坏了它）时不应误杀：
// 容器格式已经过硬校验，读不到元信息只告警。
func TestValidateBuildTargetWithoutBuildInfo(t *testing.T) {
	for name, data := range map[string][]byte{"PE": fakePE, "ELF": fakeELF} {
		t.Run(name, func(t *testing.T) {
			if err := validateBuildTarget("windows", "amd64", "x", data); err != nil {
				t.Fatalf("无构建信息时应放行，实际报错: %v", err)
			}
		})
	}
}

// ==================== 整体校验 ====================

func TestValidatePluginBinary(t *testing.T) {
	cases := []struct {
		name    string
		goos    string
		goarch  string
		file    string
		data    []byte
		wantErr string
	}{
		{name: "windows 正常", goos: "windows", goarch: "amd64", file: "iot-driver-x.exe", data: fakePE},
		{name: "linux 正常", goos: "linux", goarch: "arm64", file: "iot-driver-x", data: fakeELF},

		{name: "空文件", goos: "windows", goarch: "amd64", file: "iot-driver-x.exe", data: nil, wantErr: "为空"},
		{name: "过小", goos: "windows", goarch: "amd64", file: "iot-driver-x.exe", data: []byte("MZ"), wantErr: "过小"},
		{name: "跨平台", goos: "windows", goarch: "amd64", file: "iot-driver-x.exe", data: fakeELF, wantErr: "linux/ELF"},
		{name: "文件名不合规", goos: "windows", goarch: "amd64", file: "x.exe", data: fakePE, wantErr: "不匹配插件匹配规则"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validatePluginBinary(tc.goos, tc.goarch, "iot-driver-*", tc.file, tc.data)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("期望通过，实际报错: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("期望报错（含 %q），实际通过", tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("错误信息 %q 未包含 %q", err.Error(), tc.wantErr)
			}
		})
	}
}

// 超限文件要被拦在写入之前，而不是把内存与磁盘打满。
func TestValidatePluginBinaryOversize(t *testing.T) {
	big := make([]byte, MaxPluginFileSize+1)
	copy(big, fakePE)
	err := validatePluginBinary("windows", "amd64", "iot-driver-*", "iot-driver-x.exe", big)
	if err == nil || !strings.Contains(err.Error(), "超过单文件上限") {
		t.Fatalf("超限文件应被拒绝，实际: %v", err)
	}
}

// ==================== 落盘 ====================

// TestInstallPluginWritesFile 覆盖完整落盘路径：目录不存在时创建、
// 内容写入、权限位可执行。
func TestInstallPluginWritesFile(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "not-yet-created", "plugins")
	m := testManagerAt(t, dir)

	data := fakeSelfImage()
	name := "iot-driver-modbus" + pluginExt()

	if err := m.InstallPlugin(name, data); err != nil {
		t.Fatalf("InstallPlugin: %v", err)
	}

	got, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		t.Fatalf("读取已落盘的插件: %v", err)
	}
	if !bytes.Equal(got, data) {
		t.Fatal("落盘内容与上传内容不一致")
	}

	info, err := os.Stat(filepath.Join(dir, name))
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	// Unix 上 CreateTemp 建出的是 0600；不提权插件就不可执行。
	// Windows 上权限位无意义（一律读作 0666），故只在非 Windows 断言。
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o100 == 0 {
		t.Fatalf("插件文件缺少执行权限: %v", info.Mode().Perm())
	}
}

// 目录里不该留下临时文件或让位文件。
func TestInstallPluginLeavesNoDebris(t *testing.T) {
	dir := t.TempDir()
	m := testManagerAt(t, dir)
	name := "iot-driver-modbus" + pluginExt()
	data := fakeSelfImage()

	if err := m.InstallPlugin(name, data); err != nil {
		t.Fatalf("首次上传: %v", err)
	}
	// 覆盖同一个文件：走「旧文件让位」路径。
	if err := m.InstallPlugin(name, append(append([]byte{}, data...), 'X')); err != nil {
		t.Fatalf("覆盖上传: %v", err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) != 1 || entries[0].Name() != name {
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Fatalf("插件目录应只剩 %s，实际: %v", name, names)
	}
}

// 重复上传同一份内容应短路，不产生任何写入。
func TestInstallPluginUnchangedSkipsWrite(t *testing.T) {
	dir := t.TempDir()
	m := testManagerAt(t, dir)
	name := "iot-driver-modbus" + pluginExt()
	data := fakeSelfImage()

	if err := m.InstallPlugin(name, data); err != nil {
		t.Fatalf("首次上传: %v", err)
	}
	target := filepath.Join(dir, name)
	before, err := os.Stat(target)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}

	if err := m.InstallPlugin(name, data); !errors.Is(err, ErrPluginUnchanged) {
		t.Fatalf("重复上传应返回 ErrPluginUnchanged，实际: %v", err)
	}

	after, err := os.Stat(target)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if !after.ModTime().Equal(before.ModTime()) {
		t.Fatal("内容未变却改动了文件（mtime 变化会触发一轮无谓的热升级与设备重连）")
	}
}

// 总开关关闭时上传直接被拒：写了也不会加载，必须明确报错。
func TestInstallPluginRejectsWhenDisabled(t *testing.T) {
	m := testManagerAt(t, t.TempDir())
	m.cfg.Enabled = false

	err := m.InstallPlugin("iot-driver-modbus"+pluginExt(), fakePE)
	if !errors.Is(err, ErrPluginDisabled) {
		t.Fatalf("期望 ErrPluginDisabled，实际: %v", err)
	}
}

// 校验失败的文件不得落盘 —— 不能留下一个会被扫描器反复拉起的坏文件。
func TestInstallPluginRejectsWithoutWriting(t *testing.T) {
	dir := t.TempDir()
	m := testManagerAt(t, dir)

	// 与测试机平台不符的二进制：魔数对不上当前 GOOS。
	var foreign []byte
	var foreignLabel string
	if runtime.GOOS == "windows" {
		foreign, foreignLabel = fakeELF, "linux/ELF"
	} else {
		foreign, foreignLabel = fakePE, "windows/PE"
	}

	err := m.InstallPlugin("iot-driver-modbus"+pluginExt(), foreign)
	if err == nil {
		t.Fatal("跨平台二进制应被拒绝，实际通过")
	}
	if !strings.Contains(err.Error(), foreignLabel) {
		t.Fatalf("错误信息应点明实际格式 %s，实际: %v", foreignLabel, err)
	}

	entries, readErr := os.ReadDir(dir)
	if readErr != nil {
		t.Fatalf("ReadDir: %v", readErr)
	}
	if len(entries) != 0 {
		t.Fatalf("被拒的文件不应落盘，目录中却有 %d 个文件", len(entries))
	}
}

// ==================== 工具 ====================

// pluginExt 当前平台期望的插件扩展名。
func pluginExt() string {
	if runtime.GOOS == "windows" {
		return ".exe"
	}
	return ""
}

// testManagerAt 造一个把插件目录指向 dir 的 Manager（不启动扫描循环）。
func testManagerAt(t *testing.T, dir string) *Manager {
	t.Helper()
	m := &Manager{
		plugins: make(map[string]*loadedPlugin),
		failed:  make(map[string]fileFingerprint),
		stopCh:  make(chan struct{}),
		doneCh:  make(chan struct{}),
	}
	m.cfg.Enabled = true
	m.cfg.Glob = defaultPluginGlob
	// 绝对路径原样返回，不依赖 os.Executable()。
	m.cfg.Dir = dir
	return m
}
