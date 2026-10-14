# IoT Gateway Public API Documentation

> This document is intended for **third-party system developers**, describing how to query device data via the gateway public API (`/openApi/*`).
> All endpoints are read-only queries; no write operations are provided.

## Version History

| Version | Date | Changes | Breaking Changes |
| ------- | ---- | ------- | ---------------- |
| v1.0 | 2026-08-27 | Initial release: paginated device query, paginated device address query | - |
| v1.1 | 2026-09-07 | Added: batch device name query (4.2), batch address label query (4.3); subsequent section numbers shifted accordingly | - |
| v1.2 | 2026-10-09 | Added: full sync of devices and points (4.5, **cursor paging**, returns a batch of devices each with all of its points); point objects gain `createdAt`/`updatedAt` (applies to both 4.4 and 4.5); §3.3 notes cursor paging | - |
| v1.3 | 2026-10-09 | Added: device reliability statistics (4.6), point abnormality top-N (4.7) — alarm-based availability and abnormality rankings, for cross-gateway aggregation | - |
| v1.4 | 2026-10-10 | 4.6 unit change: `availability` goes from a 0~1 fraction to a **percentage (0~100)** rounded to two decimals; `downSeconds` / `mttrSeconds` are now rounded to whole seconds | ⚠️ Yes (the `availability` unit changed — callers integrated against the 0~1 form must parse a percentage instead) |

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
http://<gateway-IP>:9082
```

> The port depends on the actual deployment configuration; default `9082`.

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

> **Exception: 4.5 (batch sync) uses a "cursor" instead of `page`/`size`.** Its response is
> `{size, hasMore, nextAfter, records}` and carries no `total`. During a long full traversal,
> `OFFSET` paging shifts the unread window whenever rows are inserted or deleted, causing missed
> or duplicated rows; a cursor resumes from an ordered key and is unaffected. See 4.5.

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
| protocolName | string | Protocol name (e.g. `ModBus.TCP`, `Siemens.S7`; identical to the protocol name shown on the gateway's Protocol Management page) |
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
     "http://192.168.1.100:9082/openApi/device/page?page=1&size=20&name=PLC1"
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
        "protocolName": "ModBus.TCP",
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
     "http://192.168.1.100:9082/openApi/device/names"
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
     "http://192.168.1.100:9082/openApi/deviceAddress/labels"
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
| createdAt | string | Creation time |
| updatedAt | string | Last modification time (changes whenever the configuration is edited) |

**Example**

```bash
curl -H "X-Api-Key: your-app-key" \
     "http://192.168.1.100:9082/openApi/deviceAddress/page?page=1&size=50&deviceId=a1b2c3d4e5f6"
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
        "status": 1,
        "createdAt": "2026-08-01 09:10:00",
        "updatedAt": "2026-08-20 14:12:33"
      }
    ]
  },
  "serverTime": "2026-08-27 10:30:05"
}
```

### 4.5 Batch Sync of Devices and Points

Fetches **all devices together with their point definitions** in one traversal, for full data
synchronisation into an external system. Each page returns a batch of devices with their points
**nested inside**, so a device is never dropped just because it currently has no points.

```
GET /openApi/device/sync
```

**Request parameters (Query)**

| Parameter | Type | Required | Description |
| --------- | ---- | -------- | ----------- |
| after | string | no | **Cursor**: the `nextAfter` from the previous page. **Omit on the first request** |
| size | int | yes | Devices per page, 1~100 |

**Response `data`**

| Field | Type | Description |
| ----- | ---- | ----------- |
| size | int | Echo of the requested `size` |
| hasMore | boolean | Whether another page exists |
| nextAfter | string | Cursor for the next page; empty string `""` when `hasMore` is `false` |
| records | array | Devices on this page, each described below |

**Elements of `records`**: all device fields from 4.1 (`id`/`name`/`protocolName`/`description`/
`status`/`online`/`lastSuccessTime`/`createdAt`/`updatedAt`, with identical semantics), plus:

| Field | Type | Description |
| ----- | ---- | ----------- |
| addresses | array | All points of this device; **`[]` (not `null`) when it has none**. Each item matches the point object of 4.4 |
| addresses[].id | string | Point ID |
| addresses[].deviceId | string | Owning device ID |
| addresses[].name | string | Address name (the device address notation, e.g. `%MW100`) |
| addresses[].label | string | Human-readable name (e.g. "temperature", "current") |
| addresses[].commonDataType | string | Common data type name |
| addresses[].dataType | string | Protocol-internal data type name (e.g. `modbus.float32`) |
| addresses[].rwPermission | string | `R` / `W` / `RW` |
| addresses[].scanFrequency | int | Scan frequency in milliseconds |
| addresses[].description | string | Point description |
| addresses[].status | int | `1` = enabled, `0` = disabled |
| addresses[].createdAt | string | Creation time |
| addresses[].updatedAt | string | Last modification time |

**Traversal**

```
after = ""                      # omit on the first request
loop:
    resp = GET /openApi/device/sync?size=100[&after=<after>]
    process(resp.data.records)
    if not resp.data.hasMore: break
    after = resp.data.nextAfter
