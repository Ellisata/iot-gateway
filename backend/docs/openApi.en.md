# IoT Gateway Public API Documentation

> This document is intended for **third-party system developers**, describing how to query device data via the gateway public API (`/openApi/*`).
> All endpoints are read-only queries; no write operations are provided.

## Version History

| Version | Date | Changes | Breaking Changes |
| ------- | ---- | ------- | ---------------- |
| v1.0 | 2026-08-27 | Initial release: paginated device query, paginated device address query | - |
| v1.1 | 2026-09-07 | Added: batch device name query (4.2), batch address label query (4.3); subsequent section numbers shifted accordingly | - |

## Compatibility Commitment

- The **names, types and semantics of published fields will never change or be removed**;
- Only incremental extensions: new fields, new optional request parameters, new error codes;
- New fields do not affect existing integrations (integrators should ignore unknown fields rather than fail);
- Published error codes are append-only.

## 1. Getting Started

### 1.1 Obtaining an Access Key

Please contact the gateway administrator. After the administrator creates a key on the **Open API Keys** page of the admin console, the **AppKey** will be provided to the integrator.

### 1.2 Security Notes

- ⚠️ The key is transmitted **in plain text** via the `X-Api-Key` HTTP header; these endpoints are for **intranet/dedicated-line environments only** — do not expose them over the public internet;
- Keep the key safe; do not commit it to code repositories or embed it in frontend code;
- If the key is compromised, notify the administrator to regenerate it immediately.

## 2. Authentication

All public API endpoints require the key in the request header:

```
X-Api-Key: <AppKey assigned by the administrator>
```

| Scenario | Result |
| -------- | ------ |
| `X-Api-Key` header missing | HTTP **401**, `code=70003` (key is empty) |
| Invalid/non-existent key | HTTP **401**, `code=70004` (invalid key) |

> Note: For security reasons, "key does not exist" and "key is wrong" both return 70004 without distinction.
> This is **intentional** to prevent attackers from probing valid keys — do not attempt repeated guessing.

## 3. Common Conventions

### 3.1 Base URL

```
http://<gateway-IP>:9081
```

> The port depends on the actual deployment configuration; default `9081`.

### 3.2 Unified Response Envelope

The HTTP status code of all business responses is `200`; determine the result from the response body:

```json
{
  "code": "0",
  "msg": "success",
  "isSuccess": true,
  "data": { ... },
  "serverTime": "2026-08-27 10:30:00"
}
```

| Field | Type | Description |
| ----- | ---- | ----------- |
| code | string | Business code, `"0"` means success |
| msg | string | Description (follows the gateway language configuration, usually Chinese) |
| isSuccess | boolean | Whether the call succeeded, **recommended as the primary check** |
| data | object | Business data, `null` on failure |
| serverTime | string | Server time, format `yyyy-MM-dd HH:mm:ss` |

> ⚠️ Important: On business failure (e.g. parameter error, resource not found) the HTTP status code is still `200`,
> **the only exception being authentication failure which returns `401`**. Do not judge the business result by HTTP status code alone.

### 3.3 Pagination

Paginated endpoints follow a unified request/response structure:

- Request parameters: `page` (page number, starting from 1, required), `size` (page size, 1~100, required);
- Response `data` structure:

```json
{
  "page": 1,
  "size": 20,
  "total": 135,
  "records": [ ... ]
}
```

### 3.4 Time Format

All time fields use the `yyyy-MM-dd HH:mm:ss` format (server local time).
Note that an empty string `""` for the device's `lastSuccessTime` means **no successful collection record yet**.

### 3.5 Idempotency and Call Frequency

- All public API endpoints are idempotent read-only queries;
- Choose `size` as needed and paginate sequentially for full traversal; avoid high-frequency polling — for most scenarios polling every 5~30 seconds is sufficient.

## 4. API Reference

### 4.1 Paginated Device Query

Lists devices connected to the gateway along with their online status.

```
GET /openApi/device/page
```

**Request Parameters (Query)**

| Parameter | Type | Required | Description |
| --------- | ---- | -------- | ----------- |
| page | int | Yes | Page number, ≥1 |
| size | int | Yes | Page size, 1~100 |
| name | string | No | Device name, fuzzy match |

**Response Fields (elements in records)**

