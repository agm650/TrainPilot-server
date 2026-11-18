# Changelog

All notable changes to this project will be documented in this file.

## Unreleased

Planned server release: 2.0.0.

### Added

- SIG-002 manufacturer-independent signal definitions and configurable
  profiles, with semantic aspect roles, multiple targets, up to nine lights,
  dark/combined indications and a representation for future flashing aspects.
  Includes an isolated SNCF BAL preset, explicit ruleset selection, validated
  DCC aspect mappings and runtime initialization without physical confirmation.
- SIG-001 signalling specification for the 2.0.0 work: explicit downstream
  signals or terminal limits, configurable signalling profiles and semantic
  roles, separate logical and reported states, and manufacturer-independent
  DCC extended-accessory aspect mappings. The ATtiny decoder is the initial
  test bench. Includes compatibility planning and acceptance criteria. ABC braking
  remains a separate future hardware study.

## v1.1.1 - 2026-11-18

Documentation-only release; no functional changes from v1.1.0.

### Fixed

- Move the changes shipped in v1.1.0 out of `Unreleased` into a dated release
  section so distribution archives include the corrected release history.

## v1.1.0 - 2026-11-18

Physical railway topology, integrated z21 R-BUS block occupancy, and momentary
locomotive functions.

### Added

- Per-locomotive function definitions with a `momentary` setting, defaulting
  to `false`. Momentary functions stay active while pressed and turn off on
  release. Missing definitions preserve existing settings on update.
- Server-side tracking of held momentary functions, with OFF commands after
  client disconnection, lease release, expiration or takeover, and session
  invalidation. Failed OFF commands are retained for retry.
- Physical railway topology with validated nodes, track sections, turnout
  ports and conditional connections, normalized SQLite persistence, and
  deterministic export.
- Indexed static topology graph with connected components, cycles and
  structural diagnostics, plus conservative active views based on confirmed
  reported turnout positions.
- Topology queries and deterministic pathfinding with oriented traversals,
  turnout requirements, and exclusions for sections, turnouts and blocks.
- Authenticated topology REST reads, revision-based WebSocket synchronization,
  and `dccctl topology` inspection and validation commands.
- Physical block membership for track sections and turnouts, with ownership
  and connectivity validation kept separate from detector mappings.
- Optional entry and exit nodes on route definitions, physical-path validation,
  and diagnostics for inconsistent routes or undeclared shared resources.
- Integrated z21 R-BUS occupancy through persisted provider, sensor and block
  mappings, with explicit `unknown`, `free` and `occupied` states.
- Conservative multi-source occupancy aggregation, including source priority,
  required observations, freshness and command-station availability. REST,
  WebSocket snapshots and change events expose the same aggregated state.
- Authenticated single and batch external occupancy observations through the
  restricted `sensor` role, and per-source diagnostics for dispatchers.
- Graphical layout presentation stored and imported atomically, with an
  authenticated read API and a revision independent from physical topology.
- Administrator-only layout archive dry-run validation with structured
  diagnostics and rollback before publication.
- Editor diagnostics for turnout endpoint vectors, duplicate addresses,
  simple-turnout shape and topology position mismatches.
- Optional feedback logging to STDOUT through `--log-feedback` or
  `diagnostics.logFeedback`. R-BUS logs report state transitions and the
  complete sorted list of occupied addresses, including unmapped detectors.
- Linux `386` release archives alongside the existing macOS and Linux
  `amd64` and `arm64` targets.

### Changed

- The HTTP contract advances from `1.7.0` to `1.15.0`, and the event
  contract from `1.9.1` to `1.13.0`, including locomotive function definitions.
- Layout archives advance to version 8, including physical topology, block
  resource membership, occupancy provider configuration, graphical
  presentation and provider track-power dependencies. Versions 1 through 7
  remain importable.
- Rolling-stock archives advance to version 7 to retain function definitions,
  with support for importing older libraries.
- Runtime occupancy observations are excluded from layout archives and are
  not restored after a server restart.
