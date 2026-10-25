# Licensing

The project is split into three repositories with intentionally different
licenses:

| Repository | License | Contents |
|---|---|---|
| [`iot-gateway`](https://github.com/Ellisata/iot-gateway) | **AGPL-3.0-only** | Gateway core: host, collector, push engine, alarms, web console, Open API. |
| [`iot-gateway-sdk`](https://github.com/Ellisata/iot-gateway-sdk) | **Apache-2.0** | Driver SDK: contract, wire protocol, plugin serve loop, examples. |
| [`iot-gateway-drivers`](https://github.com/Ellisata/iot-gateway-drivers) | **AGPL-3.0-only** | Official protocol drivers. Commercial/OEM licensing available. |

In this repository, the SDK and the official drivers are **Git submodules**:

| Path | Points to | License |
|---|---|---|
| `backend/sdk/` | `iot-gateway-sdk` | Apache-2.0 |
| `backend/driversrc/` | `iot-gateway-drivers` | AGPL-3.0-only |
| the rest of `backend/` and `frontend/` | this repository | AGPL-3.0-only |

## The process boundary

A driver built with the SDK is a **separate executable**. It talks to the
gateway over a documented, length-prefixed wire protocol on stdin/stdout. It
does not link or import the AGPL gateway core. The SDK itself has no dependency
on the core; its CI enforces this with a dependency-direction check.

This is what makes the following statement accurate: **a proprietary driver
built with the SDK is an independent program, not a derivative work of the
AGPL gateway.** It does not by itself trigger AGPL obligations.

The official drivers are AGPL-3.0-only, not Apache-2.0. They remain part of the
commercial/OEM offering; third parties may write their own drivers under any
license using the Apache-2.0 SDK.

## Contributions

| Area | Policy |
|---|---|
| `iot-gateway-sdk` | **Apache-2.0 + DCO** (`git commit -s`); no CLA. |
| `iot-gateway-drivers` | **AGPL-3.0-only + CLA** (commercial dual-licensing requires the CLA). |
| `iot-gateway` core | **AGPL-3.0-only + CLA**. |

See the `CONTRIBUTING.md` in each repository. The top-level `CONTRIBUTING.md`
in this repository covers the core and points to the other two.

## Not legal advice

This document explains the project's intent and engineering boundary. It is
not legal advice. If you embed the gateway or ship drivers commercially, have
your counsel review your specific distribution model.