| Field | Type | Description |
| ----- | ---- | ----------- |
| id | string | Device ID (used when querying addresses) |
| name | string | Device name |
| protocolName | string | Protocol name (e.g. ModbusTcp, SiemensS7) |
| description | string | Device description |
| status | int | Config status: `1`=enabled, `0`=disabled |
| online | boolean | Whether currently online (determined by the collection engine; a device is considered offline after prolonged unsuccessful collection) |
| lastSuccessTime | string | Last successful collection time; `""` means no successful record yet |
| createdAt | string | Creation time |
| updatedAt | string | Update time |

> Note: Public API responses do not include sensitive fields such as PLC connection configuration (protocolJson, internal IDs, etc.); this trimming is intentional.

**Example**

```bash
curl -H "X-Api-Key: your-app-key" \
     "http://192.168.1.100:9081/openApi/device/page?page=1&size=20&name=PLC1"
```

```json
{
  "code": "0",
  "msg": "success",
  "isSuccess": true,
  "data": {
    "page": 1,
    "size": 20,
    "total": 2,
    "records": [
      {
        "id": "a1b2c3d4e5f6",
        "name": "PLC-01",
        "protocolName": "ModbusTcp",
        "description": "Workshop A Line 1 PLC",
        "status": 1,
        "online": true,
        "lastSuccessTime": "2026-08-27 10:29:58",
        "createdAt": "2026-08-01 09:00:00",
        "updatedAt": "2026-08-20 14:12:33"
      }
    ]
  },
  "serverTime": "2026-08-27 10:30:00"
}
```

### 4.2 Batch Device Name Query

Batch query device names by a list of device IDs. Suitable for external systems that already hold device IDs and need to display names (e.g. reports, dashboards) — one request completes the ID → name batch mapping without paging through 4.1 repeatedly.

```
POST /openApi/device/names
Content-Type: application/json
```

**Request Parameters (JSON Body)**

| Parameter | Type | Required | Description |
| --------- | ---- | -------- | ----------- |
| ids | string[] | Yes | Device ID list, at least 1 (e.g. `["id1","id2"]`) |

**Response Fields (data is an array)**

| Field | Type | Description |
| ----- | ---- | ----------- |
| id | string | Device ID |
| name | string | Device name |

> Notes:
> - The return order matches the order of the given `ids`;
> - **IDs not present in the database are omitted from the result** (no error, no placeholder) — callers must detect missing entries themselves;
> - Keep each request to no more than 100 IDs; split longer lists into batches.

**Example**

```bash
curl -X POST -H "X-Api-Key: your-app-key" -H "Content-Type: application/json" \
     -d '{"ids":["a1b2c3d4e5f6","f6e5d4c3b2a1","not-exist-id"]}' \
     "http://192.168.1.100:9081/openApi/device/names"
```

```json
{
  "code": "0",
  "msg": "success",
  "isSuccess": true,
  "data": [
    { "id": "a1b2c3d4e5f6", "name": "PLC-01" },
    { "id": "f6e5d4c3b2a1", "name": "PLC-02" }
  ],
  "serverTime": "2026-09-07 10:30:00"
}
```

### 4.3 Batch Address Label Query

Batch query address labels by a device ID array plus an address ID array. Suitable for external systems that already hold device IDs and address IDs and need to display human-readable address descriptions (e.g. reports, dashboards) — one request completes the addressID → label batch mapping without paging through 4.1 / 4.4 per device.

```
POST /openApi/deviceAddress/labels
Content-Type: application/json
```

**Request Parameters (JSON Body)**

| Parameter | Type | Required | Description |
| --------- | ---- | -------- | ----------- |
| deviceIds | string[] | Yes | Device ID list, at least 1 |
| addressIds | string[] | Yes | Address ID list, at least 1 |

> `deviceIds` is used for ownership validation: addresses not belonging to these devices are treated as non-existent.

**Response Fields (data is an array)**

| Field | Type | Description |
| ----- | ---- | ----------- |
| id | string | Address ID |
| deviceId | string | Owning device ID |
| deviceName | string | Owning device name; empty string `""` if the device has been deleted |
| label | string | Address label (human-readable description of the address name, e.g. "temperature", "current") |

> Notes:
> - The return order matches the order of the given `addressIds`;
> - **Address IDs not present in the database (or not belonging to the given devices) are omitted from the result** (no error, no placeholder) — callers must detect missing entries themselves;
> - Keep `deviceIds` and `addressIds` to no more than 100 each per request; split longer lists into batches.

**Example**