- Legacy feedback mappings migrate to the generic occupancy provider and
  sensor model. Configuration alone never implies that a block is free.
- Route reservation and activation reject both occupied blocks and blocks
  with unknown occupancy. Activation rechecks occupancy before turnout commands.
- WebSocket snapshots expose `topologyRevision` and
  `layoutPresentationRevision` so clients can refresh the corresponding
  definitions independently.

### Fixed

- R-BUS recovery integration tests select scan requests for the renewed UDP
  endpoint, ignoring startup retries left queued on slower CI runners. Both
  network and track-power recovery tests now exercise this case explicitly.
- RBUS-003: declare provider track-power dependencies, invalidate dependent
  observations during OFF, unknown power or electrical faults, and acquire
  fresh sensor states on recovery. Power generations reject queued old reports
  while preserving independent providers. z21 and DCC-EX refresh sensor state
  without replaying driving commands; source diagnostics expose the dependency.
- RBUS-002: apply each 80-input R-BUS frame as one occupancy batch, ignoring
  unmapped inputs while preserving explicit-source and wildcard precedence.
  Same-block transfers no longer publish a false free state, and transition
  logs report the final occupied-address set for the frame.
- RBUS-001: invalidate station occupancy from `degraded`, fence queued feedback
  by generation, and reacquire both R-BUS groups after network loss using a
  renewed UDP endpoint. Wildcard mappings are included and partial replies
  never restore unobserved sensors.
- Server startup explicitly queries both R-BUS groups, covering inputs 1
  through 160. Already occupied detectors update blocks and connected clients
  without requiring a new physical transition.
- Missing R-BUS group replies are retried. The driver retains a complete
  startup snapshot and no longer discards observations when its feedback
  queue fills.
- Empty locomotive lists serialize as `[]` instead of `null`, allowing
  clients to connect to a server with an empty rolling-stock database.
- Benchmark feedback injections start from the simulator's current sensor
  states, are serialized per sensor, and advance local state only after
  success, preventing false missing-event failures across successive runs
  or when concurrent requests are reordered or rejected.

## v1.0.0 - 2026-09-28

### Fixed

- Benchmark profiles can set a dedicated login timeout without relaxing
  command and WebSocket deadlines.
- Simulator scenarios that miss the measurement deadline now fail explicitly instead of producing an empty summary.

- Concurrent event publishers now preserve sequence order for every subscriber.
- Benchmark resynchronization snapshots supersede pending action expectations
  that can no longer be proven by an incremental event.
- Benchmark reports retain bounded, sanitized classifications for unexpected
  operation errors without storing raw messages or credentials.
- WebSocket snapshots now use the current event bus sequence instead of a constant value.
- Client WebSocket heartbeats no longer consume server event sequence numbers.
- Emergency stops, track power cuts, and zero-speed commands now preempt queued throttle or function commands.
- Driving commands are explicitly inhibited while the emergency stop remains active or track power is off or unknown.
- A lease heartbeat sent exactly at its expiration time can no longer reactivate an expired lease.
- Revoked tokens are distinguished from naturally expired tokens without exposing internal details.
- Old or duplicate WebSocket events are filtered without losing events published while a snapshot is being generated.
- WebSocket queue overflows now evict the oldest queued event and preserve the
  newest one, allowing resynchronization on the same connection. A write
  deadline still closes the connection.
- Layout imports now atomically reject changes to or deletion of a `pending` turnout, as well as sharing one accessory address across multiple turnouts.
- The Unix-socket administration integration test now uses a short temporary path compatible with the macOS socket path limit.
- Late accessory feedback can no longer restore a stale `pending` command state after a turnout command has failed or timed out.
- Accessory feedback now loads only the turnout associated with its address, avoiding quadratic SQLite work under concurrent reports.
- Multi-driver accessory contract tests now use a load-tolerant confirmation deadline on CI runners.
- Deterministic fixture tests now compare logical ZIP entries instead of
  platform-dependent compressed bytes.
- The 50-WebSocket benchmark integration test now scales its scenario windows
  and operation timeout under the race detector.