```

**Example**

```bash
curl -H "X-Api-Key: your-app-key" \
     "http://192.168.1.100:9082/openApi/device/sync?size=2"
```

```json
{
  "code": "0",
  "msg": "success",
  "isSuccess": true,
  "data": {
    "size": 2,
    "hasMore": true,
    "nextAfter": "a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4",
    "records": [
      {
        "id": "a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4",
        "name": "PLC #1",
        "protocolName": "ModBus.TCP",
        "description": "Line 1 PLC, workshop A",
        "status": 1,
        "online": true,
        "lastSuccessTime": "2026-10-09 10:28:11",
        "createdAt": "2026-08-01 09:00:00",
        "updatedAt": "2026-08-20 14:12:33",
        "addresses": [
          {
            "id": "f6e5d4c3b2a1f6e5d4c3b2a1f6e5d4c3",
            "deviceId": "a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4",
            "name": "D100",
            "label": "Furnace temperature",
            "commonDataType": "float32",
            "dataType": "modbus.float32",
            "rwPermission": "R",
            "scanFrequency": 1000,
            "description": "Line 1 furnace temperature",
            "status": 1,
            "createdAt": "2026-08-01 09:10:00",
            "updatedAt": "2026-08-20 14:12:33"
          }
        ]
      },
      {
        "id": "b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5",
        "name": "PLC #2 (no points yet)",
        "protocolName": "Siemens.S7",
        "description": "",
        "status": 1,
        "online": false,
        "lastSuccessTime": "",
        "createdAt": "2026-08-02 10:00:00",
        "updatedAt": "2026-08-02 10:00:00",
        "addresses": []
      }
    ]
  },
  "serverTime": "2026-10-09 10:30:00"
}
```

> **Notes:**
> - **Ordered by device `id` ascending** (unlike 4.1, which is `created_at` descending). `id` is a
>   time-ordered UUID, so devices created during a traversal have larger ids and are read later in
>   the same traversal, while deleted devices simply disappear — **no missed or duplicated rows**.
> - **Pass the previous page's `nextAfter` back verbatim** as `after`; do not construct it yourself.
>   An invalid `after` does not raise an error but resumes after that value, potentially skipping devices.
> - **Disabled (`status=0`) devices and points are returned normally**; filter on `status` as needed,
>   consistent with 4.1 / 4.4.
> - **Page size**: `size` is capped at 100 while a device's point count is unbounded. Gateways with
>   very large devices should use a smaller `size`.
> - A traversal ends when `hasMore` is `false` (then `nextAfter` is `""`) — **not** when `records` is empty.

### 4.6 Device Reliability Statistics

Aggregates each device's outage faults over a window, returning availability, fault episodes,
cumulative downtime and MTTR. Derived from the **alarm accounting basis** (deduplicated fault
segments, see the notes below).

```
GET /openApi/alarm/deviceReliability
```

**Request parameters (Query)**

| Parameter | Type | Required | Description |
| --------- | ---- | -------- | ----------- |
| from | string | yes | Window start date, `YYYY-MM-DD`, **inclusive** |
| to | string | yes | Window end date, `YYYY-MM-DD`, **inclusive** (the window actually ends at `to` 00:00:00 of the next day) |
| limit | int | no | Return only the top N by lowest availability, 1~500 |

> - The window must be **≤ 90 days**; exceeding it returns `PARAM_ERROR`. A `to` earlier than `from`, or a malformed date, likewise errors.
> - `from`/`to` are **deliberately required with no default** — the accounting basis is not hidden in a server-side default; the caller aligns it with its own reporting period.
> - **Omitted or ≤0 `limit` = no truncation (return all)**. This is **deliberately the opposite** of 4.7's `limit` default: devices are a naturally small dimension, and a truncated list is easily misread as "all devices on the network". Callers wanting to re-sort by another field (episodes / MTTR) simply omit `limit` to get the full set.

**Response `data`**

| Field | Type | Description |
| ----- | ---- | ----------- |
| gatewayId | string | This gateway's ID (use it to label each row when aggregating across gateways) |
| gatewayName | string | This gateway's name |
| records | array | Device reliability list, sorted by availability ascending (worst first); **an empty array `[]` when no device has faults** (not `null`) |

**Elements of `records`**

| Field | Type | Description |
| ----- | ---- | ----------- |
| deviceId | string | Device ID |
| deviceName | string | Device name (a name snapshot; still present after the device is deleted) |
| episodes | int | Number of fault segments in the window |
| downSeconds | number | Cumulative downtime seconds in the window, **rounded to whole seconds** (segments crossing a window boundary count only the overlapping part; an unrecovered segment counts up to the window end) |
| mttrSeconds | number | Mean time to repair (seconds), **rounded to whole seconds**, counting only segments that started within the window and have recovered; **`-1` when there is no completed fault** |
| availability | number | Availability as a **percentage (0~100)**, rounded to two decimals (e.g. `99.31` means 99.31%); **denominator is the whole window** |
| windowSeconds | number | Total window seconds (for callers to recompute availability with their own denominator) |
| ranking | int | Rank by availability ascending, 1 = worst |

> **Notes:**
> - **Devices with no fault in the window do not appear in the result** (equivalent to 100% availability). The result set covers only "devices that had faults", so it works directly as a "worst devices" ranking — but it is **not** a full device list.
> - With `limit`, `ranking` is still the **global rank** (truncation happens after sorting), so `limit=10` yields "the worst 10 devices".
> - **`ranking` and the row order come from the unrounded availability**; only the emitted value is rounded to two decimals. Two devices whose downtime differs by seconds therefore never tie, and the rank never falls back to device ID ordering.
> - **The denominator of `availability` is the whole window and does not exclude the gateway's own downtime** — while the gateway is down it polls nothing and produces no alarms, so that period counts as online for every device. Callers needing to exclude it should recompute using `windowSeconds` (the gateway cannot observe its own downtime).
> - **Basis is the "alarm basis"**: only fault segments that survived deduplication and were persisted count (an alarm is raised only after N consecutive failed polls); jitter below the dedup threshold produces no alarm and is not counted. The resulting availability is therefore slightly higher than "true" availability — it is kept consistent with the gateway's own alarm definition; this is where it diverges from third-party uptime figures.

**Example**

```bash
curl -H "X-Api-Key: your-app-key" \
     "http://192.168.1.100:9082/openApi/alarm/deviceReliability?from=2026-10-01&to=2026-10-09&limit=10"
