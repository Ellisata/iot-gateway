# IoT Gateway Public API Documentation

> This document is intended for **third-party system developers**, describing how to query device data and synchronize thing-model configuration via the gateway public API (`/openApi/*`).
> Except for **4.8 Control Write** and **4.13~4.15 thing-model configuration writes/deletes**, all endpoints are read-only queries.
> 4.8 changes the physical device state; make sure you have operational authorization before calling it.
> 4.13~4.15 only modify thing-model configuration inside the gateway and never touch field devices.

## Version History

| Version | Date | Changes | Breaking Changes |
| ------- | ---- | ------- | ---------------- |
| v1.0 | 2026-08-27 | Initial release: paginated device query, paginated device address query | - |
| v1.1 | 2026-09-07 | Added: batch device name query (4.2), batch address label query (4.3); subsequent section numbers shifted accordingly | - |
| v1.2 | 2026-10-09 | Added: full sync of devices and points (4.5, **cursor paging**, returns a batch of devices each with all of its points); point objects gain `createdAt`/`updatedAt` (applies to both 4.4 and 4.5); §3.3 notes cursor paging | - |
| v1.3 | 2026-10-09 | Added: device reliability statistics (4.6), point abnormality top-N (4.7) — alarm-based availability and abnormality rankings, for cross-gateway aggregation | - |
| v1.4 | 2026-10-10 | 4.6 unit change: `availability` goes from a 0~1 fraction to a **percentage (0~100)** rounded to two decimals; `downSeconds` / `mttrSeconds` are now rounded to whole seconds | ⚠️ Yes (the `availability` unit changed — callers integrated against the 0~1 form must parse a percentage instead) |
| v1.5 | 2026-10-31 | Added 4.8 Control Write (write PLC points). **The only public endpoint that changes the physical device state**; gated by `control.enabled`, protocol `Writable`, and point `W` permission | - |
| v1.6 | 2026-11-02 | 4.8 write permission tightened: for points bound to a thing-model property, the property's `access_mode` must also include `W` (new error code `40039`). Writing to an `RW` point bound to an `R` property is no longer allowed | ⚠️ Yes (previously writable "RW point + R property" now returns `40039`) |
| v1.7 | 2026-11-06 | Added: alarm history paging (4.9), current active alarms (4.10), threshold record paging (4.11) — detail-level queries that complement the aggregate reports in 4.6 / 4.7 | - |
| v1.8 | 2026-11-06 | Added: batch sync of subscribed devices and points (4.12, cursor paging, same shape as 4.5; the data source is the device/point metadata index maintained from subscription messages) | - |
| v1.9 | 2026-11-09 | Added: thing model and property batch upsert (4.13, create-or-update by the `code` / `identifier` natural keys, partial success across models) | - |
| v1.10 | 2026-11-09 | Added: thing-model batch delete (4.14) and thing-model property batch delete (4.15), deleting per natural key and returning per-item results | - |
| v1.11 | 2026-11-11 | Added: configuration sync (4.16) — capability negotiation, read APIs for five domains, and batch writes for push channels / alarm webhooks / runtime switches / device points. Read APIs return masked desired state plus `contentHash` for template deployment and drift detection | - |

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

> **Exception: 4.5 / 4.12 (batch sync) use a "cursor" instead of `page`/`size`.** Their response is
> `{size, hasMore, nextAfter, records}` and carries no `total`. During a long full traversal,
> `OFFSET` paging shifts the unread window whenever rows are inserted or deleted, causing missed
> or duplicated rows; a cursor resumes from an ordered key and is unaffected. See 4.5 / 4.12.

### 3.4 Time Format

All time fields use the `yyyy-MM-dd HH:mm:ss` format (server local time).
Note that an empty string `""` for the device's `lastSuccessTime` means **no successful collection record yet**.

### 3.5 Idempotency and Call Frequency

- Except for 4.8 and 4.13~4.15, all public API endpoints are idempotent read-only queries;
- 4.8 uses `requestId` for idempotency: repeating the same value returns the first result without writing the PLC again;
- 4.13 upserts by natural keys — `code` for models and `identifier` for properties. Repeating the same payload
  never creates duplicate rows; the second call just updates the existing rows to the submitted values;
- 4.14 / 4.15 return `41002` / `41004` when deleting a model or property that no longer exists — this is **not**
  treated as success. Integrations that retry "ensure absent" may treat those codes as already deleted;
- Control writes are rate-limited per device and per point; exceeding the limit returns `40038`, so back off and retry;
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

### 4.8 Control Write (write PLC points)

> ⚠️ This is the **only public endpoint that changes the physical device state**.
> The caller must have operational authorization and confirm with the operator that the
> device is safe to control before each write.

