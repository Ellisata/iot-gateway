// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package mapping

import "iot-gateway/mappingdsl"

// 标量转换已下沉到 mappingdsl。保留包内小写别名，避免改动 filter/collectedAt
// 等大量调用点的可读性。
func toString(v any) string { return mappingdsl.ToString(v) }

func toInt(s string) (int64, bool) { return mappingdsl.ToInt(s) }

func toFloat64(v any) (float64, bool) { return mappingdsl.ToFloat64(v) }

var errValueMissing = mappingdsl.ErrValueMissing
