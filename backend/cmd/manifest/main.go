// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

// Command manifest 生成发行包根目录的 MANIFEST.json。
//
// 它是「发行配对矩阵」的机器可读版本：网关版本、目标平台、SDK 版本、线协议
// APIVersion、官方驱动版本，以及 plugins/ 里每个二进制的 sha256。维护者不再需要
// 从 tag、go.mod 和日志里手工拼一份对照表，现场也可以直接读这个文件核对。
//
// 用法（由 release.sh / release.bat 调用，不面向最终用户）：
//
//	manifest -out release/MANIFEST.json -gateway v0.2.0 \
//	    -drivers-file drivers.version -os windows -arch amd64 -plugins release/plugins
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"sort"
	"strings"
	"time"

	"github.com/Ellisata/iot-gateway-sdk/driversdk"
	"github.com/Ellisata/iot-gateway-sdk/pluginwire"
)

// Manifest 发行包根目录 MANIFEST.json 的结构。
//
// 字段顺序即 JSON 输出顺序，按「先网关、后协议」排列；新增字段请追加在末尾，
// 不要重排 —— 这个文件会被发版检查脚本按字段读取。
type Manifest struct {
	Gateway    string   `json:"gateway"`
	OS         string   `json:"os"`
	Arch       string   `json:"arch"`
	BuildTime  string   `json:"buildTime"`
	SDK        string   `json:"sdk"`
	APIVersion int      `json:"apiVersion"`
	FrameMagic string   `json:"frameMagic"`
	Drivers    string   `json:"drivers"`
	Plugins    []Plugin `json:"plugins"`
}

// Plugin 一个随包分发的插件二进制。
type Plugin struct {
	// Name 去掉平台后缀的产物名，如 iot-driver-modbus。
	Name string `json:"name"`
	// Version 官方插件的 Meta.Version，构建时由 drivers.version 注入。
	Version string `json:"version"`
	// SHA256 二进制内容的十六进制摘要，供现场核对是否被替换。
	SHA256 string `json:"sha256"`
}

func main() {
	out := flag.String("out", "", "输出文件路径（必填）")
	gateway := flag.String("gateway", "dev", "网关发行版本（如 v0.2.0）")
	driversFile := flag.String("drivers-file", "", "drivers.version 路径；留空则 drivers 为空")
	goos := flag.String("os", runtime.GOOS, "目标平台 GOOS")
	goarch := flag.String("arch", runtime.GOARCH, "目标平台 GOARCH")
	pluginsDir := flag.String("plugins", "", "插件目录（包内的 plugins/）")
	flag.Parse()

	if *out == "" {
		fatalf("-out 不能为空")
	}

	drivers, err := readVersionFile(*driversFile)
	if err != nil {
		fatalf("读取 drivers.version 失败: %v", err)
	}
	plugins, err := listPlugins(*pluginsDir, drivers)
	if err != nil {
		fatalf("扫描插件目录失败: %v", err)
	}

	m := Manifest{
		Gateway:    strings.TrimSpace(*gateway),
		OS:         *goos,
		Arch:       *goarch,
		BuildTime:  time.Now().UTC().Format(time.RFC3339),
		SDK:        sdkVersion(),
		APIVersion: driversdk.APIVersion,
		FrameMagic: frameMagicString(pluginwire.FrameMagic),
		Drivers:    drivers,
		Plugins:    plugins,
	}
	if m.Gateway == "" {
		m.Gateway = "dev"
	}

	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		fatalf("序列化 MANIFEST.json 失败: %v", err)
	}
	data = append(data, '\n')

	if dir := filepath.Dir(*out); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			fatalf("创建输出目录失败: %v", err)
		}
	}
	if err := os.WriteFile(*out, data, 0o644); err != nil {
		fatalf("写入 %s 失败: %v", *out, err)
	}
	fmt.Printf("  [OK] MANIFEST.json: gateway=%s apiVersion=%d drivers=%s plugins=%d\n",
		m.Gateway, m.APIVersion, m.Drivers, len(m.Plugins))
}

// readVersionFile 读取并 trim 版本真源；路径为空时返回空串（不是错误）。
//
// 用 trim 而不是只去行尾：drivers.version 在 Windows 上可能被检出成 CRLF，
// 带着 \r 的版本号会让 go get 报 "module ... not found"，而报错完全不指向行尾。
func readVersionFile(path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", nil
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(b)), nil
}

// listPlugins 扫描插件目录，按文件名排序返回。
//
// 只看 iot-driver-* 普通文件：目录（例如未清理的中间目录）不参与，免得把一个
// 目录当成插件算 sha256。.exe 后缀只影响文件名，不进 Name —— 同一插件在不同
// 平台的标识应当一致，平台差异由 Manifest.OS / Arch 表达。
func listPlugins(dir, version string) ([]Plugin, error) {
	plugins := make([]Plugin, 0)
	if strings.TrimSpace(dir) == "" {
		return plugins, nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasPrefix(e.Name(), "iot-driver-") {
			continue
		}
		path := filepath.Join(dir, e.Name())
		sum, err := fileSHA256(path)
		if err != nil {
			return nil, err
		}
		plugins = append(plugins, Plugin{
			Name:    strings.TrimSuffix(e.Name(), ".exe"),
			Version: version,
			SHA256:  sum,
		})
	}
	sort.Slice(plugins, func(i, j int) bool { return plugins[i].Name < plugins[j].Name })
	return plugins, nil
}

func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// sdkVersion 从构建信息里取 SDK 依赖版本。
//
// manifest 工具由 backend 模块构建，go.mod 里的 github.com/Ellisata/iot-gateway-sdk
// 一定出现在 Deps 里；取不到时返回 "unknown" 而不是空串，避免 JSON 里出现
// 容易被误读成「没有依赖」的空值。
func sdkVersion() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "unknown"
	}
	for _, dep := range info.Deps {
		if dep.Path != "github.com/Ellisata/iot-gateway-sdk" {
			continue
		}
		if dep.Replace != nil && dep.Replace.Version != "" {
			return dep.Replace.Version
		}
		if dep.Version == "" {
			return "(devel)"
		}
		return dep.Version
	}
	return "unknown"
}

// frameMagicString 把 uint32 魔数渲染成可读的 ASCII（IGP2）。
// 出现不可打印字节时退回 0x 十六进制，免得 JSON 里出现乱码。
func frameMagicString(v uint32) string {
	b := []byte{byte(v >> 24), byte(v >> 16), byte(v >> 8), byte(v)}
	for _, c := range b {
		if c < 0x20 || c > 0x7e {
			return fmt.Sprintf("0x%08X", v)
		}
	}
	return string(b)
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "manifest: "+format+"\n", args...)
	os.Exit(1)
}