**Request**

```
POST /openApi/control/write
X-Api-Key: <AppKey>
Content-Type: application/json
```

```json
{
  "deviceId": "a1b2c3d4e5f6",
  "requestId": "b6f0b0c6-4d7e-4b6a-9f3a-2f6f1c0a9d11",
  "verify": true,
  "items": [
    { "deviceAddressId": "addr-1", "value": "42" }
  ]
}
```

| Field | Required | Description |
| ----- | -------- | ----------- |
| `deviceId` | Yes | Target device id |
| `requestId` | Recommended | Idempotency key. Repeating the same value returns the first result without writing again; required for MQTT/retry scenarios |
| `verify` | No | `true` reads the point back after writing (only for points with read permission) |
| `items[].deviceAddressId` | Yes | **Point id, not a PLC address**; must belong to the device, be enabled, and have `W` in `rw_permission` |
| `items[].value` | Yes | Target value as a string; the plugin encodes it according to the point data type (bool accepts `true`/`false`/`1`/`0`) |

At most 50 points per request. **Batched writes are not transactional**: each point returns its own result.

**Preconditions** (any failure rejects the request before touching the device):

1. `control.enabled=true`;
2. The protocol plugin declares `Writable`;
3. The point `rw_permission` contains `W`;
4. The device is in the active collection task and its link is up (this API does not trigger a reconnect);
5. The per-device/per-point write rate limits are not exceeded.

**Response**

```json
{
  "code": "0",
  "msg": "success",
  "isSuccess": true,
  "data": {
    "requestId": "b6f0b0c6-4d7e-4b6a-9f3a-2f6f1c0a9d11",
    "deviceId": "a1b2c3d4e5f6",
    "deviceName": "PLC-1",
    "protocolName": "ModBus.TCP",
    "duplicate": false,
    "results": [
      {
        "addressId": "addr-1",
        "addressName": "40001",
        "addressLabel": "Temperature",
        "dataType": "int16",
        "rwPermission": "RW",
        "value": "42",
        "resultValue": "42",
        "readbackValue": "42",
        "verified": true,
        "ok": true,
        "code": 0,
        "message": ""
      }
    ]
  },
  "serverTime": "2026-10-31 10:30:00"
}
```

- `results[].code`: `0` success, `1` invalid address, `2` read-only, `3` invalid value, `4` unsupported, `5` device rejected, `6` link failure / state unknown;
- `duplicate=true` means the `requestId` was already processed and nothing was written again;
- After a link failure the batch state may be unknown; read the points back or confirm on site before retrying.

### 4.9 Alarm History (paginated)

Paginated query of the alarms persisted by the gateway (offline / data quality / threshold / recovery).
It complements the aggregate reports in 4.6 / 4.7: those answer "who is worst", this answers
"which alarms exactly".

```
GET /openApi/alarm/pageAlarm
```

**Request parameters (Query)**

| Parameter | Type | Required | Description |
| --------- | ---- | -------- | ----------- |
| page | int | Yes | Page number, starting from 1 |
| size | int | Yes | Page size, 1~100 |
| targetName | string | No | Target name, fuzzy match |
| targetType | string | No | `device` / `channel` / `point` / `point_threshold`; omitted = all |
| alarmType | string | No | `offline` / `quality` / `threshold` / `recover`; omitted = all |
| status | string | No | `active` / `cleared`; omitted = all |

**Response `data`**

Pagination envelope (see §3.3); each element of `records`:

