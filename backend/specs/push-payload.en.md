# Push Payload Contract

[English](push-payload.en.md) | [简体中文](push-payload.md)

> This spec defines the field semantics and the **self-describing parsing conventions**
> for the `value` field of payloads pushed to external message channels (MQTT /
> HTTP-Webhook). It covers both `native-json` (§1 / §2) and `group-json` (§1.1);
> external consumers should parse according to this contract and must not depend on
> gateway internals.

## 0. Payload formats

Message-based push channels now support four payload formats, selected by the channel config's `format`:

- `native-json` (default, may be omitted): the contract described in §1 / §2. Enabling format selection **does not change** its fields, order, or value semantics.
- `group-json`: a self-describing frame per (device, collector group), with device and group identity at the frame header and points in a `points` array (§1.1). Point field names match `native-json`, and the `value` parsing rules in §3 / §4 apply equally.
- `mapping-json`: emits a customer-defined JSON payload from an outbound mapping. See [push-channels.md](../docs/push-channels.md).
- `script`: emits arbitrary text produced by a goja script (a string for one message, a string[] for several). See [push-channels.md](../docs/push-channels.md).

Channels using `mapping-json` or `script` are not described by the field table below; downstream systems should parse according to that channel's own mapping or script contract.

---

## 1. native-json payload structure

Message-based channels push **per collection batch** (a batch is one (device, collector group)); the `native-json` payload is a JSON array where each element = one point:

```json
[
  {
    "deviceId": "019fabd15a9176dda844147d98d38f9b",
    "deviceCode": "ac-1-code",
    "deviceName": "空调-modbus-tcp",
    "deviceAddressId": "019fb1137da076a4ac97af5e51f2e22c",
    "deviceAddressName": "411115",
    "value": "0",
    "protocol": "ModBus.TCP",
    "dataType": "word",
    "kind": "uint",
    "quality": 192,
    "collectedAt": "2026-08-19 16:39:47.864",
    "thingModelCode": "th_ac",
    "propertyCode": "temperature"
  }
]
```

> **Delivery cadence (change-only push)**: `push.changeOnly` ships as true. When
> `value`, `quality` and the thing-model/property codes of a point are all unchanged since its last **actual delivery**, the
> point is omitted from this round — downstream must not treat "a record arrived" as a
> device heartbeat (use device-disconnection alarms for liveness). The first round after
> gateway startup, or after a push channel is added/reconfigured, is always a full
> snapshot; a quality transition (e.g. 192 → 2) counts as a change too. Set the switch to
> false when evenly spaced samples are required (switchable on the Runtime Switches page;
> it applies from the next collection batch).

### 1.1 group-json frame structure

`group-json` hoists device- and group-level information into the frame header and puts
all points of the group in `points`. One message = one collection snapshot of a
(device, collector group):

```json
{
  "collectedAt": "2026-11-02 20:00:00.123",
  "deviceId": "019fabd15a9176dda844147d98d38f9b",
  "deviceCode": "ac-1-code",
  "deviceName": "空调-modbus-tcp",
  "protocol": "ModBus.TCP",
  "thingModelCode": "th_ac",
  "groupId": "019fb110000000000000000000000001",
  "groupCode": "fast",
  "groupName": "fast",
  "groupIntervalMs": 500,
  "points": [
    {
      "deviceAddressId": "019fb1137da076a4ac97af5e51f2e22c",
      "deviceAddressName": "411115",
      "value": "0",
      "dataType": "word",
      "kind": "uint",
      "quality": 192,
      "propertyCode": "temperature"
    },
    {
      "deviceAddressId": "019fb1137da076a4ac97af5e51f2e23d",
      "deviceAddressName": "411116",
      "value": "",
      "dataType": "word",
      "kind": "uint",
      "quality": 2,
      "propertyCode": ""
    }
  ]
}
```

Frame header fields:

