// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package mapping

import "iot-gateway/mappingdsl"

// 变换实现已下沉到 mappingdsl；这里保留类型别名与常量，保证既有配置、
// 前端映射编辑器与测试无需迁移。
type Transform = mappingdsl.Transform

const (
	OpScale        = mappingdsl.OpScale
	OpRound        = mappingdsl.OpRound
	OpTrim         = mappingdsl.OpTrim
	OpDefault      = mappingdsl.OpDefault
	OpHexDecode    = mappingdsl.OpHexDecode
	OpBase64Decode = mappingdsl.OpBase64Decode
	OpBitfield     = mappingdsl.OpBitfield
	OpRegex        = mappingdsl.OpRegex
)