| Field | Type | Description |
| ----- | ---- | ----------- |
| id | string | Alarm ID |
| targetId | string | Target ID (device / channel / point) |
| targetName | string | Target name (snapshot taken when the alarm occurred; still readable after the target is deleted) |
| targetType | string | `device` / `channel` / `point` / `point_threshold` |
| alarmType | string | `offline` / `quality` / `threshold` / `recover` |
| alarmTypeName | string | Human-readable alarm type name (in the gateway's configured language) |
| level | string | Alarm level |
| content | string | Alarm content |
| status | string | `active` / `cleared` |
| statusName | string | Human-readable status name |
| firstOccurTime | string | First occurrence time |
| lastOccurTime | string | Most recent occurrence time |
| clearTime | string | Recovery time; empty string `""` when not cleared |
| createdAt | string | Creation time |
| updatedAt | string | Update time |

**Example**

```bash
curl -H "X-Api-Key: your-app-key" \
     "http://192.168.1.100:9082/openApi/alarm/pageAlarm?page=1&size=20&status=active&alarmType=offline"
```

```json
{
  "code": "0",
  "msg": "success",
  "isSuccess": true,
  "data": {
    "page": 1,
    "size": 20,
    "total": 1,
    "records": [
      {
        "id": "0b6f4f8e-6f2e-4a8e-9d5c-2f7b0f3c1a11",
        "targetId": "a1b2c3d4e5f6",
        "targetName": "PLC-1",
        "targetType": "device",
        "alarmType": "offline",
        "alarmTypeName": "Offline alarm",
        "level": "critical",
        "content": "Device failed 3 consecutive collection rounds",
        "status": "active",
        "statusName": "Active",
        "firstOccurTime": "2026-11-06 08:12:30",
        "lastOccurTime": "2026-11-06 08:13:00",
        "clearTime": "",
        "createdAt": "2026-11-06 08:12:30",
        "updatedAt": "2026-11-06 08:13:00"
      }
    ]
  },
  "serverTime": "2026-11-06 10:30:00"
}
```

### 4.10 Active Alarms

Returns the currently **uncleared** offline alarms (offline devices + offline channels),
i.e. the list of offline targets. It is not paginated — active targets are the small set that
needs immediate attention, and pagination would only add another loop for the caller.

```
GET /openApi/alarm/listActive
```

**Request parameters**: none.

**Response `data`**

An array of alarm objects with exactly the same fields as the 4.9 `records`, ordered by
`firstOccurTime` descending. Empty array `[]` (not `null`) when there are no active alarms.

**Example**

```bash
curl -H "X-Api-Key: your-app-key" \
     "http://192.168.1.100:9082/openApi/alarm/listActive"
```

```json
{
  "code": "0",
  "msg": "success",
  "isSuccess": true,
  "data": [
    {
      "id": "0b6f4f8e-6f2e-4a8e-9d5c-2f7b0f3c1a11",
      "targetId": "a1b2c3d4e5f6",
      "targetName": "PLC-1",
      "targetType": "device",
      "alarmType": "offline",
      "alarmTypeName": "Offline alarm",
      "level": "critical",
      "content": "Device failed 3 consecutive collection rounds",
      "status": "active",
      "statusName": "Active",
      "firstOccurTime": "2026-11-06 08:12:30",
      "lastOccurTime": "2026-11-06 08:13:00",
      "clearTime": "",
      "createdAt": "2026-11-06 08:12:30",
      "updatedAt": "2026-11-06 08:13:00"
    }
  ],
  "serverTime": "2026-11-06 10:30:00"
}
```

### 4.11 Threshold Records (paginated)

Paginated query of threshold-breach samples. This is **not** the same thing as the alarms in 4.9:
alarms are a debounced state machine (requires N consecutive rounds), while this table keeps
evidence for **every raw breach** (throttled within a continuous breach segment) — so a 1~2 round
spike is only visible here.

```
GET /openApi/alarm/pageThresholdRecord
```

**Request parameters (Query)**

| Parameter | Type | Required | Description |
| --------- | ---- | -------- | ----------- |
| page | int | Yes | Page number, starting from 1 |
| size | int | Yes | Page size, 1~100 |
| deviceId | string | No | Limit to one device; omitted = all |
| name | string | No | Point name, fuzzy match |

**Response `data`**

Pagination envelope (see §3.3); each element of `records`:

| Field | Type | Description |
| ----- | ---- | ----------- |
| id | string | Record ID |
| deviceId | string | Owning device ID |
| deviceName | string | Device name (snapshot at occurrence) |
| addressId | string | Point ID (i.e. the point `id` from 4.4 / 4.5) |
| addressName | string | Point name |
| value | string | Collected value that breached the threshold |
| kind | string | Value kind (bool / int / float / str) |
| bound | string | Which part of the condition matched: `high` / `low` for `range`, otherwise the operator name |
| boundName | string | Human-readable name of the matched condition |
| thresholdOp | string | Snapshot of the threshold operator when written |
| thresholdValue | string | Snapshot of the operands when written (**JSON array string**, e.g. `["80","100"]`) |
| occurredAt | string | Breach time |

> - `thresholdOp` / `thresholdValue` are **snapshots taken when the record was written**: after the
>   configuration changes, history still proves which rule was in effect at that time.
> - This table keeps the most recent 90 days; older records are pruned.

**Example**

```bash
curl -H "X-Api-Key: your-app-key" \
     "http://192.168.1.100:9082/openApi/alarm/pageThresholdRecord?page=1&size=20&deviceId=a1b2c3d4e5f6"
```

```json
{
  "code": "0",
  "msg": "success",
  "isSuccess": true,
  "data": {
    "page": 1,
    "size": 20,
    "total": 1,
    "records": [
      {
        "id": "5c2b1f0e-7d8a-4f21-b3e4-9a6c8d5e2f10",
        "deviceId": "a1b2c3d4e5f6",
        "deviceName": "PLC-1",
        "addressId": "f6e5d4c3b2a1",
        "addressName": "40001",
        "value": "105",
        "kind": "int",
        "bound": "high",
        "boundName": "Above upper limit",
        "thresholdOp": "range",
        "thresholdValue": "[\"0\",\"100\"]",
        "occurredAt": "2026-11-06 09:41:12"
      }
    ]
  },
  "serverTime": "2026-11-06 10:30:00"
}
```

### 4.12 Batch Sync of Subscribed Devices and Points

Fetches **all devices that have appeared in subscription messages, together with their point
metadata**, for full synchronisation into an external system. The shape is identical to 4.5
(cursor paging, points nested inside devices), but the **data source differs**: this endpoint
reads the gateway's subscription device/point index (`mqtt_subscription_device_point`), which is
maintained from MQTT subscription parsing results or manually in the admin UI — **not** the local
PLC device table.

It therefore has no protocol name, online status, collection time, or other acquisition fields.

```
GET /openApi/subscription/devicePoint/sync
```

**Request parameters (Query)**

| Parameter | Type | Required | Description |
| --------- | ---- | -------- | ----------- |
| after | string | no | **Cursor**: the `nextAfter` from the previous page. **Omit on the first request** |
| size | int | yes | Devices per page, 1~100 |

**Response `data`** (same shape as 4.5)

| Field | Type | Description |
| ----- | ---- | ----------- |
| size | int | Echo of the requested `size` |
| hasMore | boolean | Whether another page exists |
| nextAfter | string | Cursor for the next page; empty string `""` when `hasMore` is `false` |
| records | array | Devices on this page, each described below |

**Elements of `records`**

| Field | Type | Description |
| ----- | ---- | ----------- |
| deviceId | string | Subscribed device ID (device identity parsed from messages) |
| deviceName | string | Device name; empty string `""` when configured by neither subscription nor manual entry |
| points | array | All points of this device; **always non-empty** (devices are derived from point rows) |

**Elements of `points`**

| Field | Type | Description |
| ----- | ---- | ----------- |
| deviceId | string | Owning device ID (same as the parent, for easy flattening) |
| deviceAddressId | string | Point ID |
| deviceAddressLabel | string | Point label (e.g. "current", "voltage") |
| kind | string | Data type category (e.g. `float`); empty string when not configured |
| source | string | Maintenance source: `auto` = auto-collected from subscription messages, `manual` = created/edited/imported manually |
| createdAt | string | First collection time |
| updatedAt | string | Last metadata update time |

**Traversal**

```
after = ""                      # omit on the first request
loop:
    resp = GET /openApi/subscription/devicePoint/sync?size=100[&after=<after>]
    process(resp.data.records)
    if not resp.data.hasMore: break
    after = resp.data.nextAfter
```

**Example**

```bash
curl -H "X-Api-Key: your-app-key" \
     "http://192.168.1.100:9082/openApi/subscription/devicePoint/sync?size=2"
```

```json
{
  "code": "0",
  "msg": "success",
  "isSuccess": true,
  "data": {
    "size": 2,
    "hasMore": false,
    "nextAfter": "",
    "records": [
      {
        "deviceId": "meter-1",
        "deviceName": "Meter 1",
        "points": [
          {
            "deviceId": "meter-1",
            "deviceAddressId": "current",
            "deviceAddressLabel": "Phase A current",
            "kind": "float",
            "source": "auto",
            "createdAt": "2026-10-01 09:00:00",
            "updatedAt": "2026-10-01 12:00:00"
          },
          {
            "deviceId": "meter-1",
            "deviceAddressId": "voltage",
            "deviceAddressLabel": "Voltage",
            "kind": "float",
            "source": "manual",
            "createdAt": "2026-10-02 09:00:00",
            "updatedAt": "2026-10-02 09:00:00"
          }
        ]
      }
    ]
  },
  "serverTime": "2026-11-06 10:30:00"
}
```

> **Notes:**
> - **Ordered by `deviceId` ascending**; the cursor is the last `deviceId` of the previous page.
>   Pass the previous page's `nextAfter` back verbatim as `after`; do not construct it yourself.
> - **A page is split by device**: all points of a device stay on the same page; a device is never
>   cut across two pages.
> - **There are no "point-less devices"** here: devices are derived from point rows, so a device
>   without points simply does not exist. Unlike 4.5, `points` is never an empty array.
> - **`source=manual` rows** have metadata confirmed manually; later subscription messages will not
>   overwrite them. The admin UI can hand such a row back to auto maintenance when needed.
> - A traversal ends when `hasMore` is `false` (then `nextAfter` is `""`) — **not** when `records`
>   is empty.

### 4.13 Thing Model and Property Batch Upsert

Create or update thing models and their properties in batches by natural keys: models are matched by `code`,
and properties by `identifier` within their model. **Create if absent, update if present.** This endpoint only
modifies thing-model configuration inside the gateway; it never touches field devices and never deletes models
or properties that are absent from the request.

**Request**

```
POST /openApi/thingModel/batchUpsert
X-Api-Key: <AppKey>
Content-Type: application/json
```

```json
{
  "models": [
    {
      "code": "th_sensor",
      "name": "Temperature & Humidity Sensor",
      "category": "sensor",
      "version": "1.0.0",
      "description": "Workshop environment sensor",
      "status": 1,
      "properties": [
        {
          "identifier": "temperature",
          "name": "Temperature",
          "kind": "float",
          "accessMode": "R",
          "unit": "C",
          "description": "",
          "sortOrder": 10,
          "status": 1
        },
        {
          "identifier": "humidity",
          "name": "Humidity",
          "kind": "float",
          "accessMode": "R",
          "unit": "%"
        }
      ]
    }
  ]
}
```

**Request fields**

| Field | Required | Description |
| ----- | -------- | ----------- |
| `models` | Yes | 1~100 models |
| `models[].code` | Yes | Model identifier (starts with a letter; letters, digits, `_`, `-`; ≤64). **Natural key, immutable after creation** |
| `models[].name` | Yes | Display name (≤100 characters, globally unique) |
| `models[].category` | No | Category code: built-in `sensor` / `meter` / `actuator` / `robot` / `plc` / `other`, or a custom code maintained by the management platform (starts with a lowercase letter; lowercase letters, digits, `_`, `-`; ≤32). Omitted = keep current value (new models = uncategorized); explicit `""` = uncategorized |
| `models[].version` | No | ≤32 characters; omitted = keep current value (new models = `1.0.0`) |
| `models[].description` | No | ≤500 characters; omitted = keep current value |
| `models[].status` | No | `1` enabled / `0` disabled; omitted = keep current value (new models = `1`) |
| `models[].properties` | No | Properties to sync under this model, up to 200; omitted or an empty array means **leave properties unchanged** |
| `models[].properties[].identifier` | Yes | Property identifier (starts with a letter; letters, digits, `_`; ≤64). **Natural key within the model, immutable after creation** |
| `models[].properties[].name` | Yes | Property display name (≤100 characters) |
| `models[].properties[].kind` | Required on create | `bool` / `int` / `uint` / `float` / `string` / `time`; omitted on update = keep current value |
| `models[].properties[].accessMode` | Required on create | `R` / `W` / `RW`; omitted on update = keep current value |
| `models[].properties[].unit` | No | ≤32 characters; omitted = keep current value |
| `models[].properties[].description` | No | ≤500 characters; omitted = keep current value |
| `models[].properties[].sortOrder` | No | 0~99999; when omitted on create it becomes the model's current maximum +10; omitted on update = keep current value |
| `models[].properties[].status` | No | `1` enabled / `0` disabled; omitted = keep current value (new properties = `1`) |

**Semantics**

- **Optional fields**: omitted means "do not change"; an explicit empty string clears the value
  (`category=""` means uncategorized). An empty `kind` / `accessMode` on update is invalid.
- **Transaction boundary is the model**: a model's metadata and all of its properties commit in one transaction.
  If any property fails, the whole model (including metadata) rolls back; other models are unaffected — so a batch
  may partially succeed.
- **No deletion**: properties absent from the request stay untouched. This endpoint cannot delete models or properties.
- **Bound properties**: once a property is bound to a point (`device_address.property_id`), an actual change to
  `kind` / `accessMode` is rejected (`41018`). Unbind it in the admin console first.
- **Duplicates within a batch**: for a repeated `code`, the first occurrence wins and later entries fail;
  a repeated `identifier` within one model fails the entire model.
- **Name uniqueness**: model names are globally unique; a collision with another model fails that model (`41001`).
- `action=updated` means **matched and synchronized successfully**, even if the submitted values equal the stored ones.

**Response**

```json
{
  "code": "0",
  "msg": "success",
  "isSuccess": true,
  "data": {
    "total": 1,
    "success": 1,
    "failed": 0,
    "created": 1,
    "updated": 0,
    "propertiesCreated": 2,
    "propertiesUpdated": 0,
    "models": [
      {
        "code": "th_sensor",
        "name": "Temperature & Humidity Sensor",
        "action": "created",
        "id": "0b1c2d3e4f5a",
        "properties": [
          { "identifier": "temperature", "action": "created", "id": "a1b2c3" },
          { "identifier": "humidity", "action": "created", "id": "d4e5f6" }
        ]
      }
    ]
  },
  "serverTime": "2026-11-09 10:30:00"
}
```

| Field | Description |
| ----- | ----------- |
| `total` | Models in the request |
| `success` / `failed` | Models succeeded / failed |
| `created` / `updated` | Among successful models, how many were created / updated |
| `propertiesCreated` / `propertiesUpdated` | Properties actually created / updated under successful models |
| `models[]` | Per-model results, in request order |
| `models[].code` / `name` | Identifier and name from the request |
| `models[].action` | `created` / `updated` / `failed` |
| `models[].id` | Gateway-internal model ID (omitted on failure) |
| `models[].errorCode` / `errorMessage` | Failure reason (present only on failure) |
| `models[].properties[]` | Per-property results; a failed model contains at most one failed property entry |

> **Note**: a failed model or property **does not** turn the top-level response into an error envelope —
> it still returns `code=0`, `isSuccess=true`. Callers must inspect `data.failed` and
> `models[].action/errorCode`.

**Error codes**: per-item errors are listed in §5 as `41001` / `41003` / `41007` / `41008` / `41010` /
`41018` / `10001`. Malformed JSON, an empty `models`, or more than 100 models fails at the top level
with `PARAM_ERROR`.

### 4.14 Thing Model Batch Delete

Deletes thing models in batches by model identifier (`code`). **Deleting a model also deletes all of its
properties.** This endpoint only modifies configuration inside the gateway and never touches field devices.

**Request**

```
POST /openApi/thingModel/batchDelete
X-Api-Key: <AppKey>
Content-Type: application/json
```

```json
{ "codes": ["th_sensor", "th_old"] }
```

| Field | Required | Description |
| ----- | -------- | ----------- |
| `codes` | Yes | Model identifiers, 1~200 entries; trimmed and matched exactly against `code` |

**Semantics**

- **Delete protection**: if the model is bound to a device (`device.model_id`), or one of its properties is
  still bound to a point (`device_address.property_id`), that model returns `41005` and stays untouched.
- **Independent entries**: one failed model does not affect the others; the top-level response is always a
  success envelope.
- **Not found**: a missing model returns `41002` (not treated as success). Integrations that retry
  "ensure absent" may treat `41002` as already deleted.
- **Duplicate codes**: if the same `code` appears twice in one batch, the first entry deletes it and the
  later entry fails with `41002`.
- Malformed JSON, an empty `codes`, or more than 200 entries fails at the top level with `PARAM_ERROR`.

**Response**

```json
{
  "code": "0",
  "msg": "success",
  "isSuccess": true,
  "data": {
    "requested": 2,
    "deleted": 1,
    "failed": 1,
    "results": [
      { "code": "th_sensor", "action": "deleted" },
      { "code": "th_old", "action": "failed", "errorCode": "41002", "errorMessage": "thing model not found" }
    ]
  },
  "serverTime": "2026-11-09 11:00:00"
}
```

| Field | Description |
| ----- | ----------- |
| `requested` / `deleted` / `failed` | Requested entries / actually deleted / failed |
| `results[].code` | Model identifier from the request |
| `results[].action` | `deleted` / `failed` |
| `results[].errorCode` / `errorMessage` | Failure reason (present only on failure) |

### 4.15 Thing Model Property Batch Delete

Deletes thing-model properties in batches by (model identifier `modelCode` + property identifier `identifier`).
**Only the specified properties are deleted**; other properties of the same model are unaffected. This endpoint
only modifies configuration inside the gateway and never touches field devices.

**Request**

```
POST /openApi/thingModelProperty/batchDelete
X-Api-Key: <AppKey>
Content-Type: application/json
```

```json
{
  "items": [
    { "modelCode": "th_sensor", "identifier": "temperature" },
    { "modelCode": "th_sensor", "identifier": "humidity" }
  ]
}
```

| Field | Required | Description |
| ----- | -------- | ----------- |
| `items` | Yes | 1~200 entries |
| `items[].modelCode` | Yes | Identifier `code` of the owning model |
| `items[].identifier` | Yes | Property identifier |

**Semantics**

- **Delete protection**: a property bound to a point (`device_address.property_id`) returns `41006` and stays
  untouched.
- **Not found**: a missing model returns `41002`; a missing property returns `41004` (neither is success).
- **Independent entries**: one failure does not affect the others; the top-level response is always a success
  envelope.
- **Duplicate entries**: deleting the same property twice in one batch makes later entries fail with `41004`.
- Malformed JSON, an empty `items`, or more than 200 entries fails at the top level with `PARAM_ERROR`.

**Response**

```json
{
  "code": "0",
  "msg": "success",
  "isSuccess": true,
  "data": {
    "requested": 2,
    "deleted": 2,
    "failed": 0,
    "results": [
      { "modelCode": "th_sensor", "identifier": "temperature", "action": "deleted" },
      { "modelCode": "th_sensor", "identifier": "humidity", "action": "deleted" }
    ]
  },
  "serverTime": "2026-11-09 11:00:00"
}
```

| Field | Description |
| ----- | ----------- |
| `requested` / `deleted` / `failed` | Requested entries / actually deleted / failed |
| `results[].modelCode` / `identifier` | Natural key from the request |
| `results[].action` | `deleted` / `failed` |
| `results[].errorCode` / `errorMessage` | Failure reason (present only on failure) |

### 4.16 Configuration Sync (Management Platform)

> These endpoints let a management platform deploy configuration templates to the gateway.
> **They only modify gateway configuration and never touch field devices.**
> The `payload` returned by read APIs is exactly the desired state the management platform
> should store; secret fields are masked as `***`. Write APIs return per-item results while
> the top-level envelope stays successful (`code=0`); callers must inspect `items[]`.

#### 4.16.1 Capability Negotiation

```
GET /openApi/config/capabilities
```

Returns `cfgSyncVersion`, `openApiVersion`, `gatewayVersion`, `hashVersion`,
`hashAlgorithm`, and per-domain `read/upsert/delete/update/reset/maxBatch`.
The management platform must call this first; if `hashVersion` differs, drift
detection must be disabled.

#### 4.16.2 Read APIs

| Method | Path | Description |
| ------ | ---- | ----------- |
| GET | `/openApi/thingModel/page` | Thing models + properties + `contentHash` |
| GET | `/openApi/deviceConfig/page` | Devices + collector groups + points + `contentHash`; page size max 20 |
| GET | `/openApi/pushChannel/page` | Push channels, `config` masked |
| GET | `/openApi/pushChannel/types` | Channel type metadata |
| GET | `/openApi/pushChannel/forms` | Channel dynamic forms |
| GET | `/openApi/alarmWebhook/page` | Alarm webhooks, `secret` masked |
| GET | `/openApi/alarmWebhook/types` | Webhook type metadata |
| GET | `/openApi/alarmWebhook/forms` | Webhook dynamic forms |
| GET | `/openApi/systemConfig/list` | Current runtime switch values |
| GET | `/openApi/config/protocols` | Protocols and point data types |

Paginated endpoints use `page/size` and return `{page,size,total,records}`; each
record is `{targetKey,payload,contentHash}`.

#### 4.16.3 Write APIs

| Method | Path | Natural key | Description |
| ------ | ---- | ----------- | ----------- |
| POST | `/openApi/thingModel/batchUpsert` | `code` / `identifier` | Existing API; optional `replaceProperties=true` deletes absent properties |
| POST | `/openApi/thingModel/batchDelete` | `code` | Existing API |
| POST | `/openApi/pushChannel/batchUpsert` | `type/name` | `config` is an object; `"***"` keeps the existing secret |
| POST | `/openApi/pushChannel/batchDelete` | `type/name` | Per-item delete |
| POST | `/openApi/alarmWebhook/batchUpsert` | `name` | `secret` `"***"` keeps the existing value |
| POST | `/openApi/alarmWebhook/batchDelete` | `name` | Per-item delete |
| POST | `/openApi/systemConfig/batchUpdate` | `key` | Batch update runtime switches |
| POST | `/openApi/systemConfig/batchReset` | `key` | Batch reset to defaults |
| POST | `/openApi/deviceConfig/batchUpsert` | `code` or `name` | Device + groups + points; `mode=replace` deletes absent points/groups |
| POST | `/openApi/deviceConfig/batchDelete` | `code` or `name` | Per-item delete, preserving trigger-rule / push-channel reference guards |

Batch limits: thing models / push channels / alarm webhooks 100, runtime switches 50,
devices 20. Per device: at most 500 points and 100 collector groups.

#### 4.16.4 contentHash and Secret Retention

- Algorithm: `sha256-canonical-json-v1`. Object keys are sorted, numbers normalized,
  HTML not escaped; secret fields (`password/secret/token/apiKey/...`) contribute only
  their presence, not their value.
- Secret placeholder `"***"`: on write it means "keep the gateway's existing value"
  (supported in nested `config`/`protocolJson` paths and aligned array indexes).
  Read APIs never return real secrets.
- Therefore "import → re-deploy" will not overwrite gateway secrets with `***`.
- Known limitation: changing only a secret value does not produce drift.

#### 4.16.5 Apply Order

The management platform applies in this order: `runtime switches → push channels →
alarm webhooks → thing models → devices/points`. Devices and points depend on thing
models and protocols; reversing the order fails on missing references.

#### 4.16.6 Error Codes

`42001-42009` are configuration-sync specific: unsupported type, protocol missing,
thing model missing/disabled, model change missing points, protocol change missing
all points, collector group missing, unsupported hash version, batch limit exceeded,
invalid device configuration. Per-item `errorCode` reflects the gateway's actual result.

## 5. Error Code Reference

The error codes the public API may return are listed below (the complete list is in this section; other internal error codes never appear in public APIs):

| code | HTTP | Meaning | Recommended Action |
| ---- | ---- | ------- | ------------------ |
| `0` | 200 | Success | - |
| `70003` | 401 | Public API key is empty | The `X-Api-Key` header is missing; check the request construction |
| `70004` | 401 | Public API key invalid | Key is wrong or has been deleted; contact the administrator |
| `PARAM_ERROR` | 200 | Parameter validation failed | Fix the request parameters per msg (msg looks like `参数校验失败: size max`) |
| `40031` | 200 | Control writes are disabled | Ask the administrator to check `control.enabled` |
| `40032` | 200 | Protocol does not support control writes | The plugin does not declare `Writable`; read-only |
| `40033` | 200 | Point is not writable | `rw_permission` must be `W` or `RW` |
| `40034` | 200 | Point not found or not on this device | Check `deviceAddressId` and `deviceId` |
| `40035` | 200 | Device is not collecting or is offline | Retry after the device is online |
| `40036` | 200 | Invalid control write parameters | Fix per msg (duplicate point, >50 items, bad time format, …) |
| `40037` | 200 | Control write failed (link error, state unknown) | Read back / verify on site before retrying with a new `requestId` |
| `40038` | 200 | Control writes are too frequent | Back off per the device/point rate limit and retry |
| `40039` | 200 | The thing-model property bound to this point is read-only | The bound property's `access_mode` must be `W` or `RW`; change it or unbind, then retry |
| `10001` | 200 | Thing-model/property entry validation failed (per item) | Fix the name, enum value, length or count limit per `models[].errorMessage` |
| `41001` | 200 | Thing-model name already exists, or duplicate `code` in a batch (per item) | Model names are globally unique — rename, or fix the duplicate `code` |
| `41003` | 200 | Duplicate property identifier in a batch (per item) | The same `identifier` may appear only once per model per batch |
| `41002` | 200 | Thing model not found (per item) | It may already be deleted; treat it as deleted or check `code` |
| `41004` | 200 | Thing-model property not found (per item) | It may already be deleted; check `modelCode` and `identifier` |
| `41005` | 200 | Thing model is bound to a device, or its properties are bound to points (per item) | Unbind the device/points first, or keep the model |
| `41006` | 200 | Property is bound to a point and cannot be deleted (per item) | Unbind the point first, then delete |
| `41007` | 200 | Invalid thing-model code (per item) | Starts with a letter; letters, digits, `_`, `-`; ≤64 |
| `41008` | 200 | Invalid property identifier (per item) | Starts with a letter; letters, digits, `_`; ≤64 |
| `41010` | 200 | Invalid thing-model category format (per item) | Empty, or a code that starts with a lowercase letter, contains only lowercase letters/digits/`_`/`-`, and is ≤32 characters (built-in `sensor/meter/actuator/robot/plc/other` also valid) |
| `41018` | 200 | Property is bound to a point and cannot change data type or access mode (per item) | Unbind the point in the admin console first, then change `kind`/`accessMode` |
| `-1` | 200 | System error | Retry later; if persistent, contact the administrator and check gateway logs |

## 6. FAQ

**Q: The request returns 401 but my key is correct?**
Troubleshooting order: ① Confirm the header name is `X-Api-Key` (HTTP header names are case-insensitive — `x-api-key` works too — but the header must be present); ② Confirm the key value matches the one issued — **the key value itself is case-sensitive**, beware of stray whitespace when copying; ③ Confirm the key has not been deleted or regenerated by the administrator. If it still fails, contact the administrator.

**Q: How do I read the current real-time value of an address?**
The public API currently only provides queries at the configuration level (devices and address definitions). For **real-time data**, subscribe via one of the gateway's push channels (e.g. MQTT), or contact the administrator to extend the public API.

**Q: How long until address configuration changes take effect?**
After the admin console modifies device/address configuration, the gateway hot-reloads it within about 10 seconds; subsequent queries return the latest configuration.