| Field | Type | Description |
|------|------|------|
| `collectedAt` | string | Same as `collectedAt` in §2; **frame-level**, shared by all points in the frame |
| `deviceId` / `deviceCode` / `deviceName` | string | Same as §2; present once per frame, not repeated per point |
| `protocol` | string | Same as §2; a batch contains one device only, so this is frame-level. `dataType` remains a per-point field |
| `thingModelCode` | string | Same as §2; device-level, present once |
| `groupId` | string | Collector group internal key; empty string when ungrouped |
| `groupCode` | string | Collector group code (`collector_group.code`), immutable after creation; **downstream should use it to correlate business data**. Empty string when ungrouped |
| `groupName` | string | Collector group display name; empty string when ungrouped |
| `groupIntervalMs` | int | Group collection interval (milliseconds); 0 when ungrouped |
| `points` | array | All points of the group, see below |
| `trigger` | object | Same as §2.1; **frame-level** (all records in the batch share the same trigger), present only for event-triggered pushes, `omitempty` |

Each `points[]` element uses exactly the names and semantics of §2 (`deviceAddressId` /
`deviceAddressName` / `value` / `dataType` / `kind` / `quality` / `propertyCode`) and
is not repeated here; parse `value` by §3, and `protocol` is still the namespace of
`dataType` (§4).

**Bad points are not dropped**: `points` contains every point of the group, including
points with `quality < 128` (whose `value` is usually an empty string). **Test for
"bad" with `quality < 128`, never with an empty `value`** — an empty string can be a
legitimate text value.

Delivery cadence follows the change-only rule in §1: unchanged points are omitted from
`points`, and no message is emitted when every point in the frame is unchanged. A frame
may therefore contain only part of the group; downstream must not assume a full
snapshot. Except for `trigger`, fields are always present and fall back to an empty
string or 0; read by field name, not by position. Topic templates and `groupIds`
subscription filtering are covered in [push-channels.md §2.1](../docs/push-channels.md).

## 2. Field semantics

| Field | Type | Description |
|------|------|------|
| `deviceId` / `deviceName` | string | Device identity and name |
| `deviceCode` | string | Device code (`device.code`); empty string for legacy data without a code. Immutable after creation, so it is a stable cross-environment key. Read it by field name, not by position |
| `deviceAddressId` / `deviceAddressName` | string | Point identity and address name (e.g. `411115`) |
| `value` | string | **Already-decoded** collected value (protocol, byte order and word order resolved on the gateway) |
| `protocol` | string | Protocol name (`iot_protocol.name`, e.g. `ModBus.TCP`); the namespace identifier, see §4 |
| `dataType` | string | The protocol's **internal data type name** (e.g. `word`, `int16`); combined with `protocol` it pins down the exact PLC type, see §4 |
| `kind` | string | Data-kind category (one of the 6 kinds); the **single source of truth** for parsing `value` |
| `quality` | int | Collection quality code (`driver.QualityXxx`; class in the top 2 bits, cause in the low 6): 192 = good, 128 = uncertain but usable, 0 = bad/unclassified, 1 = bad·address, 2 = bad·comms, 3 = bad·decode. **Test for "bad" with `quality < 128`, not `quality == 0`** (a bad code is written when a ranged read fails, with an empty `value`) |
| `collectedAt` | string | Collection time, format `2006-01-02 15:04:05.000` (millisecond precision), **local time zone** |
| `thingModelCode` | string | Thing model code bound to the device (`thing_model.code`); empty string when unbound. Immutable after creation, so it is a stable dimension |
| `propertyCode` | string | Thing model property identifier bound to the point (`thing_model_property.identifier`); empty string when unbound. Binding validates that the property belongs to the device's model |

