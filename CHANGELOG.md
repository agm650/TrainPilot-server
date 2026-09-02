# Changelog

All notable changes to this project will be documented in this file.

## Unreleased

### Fixed

- N/A

### Added

- N/A

### Changed

- N/A

## Unreleased

### Fixed

- WebSocket snapshots now use the current event bus sequence instead of a constant value.
- Client WebSocket heartbeats no longer consume server event sequence numbers.
- Emergency stops, track power cuts, and zero-speed commands now preempt queued throttle or function commands.
- Driving commands are explicitly inhibited while the emergency stop remains active or track power is off or unknown.
- A lease heartbeat sent exactly at its expiration time can no longer reactivate an expired lease.
- Revoked tokens are distinguished from naturally expired tokens without exposing internal details.
- Old or duplicate WebSocket events are filtered without losing events published while a snapshot is being generated.
- Slow WebSocket clients are disconnected when their queue overflows or a write deadline expires.
- Layout imports now atomically reject changes to or deletion of a `pending` turnout, as well as sharing one accessory address across multiple turnouts.
- The Unix-socket administration integration test now uses a short temporary path compatible with the macOS socket path limit.
- Late accessory feedback can no longer restore a stale `pending` command state after a turnout command has failed or timed out.

### Added

- Typed station contract for binary DCC accessories, portable linear-address range validation, and a generic provider for qualified feedback.
- z21 `LAN_X_SET_TURNOUT` accessory commands, configurable pulse and safe deactivation, correlated `LAN_X_GET_TURNOUT_INFO` queries, and state broadcasts without invented positions.
- DCC-EX accessories aligned with `<a linear 0|1>`, including portable-range validation, `assumed` feedback, concurrent TCP tests, and no replay after reconnection.
- Simple and compound turnout model with binary endpoints, explicit logical positions, inversion, physical-state resolution, SQLite migration, and backward-compatible archives.
- Persistent authentication with revocable access and refresh tokens.
- User administration through a local Unix socket only.
- Locomotive CRUD with DCC address validation and role-based access control.
- Exclusive control leases with heartbeats, expiration, and a controlled stop before release.
- Track power, emergency stop, speed, and function control.
- Blocks, turnouts, routes, and feedback-to-block mappings.
- Simulator, Z21 UDP, and DCC-EX TCP drivers behind a common abstraction.
- Z21 `online`, `degraded`, and `offline` states, with active commands rejected while offline.
- Versioned rolling-stock and layout import/export using ZIP archives.
- `dccctl` and `dcc-api-conformance` tools.
- OpenAPI and AsyncAPI contracts, plus reusable client contract scenarios.
- WebSocket snapshot on connection and resynchronization through `client.snapshot_request`.
- CGO-free macOS/Linux builds and GoReleaser v2 archives.
- Example systemd service and Linux configuration.
- Complete session-filtered WebSocket snapshots, with sequence-gap, resynchronization, reconnection, and token-expiration tests.
- Stable error categories and codes for authentication, authorization, validation, safety, and command-station unavailability.
- Verified inventory of public endpoints and passive, active, and configuration-mutation conformance scenarios.
- Separate compatibility and migration policies for HTTP and WebSocket.
- Public `locomotiveControlStates` snapshot state to distinguish free locomotives from locomotives controlled by the current session, another session of the same user, or another user.
- Explicit lease takeover endpoint between sessions of the same user, with a zero-speed stop, atomic lease transfer, and `locomotive.control.transferred` event.
- Deterministic simulator foundation with explicit state, injectable clock, deeply copied snapshots, accessory introspection, and reset without disconnection.
- Deterministic accessory simulation with desired and reported states, immediate or delayed confirmation, missing confirmation, and inconsistent feedback.
- Binary `position1`/`position2` endpoint simulation, qualified position events, external reports, address-specific faults, and simple, three-way, double-slip, partial-failure, and stale-confirmation scenarios.
- Injectable simulator electrical telemetry covering currents, voltages, temperature, programming mode, power loss, overheating, and short circuits.
- Deterministic injection of `online/degraded/offline` connectivity, context-aware delays, and operation-specific errors with occurrence limits in the simulator.
- Simulated feedback with observable physical state, repeated events, deterministic bouncing, intentional event loss, explicit saturation, and multi-block integration.
- Strict and deterministic JSON v1 scenario engine with manual advancement without real sleeps, cancelable real-time execution, observable control state, and versioned reference scenarios.
- Authenticated simulator test API for snapshots, reset, connectivity, telemetry, feedback, accessories, faults, and scenarios, entirely absent with hardware drivers or when `testAPI=false`.
- Twelve reference simulator scenarios executed in logical time by the HTTP/WebSocket integration suite and CI, covering no replay after an outage, telemetry, feedback, and accessory confirmation.
- Generic publication of simulator-injected status changes through `station.StatusEventProvider`.
- Comprehensive virtual test-bench guide for client development, with HTTP/WebSocket examples, scenarios, and PlantUML diagrams, included in release archives.
- Turnout controller with safe multi-endpoint transitions, qualified confirmation, configurable timeout, per-device serialization, partial-failure handling, and external changes.
- REST commands using `position`, compound state in snapshots, stable error codes, and `dccctl turnouts` / `dccctl turnout` commands.
- Common accessory conformance suite for Simulator, z21, and DCC-EX, with simple and compound fixtures, a capability matrix, and opt-in external control through `--check-turnouts`.
- Reproducible AIG-009 hardware campaign with a protected interactive script, dry-run mode, reconnection tests without replay, and z21/DCC-EX report templates.
- AIG-010 review of motor types, compound devices, confirmations, unknown state after restart, and explicitly unsupported equipment, with representability tests.

### Changed

- Drivers now receive `position1` or `position2` through `SetBasicAccessory`, without geometric `straight`/`diverging` strings.
- The OpenAPI contract is now version `1.7.0` and AsyncAPI is now `1.9.0`. Turnouts expose `reportQuality`, use `position` for commands, and retain the `turnout.commanded`, `turnout.state.changed`, and `turnout.command.failed` events.
- Layout archives are now version 3 and separate turnout configuration from runtime state.
- SQLite now uses the pure-Go `modernc.org/sqlite` driver.
- A valid throttle or function command now renews the control lease.
- The WebSocket snapshot now includes command-station capabilities and current status.
- The OpenAPI contract is now version `1.4.0` and documents the takeover endpoint and stable error codes.
- The AsyncAPI contract is now version `1.6.0` and describes every event payload, complete resynchronization, ownership in `system.snapshot`, and `locomotive.control.transferred`.
- Command-station capabilities now expose `maxFunctionNumber`; `functions` remains the number of functions for compatibility.
- A WebSocket connection now expires with the access token used to open it and closes after session revocation; closing it does not automatically release leases.

## v0.0.1

### Changed

- Initial version
- Z21 interface mostly working. RBUS still not integrated
- Train driving is working through command line
