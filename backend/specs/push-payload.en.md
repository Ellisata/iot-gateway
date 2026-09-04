# Push Payload Contract

[English](push-payload.en.md) | [简体中文](push-payload.md)

> This spec defines the field semantics of each point-record JSON and the **self-describing parsing conventions** for the `value` field when collected data is pushed to external channels (currently MQTT). External consumers should parse according to this contract and must not depend on gateway internals.

---

## 1. Payload structure

The MQTT channel pushes grouped **per device**; the payload is a JSON array where each element = one point:

```json
[
  {
    "deviceId": "019fabd15a9176dda844147d98d38f9b",
    "deviceName": "空调-modbus-tcp",
    "deviceAddressId": "019fb1137da076a4ac97af5e51f2e22c",
    "deviceAddressName": "411115",
    "value": "0",
    "protocol": "ModBus.TCP",
    "dataType": "word",
    "kind": "uint",
    "quality": 192,
    "collectedAt": "2026-08-19 16:39:47.864"
  }
]
```

## 2. Field semantics

| Field | Type | Description |
|------|------|------|
| `deviceId` / `deviceName` | string | Device identity and name |
| `deviceAddressId` / `deviceAddressName` | string | Point identity and address name (e.g. `411115`) |
| `value` | string | **Already-decoded** collected value (protocol, byte order and word order resolved on the gateway) |
| `protocol` | string | Protocol name (`iot_protocol.name`, e.g. `ModBus.TCP`); the namespace identifier, see §4 |
| `dataType` | string | The protocol's **internal data type name** (e.g. `word`, `int16`); combined with `protocol` it pins down the exact PLC type, see §4 |
| `kind` | string | Data-kind category (one of the 6 kinds); the **single source of truth** for parsing `value` |
| `quality` | int | 192 = good, 0 = bad (set to 0 when a ranged read fails, with an empty `value`) |
| `collectedAt` | string | Collection time, format `2006-01-02 15:04:05.000` (millisecond precision), **local time zone** |

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

- `protocol` is the protocol name (`iot_protocol.name`, e.g. `ModBus.TCP`, `Siemens.Net.S7`) — the **namespace** of the type; `dataType` is the **internal type name** within that protocol scope (`word`, `int16`, `uint8`...), taken from the address config's `data_type` field and already normalized (`Word`/`WORD`/`word` all land as `word`).
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