`deviceCode` follows `deviceId`; `thingModelCode` / `propertyCode` are **appended fields**
(at the end of the JSON). All three are always present and fall back to an empty string
when missing; the names, relative order and values of existing fields are unchanged.
Records produced by external MQTT subscriptions have no local binding, so these fields are
empty there too.
The TSDB channels (TDengine / InfluxDB) write both as tags (`thing_model_code` / `property_code`).

## 3. Parsing `value`: dispatch by `kind`

`kind` is a `driver.KindXxx` value, always one of the following 6 kinds. External consumers should dispatch parsing on it, **not** on `dataType` (dataType is already normalized to internal names, but one kind may map to many internal types — `uint8`/`uint16`/`word` are all `uint` and parse the same way).

| kind | Meaning | Parsing |
|------|------|----------|
| `bool` | Boolean | String `"0"`/`"1"` |
| `int` | Signed integer | Decimal integer (`strconv.ParseInt`), may carry a leading `-` |
| `uint` | Unsigned integer | Decimal integer (`strconv.ParseUint`), never negative |
| `float` | Floating point | Decimal, **shortest round-trip representation** (see §5) |
| `string` | Text | Verbatim string (trailing `\x00` stripped) |
| `time` | Date/time | Protocol-dependent ISO-8601 or date/time string (see §6) |

For unknown/empty `kind`, fall back to numeric inference: integer → float → text (same as the TSDB channels).

## 4. `protocol` + `dataType`

- `protocol` is the protocol name (`iot_protocol.name`, e.g. `ModBus.TCP`, `Siemens.S7`) — the **namespace** of the type; `dataType` is the **internal type name** within that protocol scope (`word`, `int16`, `uint8`...), taken from the address config's `data_type` field and already normalized (`Word`/`WORD`/`word` all land as `word`).
- Together they pin down the exact PLC type: the same internal name means different things under different protocols (e.g. `byte` → Modbus `uint8` vs. S7 `byte`); `protocol` resolves cross-protocol name collisions.
- **External consumers parse `value` by `kind`** (see §3); `protocol`+`dataType` are for exact type recovery and aggregation/analytics.

## 5. Float representation (lossless round-trip)

`float`/`uint`/`int` strings are produced by `strconv.FormatFloat(v, 'f', -1, bits)`:

- **Fixed notation**: exponents never appear (no `1.23e+08`);
- **Shortest round-trip**: the string restores the decoded float/integer value losslessly (32 bits for float32, 64 for float64).
- Examples: `25.5000 → "25.5"`, float32 π → `"3.1415927"`.
- Float precision is therefore **lossless at the string level**; parse as a number to recover the original precision. Do not assume a fixed number of decimal places.

## 6. Time types (`kind=time`)

| Protocol | Type | value format | Example |
|------|------|-----------|------|
| Modbus / FINS | `date` | RFC3339, UTC | `2026-08-19T08:39:47Z` |
| S7 | `date` | `YYYY-MM-DD` | `2026-08-19` |
| S7 | `tod` | `HH:MM:SS` | `16:39:47` |
| S7 | `dt` | `YYYY-MM-DD HH:MM:SS` | `2026-08-19 16:39:47` |

- Modbus/FINS `date` has raw semantics of **Unix seconds** (driver decoding convention), normalized to UTC RFC3339 on output, so consumers don't need to agree on units; convert from ISO when an epoch is needed.
- S7 date/tod/dt are output in human-readable form **without a time zone** (S7 decodes to local time); consume as text.

## 7. Known contract gaps (deliberately not addressed by this spec)

- `collectedAt` is a **local-time string with no time-zone marker**. When multiple gateways are deployed across time zones, downstream time sorting/comparison may misalign; the TSDB channels parse it to epoch with `time.Local` on the gateway side, so they are unaffected. If the ambiguity needs removal, `collectedAt` will later be normalized to timezone-aware RFC3339 (which also changes TDengine/InfluxDB timestamp parsing).
- Engineering-unit scaling (scale/offset) is outside the acquisition scope and not included in the payload; extend via point configuration when needed.