- Long-running benchmarks now refresh virtual-user sessions before access-token
  expiry and recover authenticated WebSocket handshakes with bounded retry.

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
- Strict and deterministic JSON v2 scenario engine, with backward-compatible
  v1 loading, manual advancement without real sleeps, cancelable real-time
  execution, observable control state, and versioned reference scenarios.
- Authenticated simulator test API for snapshots, reset, connectivity, telemetry, feedback, accessories, faults, and scenarios, entirely absent with hardware drivers or when `testAPI=false`.
- Twelve reference simulator scenarios executed in logical time by the HTTP/WebSocket integration suite and CI, covering no replay after an outage, telemetry, feedback, and accessory confirmation.
- Generic publication of simulator-injected status changes through `station.StatusEventProvider`.
- Comprehensive virtual test-bench guide for client development, with HTTP/WebSocket examples, scenarios, and PlantUML diagrams, included in release archives.
- Turnout controller with safe multi-endpoint transitions, qualified confirmation, configurable timeout, per-device serialization, partial-failure handling, and external changes.
- REST commands using `position`, compound state in snapshots, stable error codes, and `dccctl turnouts` / `dccctl turnout` commands.
- Common accessory conformance suite for Simulator, z21, and DCC-EX, with simple and compound fixtures, a capability matrix, and opt-in external control through `--check-turnouts`.
- Reproducible AIG-009 hardware campaign with a protected interactive script, dry-run mode, reconnection tests without replay, and z21/DCC-EX report templates.
- AIG-010 review of motor types, compound devices, confirmations, unknown state after restart, and explicitly unsupported equipment, with representability tests.
- Optional private Prometheus metrics and Go `pprof` diagnostics on a listener
  separate from the public API, disabled by default and using bounded labels.
- Safety-gated `trainpilot-bench` load generator with versioned YAML profiles,
  deterministic scheduling, HTTP/WebSocket workers, invariants, and atomic
  secret-free reports.
- Deterministic `small`, `medium`, `large`, and `xlarge` benchmark fixtures,
  contention, ramp, reconnect, resynchronization, and soak profiles.
- Versioned report enrichment, publication validation, median comparison of
  repeated runs, and PASS/WARN/FAIL policies.
- Prometheus and Grafana configuration, dashboards, host textfile collectors,
  and systemd collector units for external monitoring.
- Fault and recovery orchestration, planned-outage handling, report schema v3,
  Prometheus recording and alerting rules, and `analyze-soak` output.
- Ubuntu simulator smoke benchmark in CI with separate active, reconnect, and
  resynchronization phases.
- Optional phase-aware live Prometheus metrics from `trainpilot-bench`, exposed
  by `--metrics-listen` on a dedicated listener.

### Changed

- Drivers now receive `position1` or `position2` through `SetBasicAccessory`, without geometric `straight`/`diverging` strings.
- The OpenAPI contract is now version `1.7.0` and AsyncAPI is now `1.9.1`.
  Turnouts expose `reportQuality`, use `position` for commands, and retain the
  `turnout.commanded`, `turnout.state.changed`, and
  `turnout.command.failed` events.
- Layout archives are now version 3 and separate turnout configuration from runtime state.
- SQLite now uses the pure-Go `modernc.org/sqlite` driver.
- A valid throttle or function command now renews the control lease.
- The WebSocket snapshot now includes command-station capabilities and current status.
- Command-station capabilities now expose `maxFunctionNumber`; `functions` remains the number of functions for compatibility.
- A WebSocket connection now expires with the access token used to open it and closes after session revocation; closing it does not automatically release leases.
- Benchmark reports now use schema v3. Schema v2 reports remain readable and
  are migrated in memory.
- GoReleaser now packages `trainpilot-bench`, benchmark profiles, fixtures, and
  operational monitoring resources with the existing binaries and docs.

## v0.0.1

### Changed

- Initial version
- Z21 interface mostly working. RBUS still not integrated
- Train driving is working through command line