```

```json
{
  "code": "0",
  "msg": "success",
  "isSuccess": true,
  "data": {
    "gatewayId": "gw-01",
    "gatewayName": "Gateway-1",
    "records": [
      {
        "deviceId": "a1b2c3d4e5f6",
        "deviceName": "PLC-1",
        "episodes": 3,
        "downSeconds": 5400,
        "mttrSeconds": 1800,
        "availability": 99.31,
        "windowSeconds": 777600,
        "ranking": 1
      }
    ]
  },
  "serverTime": "2026-10-09 21:00:00"
}
```

### 4.7 Point Abnormality Top-N

Aggregates the points with the most pronounced collection abnormalities over a window.
**Collection quality abnormalities and threshold violations are counted separately**, never merged:
the former points to operations (address / parsing issues) while the latter may just be process drift.

```
GET /openApi/alarm/pointAbnormal
```

**Request parameters (Query)**

| Parameter | Type | Required | Description |
| --------- | ---- | -------- | ----------- |
| deviceId | string | no | Restrict to points under one device; omitted = all devices |
| from | string | yes | Window start date `YYYY-MM-DD`, inclusive |
| to | string | yes | Window end date `YYYY-MM-DD`, inclusive |
| limit | int | no | Return top N, 1~500; **server default 20 when omitted or ≤0** |

> - Window rules match 4.6 (`from`/`to` required, format validated, **≤ 90 days**).
> - `deviceId` and `limit` combine: passing only `deviceId` returns all qualifying points of that device; passing only `limit` returns the plant-wide top N.
> - **The default for an omitted `limit` is server-side** (deliberately the opposite of 4.6's "omitted = no truncation") — points outnumber devices by 1~2 orders of magnitude, and without a fallback the long tail would be returned.

**Response `data`**

| Field | Type | Description |
| ----- | ---- | ----------- |
| gatewayId | string | This gateway's ID |
| gatewayName | string | This gateway's name |
| records | array | Point abnormality list, sorted by episode count descending; **an empty array `[]` when there are no abnormal points** (not `null`) |

**Elements of `records`**

| Field | Type | Description |
| ----- | ---- | ----------- |
| targetId | string | Point ID (i.e. the point `id` returned by 4.4 / 4.5) |
| targetName | string | Concatenation of "device name.point name", **for display directly**; a name snapshot, still readable after the device is deleted |
| deviceId | string | Owning device ID (use this for grouping / filtering) |
| qualityEpisodes | int | Collection quality abnormality segments in the window |
| thresholdEpisodes | int | Threshold violation segments in the window |
| totalEpisodes | int | Sum of the two |
| lastOccurTime | string | First occurrence time of the most recent abnormality in the window; `""` when none |
| ranking | int | Rank by `totalEpisodes` descending, 1 = worst |

> **Notes:**
> - `targetName` is a concatenated string and is ambiguous when the name itself contains a dot — **do not split** it to get the device name; use `deviceId` for that.
> - `ranking` is **not a rate**: points sampled more frequently make more judgements per unit time, so jittery points are more likely to appear; a permanently bad point is unaffected (edge-triggered, ever only one segment).

**Example**

```bash
curl -H "X-Api-Key: your-app-key" \
     "http://192.168.1.100:9082/openApi/alarm/pointAbnormal?from=2026-10-01&to=2026-10-09&limit=20"
```

```json
{
  "code": "0",
  "msg": "success",
  "isSuccess": true,
  "data": {
    "gatewayId": "gw-01",
    "gatewayName": "Gateway-1",
    "records": [
      {
        "targetId": "f6e5d4c3b2a1",
        "targetName": "PLC-1.FurnaceTemperature",
        "deviceId": "a1b2c3d4e5f6",
        "qualityEpisodes": 12,
        "thresholdEpisodes": 5,
        "totalEpisodes": 17,
        "lastOccurTime": "2026-10-08 14:22:03",
        "ranking": 1
      }
    ]
  },
  "serverTime": "2026-10-09 21:00:00"
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
