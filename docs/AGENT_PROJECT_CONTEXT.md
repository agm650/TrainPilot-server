# TrainPilot / DCC — Detailed Agent Project Context

This document contains detailed architectural, hardware, protocol, and project
decisions for coding agents.

It is intentionally not part of the minimal always-loaded agent instructions.

Read this document when a task affects one or more of the following:

- command-station drivers;
- z21 or DCC-EX;
- locomotive control or reservations;
- accessories or turnouts;
- simulator behavior;
- block occupancy or feedback;
- WebSocket events or synchronization;
- API contracts;
- persistence models;
- safety logic;
- release/build architecture;
- cross-repository responsibilities.

The repository code, tests, and current configuration remain the source of truth.

If documentation and implementation disagree, explicitly report the discrepancy
and base implementation decisions on the current repository unless the task
explicitly requires changing that behavior.

Do not assume a feature is absent merely because it appears in
`docs/DCC_BACKLOG.md`. Some backlog entries represent validation, consolidation,
or extensions of existing functionality.

## Recommended Supporting Documentation

Read only the documents relevant to the task:

- `docs/DCC_CONTEXT.md` — hardware and laboratory context;
- `docs/DCC_ARCHITECTURE.md` — current architecture;
- `docs/DCC_BACKLOG.md` — planned, incomplete, or validation work;
- `RTK.md` — RTK command usage and output-reduction rules.

Always inspect the actual code and tests involved in the task.

# Project Purpose

TrainPilot is a cross-platform server for safely controlling digital DCC railway
equipment through an API.

The server isolates clients from command-station-specific protocols, manages
users and reservations, exposes network state, and is intended to support
automated route operation later.

Project priorities, in order, are:

1. safe operation of real railway hardware;
2. consistency of state and events;
3. API compatibility;
4. testability without physical hardware;
5. macOS/Linux portability;
6. operational simplicity.

# Architectural Decisions

## Server and Platform

- The server is implemented in Go.
- Do not introduce Java.
- Linux binaries must remain buildable with `CGO_ENABLED=0`.
- SQLite uses `modernc.org/sqlite`.
- One TrainPilot server instance controls one command station.
- Multiple command stations require multiple server instances.
- Native clients are preferred:
  - macOS in Swift first;
  - Linux later.

## Command Stations

### z21

The white z21 remains the first command station validated against real hardware.

The z21 driver implements binary accessories and their function feedback.

UDP tests are automated, but the following still require real-hardware
validation where applicable:

- addressing;
- pulse behavior;
- mechanical turnout position.

A feedback quality of `station` must never be presented as proof of physical
position.

### DCC-EX

The DCC-EX TCP driver implements:

- `online`;
- `degraded`;
- `offline`;

and automatic reconnection.

Protocol coverage and physical-hardware validation may still be incomplete.

DCC-EX accessories are commanded using:

    <a linear 0|1>

After a successful write, TrainPilot publishes only an `assumed` feedback state.

The server must not:

- create `<T>` definitions;
- invent external state changes;
- invent physical feedback.

# Accessories and Turnouts

Driver-level accessory addressing uses only:

- linear addresses `1..2040`;
- binary positions `position1` and `position2`.

Geometrical concepts belong to the business model, not the driver layer.

Examples include:

- straight;
- diverging;
- triple turnouts;
- double-slip crossings;
- single-slip crossings.

A logical turnout may contain multiple physical endpoints.

Its valid commandable positions are only the explicitly declared endpoint
vectors.

A physical combination that has not been declared must remain unknown and must
never be reported as a successful logical position.

A turnout command must never directly modify `reportedPosition`.

The controller must:

- wait for endpoint reports;
- use steps that change only one endpoint at a time;
- serialize operations per logical turnout;
- never perform blind rollback after a partial failure.

A partially failed compound turnout command must never be reported as successful.

# Simulator

The simulator is the deterministic reference environment for automated testing.