```bash
curl -X POST -H "X-Api-Key: your-app-key" -H "Content-Type: application/json" \
     -d '{"deviceIds":["a1b2c3d4e5f6"],"addressIds":["f6e5d4c3b2a1","0e9d8c7b6a5f","not-exist-id"]}' \
     "http://192.168.1.100:9081/openApi/deviceAddress/labels"
```

```json
{
  "code": "0",
  "msg": "success",
  "isSuccess": true,
  "data": [
    { "id": "f6e5d4c3b2a1", "deviceId": "a1b2c3d4e5f6", "deviceName": "PLC-01", "label": "Furnace temperature" },
    { "id": "0e9d8c7b6a5f", "deviceId": "a1b2c3d4e5f6", "deviceName": "PLC-01", "label": "Spindle current" }
  ],
  "serverTime": "2026-09-07 10:35:00"
}
```

### 4.4 Paginated Device Address Query (Point Definitions)

Lists the address (point) definitions of a device. Use the `id` returned by 4.1 as `deviceId`.

```
GET /openApi/deviceAddress/page
```

**Request Parameters (Query)**

| Parameter | Type | Required | Description |
| --------- | ---- | -------- | ----------- |
| page | int | Yes | Page number, ≥1 |
| size | int | Yes | Page size, 1~100 |
| deviceId | string | Yes | Owning device ID (the `id` returned by 4.1) |
| name | string | No | Address name, fuzzy match |

**Response Fields (elements in records)**

| Field | Type | Description |
| ----- | ---- | ----------- |
| id | string | Address ID |
| deviceId | string | Owning device ID |
| name | string | Address name |
| label | string | Human-readable description of the name (e.g. "temperature", "current") |
| commonDataType | string | Common data type name (as displayed in the admin console) |
| dataType | string | Protocol-internal data type name (e.g. `modbus.bool`, `s7.int16`), used by the driver for decoding |
| rwPermission | string | Read/write permission: `R`=read-only, `W`=write-only, `RW`=read-write |
| scanFrequency | int | Collection scan frequency, in milliseconds |
| description | string | Address description |
| status | int | Config status: `1`=enabled, `0`=disabled |

**Example**

```bash
curl -H "X-Api-Key: your-app-key" \
     "http://192.168.1.100:9081/openApi/deviceAddress/page?page=1&size=50&deviceId=a1b2c3d4e5f6"
```

```json
{
  "code": "0",
  "msg": "success",
  "isSuccess": true,
  "data": {
    "page": 1,
    "size": 50,
    "total": 12,
    "records": [
      {
        "id": "f6e5d4c3b2a1",
        "deviceId": "a1b2c3d4e5f6",
        "name": "D100/temperature",
        "label": "Furnace temperature",
        "commonDataType": "float32",
        "dataType": "modbus.float32",
        "rwPermission": "R",
        "scanFrequency": 1000,
        "description": "Line 1 furnace temperature",
        "status": 1
      }
    ]
  },
  "serverTime": "2026-08-27 10:30:05"
}
```

## 5. Error Code Reference

The error codes the public API may return are listed below (the complete list is in this section; other internal error codes never appear in public APIs):

| code | HTTP | Meaning | Recommended Action |
| ---- | ---- | ------- | ------------------ |
| `0` | 200 | Success | - |
| `70003` | 401 | Public API key is empty | The `X-Api-Key` header is missing; check the request construction |
| `70004` | 401 | Public API key invalid | Key is wrong or has been deleted; contact the administrator |
| `PARAM_ERROR` | 200 | Parameter validation failed | Fix the request parameters per msg (msg looks like `参数校验失败: size max`) |
| `-1` | 200 | System error | Retry later; if persistent, contact the administrator and check gateway logs |

## 6. FAQ

**Q: The request returns 401 but my key is correct?**
Troubleshooting order: ① Confirm the header name is `X-Api-Key` (HTTP header names are case-insensitive — `x-api-key` works too — but the header must be present); ② Confirm the key value matches the one issued — **the key value itself is case-sensitive**, beware of stray whitespace when copying; ③ Confirm the key has not been deleted or regenerated by the administrator. If it still fails, contact the administrator.

**Q: How do I read the current real-time value of an address?**
The public API currently only provides queries at the configuration level (devices and address definitions). For **real-time data**, subscribe via one of the gateway's push channels (e.g. MQTT), or contact the administrator to extend the public API.

**Q: How long until address configuration changes take effect?**
After the admin console modifies device/address configuration, the gateway hot-reloads it within about 10 seconds; subsequent queries return the latest configuration.
