// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package delivery

import "github.com/Ellisata/iot-gateway/tsdb"

func orNop(l tsdb.Logger) tsdb.Logger {
	if l == nil {
		return tsdb.NopLogger()
	}
	return l
}