Its control API remains under:

    /test/v1/simulator/...

This API:

- exists only when `testAPI=true`;
- requires the simulator driver;
- must never become part of the public production API.

Simulator scenarios currently use JSON format version 2.

Backward-compatible reading of version 1 scenarios must be preserved unless a
different migration strategy is explicitly documented.

# CV Programming

CV programming is outside the scope of TrainPilot-server.

It belongs to the separate CVProgrammer / Z21CVProgrammer application.

Do not add CV programming functionality to TrainPilot-server without an explicit
architecture decision changing this boundary.

# Users, Reservations, and Sessions

The server is authoritative for:

- users;
- locomotive reservations;
- lease/session expiration;
- safety rules.

Clients must not independently override those server-side decisions.

# Command-Station State and Reconnection

Loss of communication with a command station must transition:

    online -> degraded -> offline

The `degraded -> offline` transition occurs after `station.offlineAfter`.

The default value remains:

    10s

When the command station is `offline`, do not transmit new active commands,
including:

- locomotive throttle commands;
- locomotive function commands;
- accessory commands.

After command-station reconnection, never automatically restart a locomotive at
its previous speed.

A new explicit throttle command is required.

Rejected commands must return explicit, actionable errors.

Do not simulate success.

# Emergency and Safety Behavior

Emergency stop and track-power-off operations take priority over ordinary
commands.

A partially failed compound device command must never be considered successful.

Do not automatically replay rejected or failed hardware commands unless the
protocol and architecture explicitly define that behavior.

Validate all external API values before forwarding them to hardware drivers.

Automated tests must never control physical hardware by default.

Hardware tests must:

- be explicitly enabled;
- be clearly documented;
- identify the hardware assumptions involved.

# Events and WebSocket Synchronization

Events must expose a monotonic sequence usable by clients.

Snapshots must report the current event-bus sequence and must not use a constant
placeholder value.

If a client detects a sequence gap, it may request a new snapshot using:

    client.snapshot_request

This mechanism is part of the current AsyncAPI contract.

Command-station events must allow clients to distinguish at least:

- `online`;
- `degraded`;
- `offline`.

Changes to the event protocol must include tests covering:

- reconnection;
- event loss;
- resynchronization;
- duplicate events.

# API Compatibility

API changes must preserve compatibility whenever possible.

If compatibility cannot be preserved, the change must include an explicit
migration strategy.

Do not invent:

- endpoints;
- schemas;
- event names;
- package names;

without first inspecting the existing repository and contracts.

# Build and Release

Always prefer build and validation commands documented by the repository.

When no more specific command exists, common checks include:

```bash
gofmt -w <modified-go-files>
go test ./...
go vet ./...
CGO_ENABLED=0 go test ./...
goreleaser release --snapshot --clean --skip=publish
```

GoReleaser configuration must remain compatible with GoReleaser v2 syntax.

Expected release artifacts include the configured binaries for supported
platforms, including:

- server;
- CLI;
- conformance tooling.

# Documentation and Local Data

Never commit:

- passwords;
- API tokens;
- authentication files;
- production SQLite databases;
- logs containing user data or secrets.

Laboratory IP addresses documented in `docs/DCC_CONTEXT.md` are test-bench
values and must remain configurable.

Every new configuration variable must document:

- its default value;
- its unit, where applicable;
- its operational effect.

# Repository Boundaries

## TrainPilot-server

Contains:

- DCC server;
- public API;
- persistence;
- CLI;
- conformance tests;
- command-station integrations.

## TrainPilot macOS Client

Contains:

- native macOS user interface;
- network editor;
- driving interface.

It is a separate repository/application.

## Z21CVProgrammer / CVProgrammer

Contains:

- CV programming;
- locomotive speed calibration.

It is not a functional submodule of TrainPilot-server.

If a requested task mixes responsibilities from several repositories, identify
the target repository before modifying code.
