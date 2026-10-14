// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package configFile

import (
	"reflect"
	"strings"
	"testing"
)

// TestConfigTagsMatchFieldNames 遍历整棵配置结构树，断言每个字段都写了
// mapstructure 标签，且标签与字段名大小写不敏感地一致。
//
// 存在的理由很具体：viper 的 Unmarshal 以 mapstructure 作 TagName，标签写错名字
// （历史上这里全写成了 destructure）或与字段名对不上时**不会报错** —— 字段静默
// 保持零值，症状是「端口跑到默认值」「令牌有效期变 0」这类只能靠翻代码回溯的问题。
// 本文件此前没有任何测试，这条断言补上了这个盲区。
//
// 注意 EqualFold 而非全等：SQLite→sqlite、MaxSizeMB→maxSizeMB 这类缩写字段
// 本来就大小写不同，规则是「标签 = 字段名的首字母小写形式」。
func TestConfigTagsMatchFieldNames(t *testing.T) {
	seen := map[reflect.Type]bool{}

	var walk func(typ reflect.Type, path string)
	walk = func(typ reflect.Type, path string) {
		if seen[typ] {
			return
		}
		seen[typ] = true

		for i := 0; i < typ.NumField(); i++ {
			f := typ.Field(i)
			fieldPath := path + "." + f.Name

			tag, ok := f.Tag.Lookup("mapstructure")
			if !ok || tag == "" {
				t.Errorf("%s 缺少 mapstructure 标签：viper 会回退到「字段名不区分大小写」匹配，"+
					"字段一改名就静默失配（配置被忽略、退回零值，启动不报错）", fieldPath)
				continue
			}
			if !strings.EqualFold(tag, f.Name) {
				t.Errorf("%s 的标签是 %q，与字段名不一致 —— "+
					"请确认它与 default.yaml 里的键是同一个（两者对不上时该字段会被静默忽略）",
					fieldPath, tag)
			}

			if f.Type.Kind() == reflect.Struct {
				walk(f.Type, fieldPath)
			}
		}
	}

	walk(reflect.TypeOf(Config{}), "Config")
}
