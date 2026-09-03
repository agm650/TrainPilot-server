# TrainPilot Server

TrainPilot Server is the foundation for controlling a digital DCC railway from
multiple native clients. This repository intentionally contains no graphical
macOS, iOS, or Linux client.

## Current release status

This release is a **functional and testable MVP**. Its purpose is to stabilize
the architecture and the contract used by future clients.

Included features:

- Go JSON HTTP server;
- OpenAPI contract and AsyncAPI event contract;
- WebSocket implementation without an external Go dependency, with monotonic
  sequences, an initial snapshot, and on-demand resynchronization;
- SQLite through `modernc.org/sqlite`, without CGO;
- users, roles, sessions, revocable access tokens, and refresh tokens;
- user administration through a local Unix socket only;
- exclusive locomotive control leases scoped to a session;
- lease heartbeats;
- zero-speed stop before releasing an expired lease;
- demonstration locomotives, blocks, turnouts, and routes;
- normalized feedback and sensor-to-block mappings;
- simulated command-station driver;
- DCC-EX TCP driver for track power, emergency stop, speed, functions,
  accessories, and sensor feedback, with health tracking and automatic
  reconnection;
- Z21 UDP driver for track power, emergency stop, speed, functions, binary
  accessories, and R-BUS parsing;
- versioned rolling-stock and layout import/export using native ZIP archives;
- `dccctl` diagnostics and transfer CLI;
- `dcc-api-conformance` conformance tool;
- unit, concurrency, protocol, and integration tests.

Known MVP limitations:

- full graphical layout editing is not implemented. Archives currently cover
  locomotives, blocks, turnouts, routes, and feedback mappings, but not
  graphical resources;
- R-BUS decoding still requires validation with a real white z21 and the
  selected modules;
- z21 accessory commands and reports are covered by a fake UDP server, but
  addressing and pulse timing still require validation on the physical test
  bench;
- the DCC-EX driver provides the basic commands, feedback, and automatic
  reconnection after a successful initial connection. Broader protocol coverage
  and validation on real hardware remain incomplete;
- physical confirmation that a locomotive has stopped is unavailable on some
  command stations. A safety delay is used instead;
- one server process controls one command station;
- CV programming is not included;
- passwords currently use PBKDF2-HMAC-SHA256 with 600,000 iterations. The
  implementation isolates password hashing so it can later migrate to Argon2id
  with progressive rehashing.

## Repository layout

```text
api/                         OpenAPI and AsyncAPI contracts
cmd/dccd/                    server and local administration
cmd/dccctl/                  diagnostics CLI client
cmd/dcc-api-conformance/     conformance tests against a running server
internal/api/                HTTP and WebSocket APIs
internal/admin/              Unix-socket administration server/client
internal/auth/               passwords and opaque tokens
internal/service/            business and safety rules
internal/station/            command-station abstraction and drivers
internal/store/              domain persistence
internal/transfer/           versioned archives and import validation
internal/sqlite/             SQLite through database/sql and modernc.org/sqlite
internal/websocket/          minimal RFC 6455 implementation
internal/*_test.go           unit tests
tests/contract/              versioned contract-scenario validation
tests/integration/           integration tests
tests/simulator/scenarios/   deterministic virtual test-bench scenarios
contract-tests/              domain scenarios readable by multiple clients
deploy/                      systemd unit and Linux configuration example
```

## Requirements

### Linux and macOS

- Go 1.26 or later;
- GoReleaser 2.17 or later to produce binaries and release archives.

Persistence uses `modernc.org/sqlite`, a pure-Go `database/sql` driver. No C
compiler or system SQLite package is required. Binaries can be built natively
or cross-compiled with `CGO_ENABLED=0`.

## Build and test

```bash
go mod download
go test ./...
CGO_ENABLED=0 go test ./...
go test -race ./...
go vet ./...
goreleaser check
goreleaser release --snapshot --clean --skip=publish
```

GoReleaser creates one archive per target platform under `dist/`. Each archive
contains:

```text
bin/dccd
bin/dccctl
bin/dcc-api-conformance
README.md
config.json
api/
docs/
deploy/
```

To build only the three binaries for the current platform:

```bash
goreleaser build --single-target --snapshot
```

## Develop without a DCC command station

The versioned `config.json` is a development configuration that uses the
simulator and binds to the loopback interface.

For Go tests, the simulator provides an injectable clock, a deeply copied state
snapshot, and a deterministic reset that preserves its connection state.
Simulated accessories distinguish the requested `Desired` state from the
observed `Reported` state. They can confirm immediately, confirm after a
deterministic delay, remain unconfirmed, or report an inconsistent state. These
introspection features belong to the test bench and do not alter the public
`/api/v1/...` API.

The initial simulated telemetry is stable: 25 degrees Celsius, an 18,000 mV
supply, 0 mV on the track while power is off, zero current, and no faults.
Tests can inject current, voltage, temperature, programming mode, power loss,
overheating, and short circuits. No physical model or hidden automatic power
cut is applied.

Simulator connectivity can be forced to `online`, `degraded`, or `offline`.
Typed per-operation rules can add a context-aware delay and an error to the next
N calls. `Remaining: 0` keeps the rule active until `ClearFaults()` or
`Reset()`. A delayed operation is cancelled when faults are cleared, the
simulator is reset or closed, or connectivity changes. Rejected commands are
never replayed.

Simulated sensors are identified by source, type, and address. `SetFeedback`
updates their physical state and either guarantees event delivery or returns a
saturation error. Repeated events are preserved. `SetFeedbackState` explicitly
models a physical change whose event was lost. Sequences and bounce patterns
use the injected clock. The older `InjectFeedback` method remains available as
a best-effort compatibility API.

The `internal/station/simulator/scenario` package loads versioned JSON scenarios
and validates the complete document before execution. In manual mode, a
`Runner` connected to `clock.Fake` advances without real sleeping. It executes
all due steps and preserves file order when several actions have the same
timestamp. `StartRealtime(ctx)` uses real time for interactive testing. It is
cancellable and also stops when the simulator is closed or reset externally.
The control snapshot exposes the loaded scenario, its
`loaded/running/completed/stopped/failed` state, logical time, next step, and
last error.

Reference scenarios live under `tests/simulator/scenarios/`. The current v2
format uses Go durations such as `500ms`, `5s`, and `1m`; the loader still
accepts v1. Available actions are:

- `station.connectivity`, `station.track_power`, `station.emergency_stop`, and
  `station.electrical`;
- `feedback.set` with `emit: true|false`, and `feedback.emit`;
- `accessory.report` and `accessory.behavior`;
- `fault.operation`, `fault.clear`, and `simulator.reset`.

The SIM-008 suite covers twelve cases: nominal driving, emergency stop,
`degraded` and `offline` recovery, an electrical short circuit, single and
multiple feedback sensors, bounce and event loss, then successful, missing, and
inconsistent accessory confirmation. Critical scenarios exercise the real HTTP
and WebSocket APIs during `go test ./...`. Time advances logically, without
waiting 10 or 30 real seconds.

AIG-003 scenarios add a simple binary endpoint, the three valid and one invalid
vectors of a three-way turnout, all four vectors of a double slip, a fault
targeted at one endpoint, and a stale delayed confirmation.

Example of manual execution in a Go test:

```go
clk := clock.NewFake(start)
sim := simulator.NewWithClock(clk)
_ = sim.Connect(ctx)
definition, _ := scenario.LoadFile("tests/simulator/scenarios/feedback-a-to-b.json")
runner, _ := scenario.New(definition, sim, clk)
_ = runner.Start(ctx)
_ = runner.Advance(ctx, 3*time.Second)
```

The scenario engine depends on neither domain services, SQLite, nor HTTP
handlers. It only simulates the external world observed by TrainPilot.

When `testAPI=true` and `station.driver` is `simulator`, another process can
control the test bench through `/test/v1/simulator/...`. The API covers state
snapshots, reset, connectivity, telemetry, feedback, accessories, faults, and
manual scenario advancement. These routes require authentication. They do not
exist with a hardware driver or when `testAPI=false`. Their separate contract
is documented in
[`docs/SIMULATOR_TEST_API.md`](docs/SIMULATOR_TEST_API.md); they are deliberately
absent from the public production OpenAPI contract.

The complete client-development guide covers HTTP, WebSocket, leases,
scenarios, errors, and PlantUML sequence diagrams. See
[`docs/CLIENT_SIMULATOR_GUIDE.md`](docs/CLIENT_SIMULATOR_GUIDE.md). It is
included in every release archive.

Start the server:

```bash
go run ./cmd/dccd serve --config config.json
```

In another terminal, create users while the server is running:

```bash
printf '%s\n' 'correct-horse-1' |
  go run ./cmd/dccd user bootstrap \
    --socket /tmp/dccd-admin.sock \
    --username alice \
    --display-name 'Alice' \
    --role driver \
    --password-stdin

printf '%s\n' 'correct-horse-2' |
  go run ./cmd/dccd user add \
    --socket /tmp/dccd-admin.sock \
    --username bob \
    --display-name 'Bob' \
    --role driver \
    --password-stdin
```

`bootstrap` works only while the user table is empty.

After login, copy the returned `accessToken`, then load and advance a scenario
from another terminal:

```bash
curl -sS http://127.0.0.1:8080/api/v1/auth/login \
  -H 'Content-Type: application/json' \
  -d '{"username":"alice","password":"correct-horse-1","clientId":"simulator-console-1","clientName":"simulator-console","platform":"cli"}'

export TRAINPILOT_TOKEN='<returned accessToken>'

curl -sS -X POST http://127.0.0.1:8080/test/v1/simulator/scenarios \
  -H "Authorization: Bearer $TRAINPILOT_TOKEN" \
  -H 'Content-Type: application/json' \
  --data-binary @tests/simulator/scenarios/station-offline-recovery.json

curl -sS -X POST http://127.0.0.1:8080/test/v1/simulator/scenarios/start \
  -H "Authorization: Bearer $TRAINPILOT_TOKEN"

curl -sS -X POST http://127.0.0.1:8080/test/v1/simulator/scenarios/advance \
  -H "Authorization: Bearer $TRAINPILOT_TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{"duration":"2s"}'
```

Public state is visible through `dccctl ... power status`. To inspect the
snapshot and ordered events, a WebSocket client such as `websocat` can connect
to `ws://127.0.0.1:8080/api/v1/events` with the
`Authorization: Bearer <accessToken>` header.

List users:

```bash
go run ./cmd/dccd user list --socket /tmp/dccd-admin.sock
```

Disable a user and revoke their sessions:

```bash
go run ./cmd/dccd user disable --socket /tmp/dccd-admin.sock --username bob
```

No `/api/v1/users` route is exposed to clients. Even an application user with
the `administrator` role cannot create accounts remotely.

## Test the API

```bash
DCC_PASSWORD='correct-horse-1' \
go run ./cmd/dccctl \
  --server http://127.0.0.1:8080 \
  --username alice \
  --password-env DCC_PASSWORD \
  locomotives
```

Run the conformance suite:

```bash
go run ./cmd/dcc-api-conformance \
  --server http://127.0.0.1:8080 \
  --user1 alice --pass1 correct-horse-1 \
  --user2 bob   --pass2 correct-horse-2
```

Without destructive options, the suite verifies health, contract versions,
authentication, token rotation and revocation, authenticated read operations,
structured errors, and exports. Display its complete endpoint inventory with:

```bash
go run ./cmd/dcc-api-conformance --list-endpoints
```

Track commands run only with `--allow-active-commands`. Temporary configuration
changes additionally require `--allow-configuration-mutations`, `--admin`, and
`--admin-pass`. Use them only with a disposable simulator instance.

Turnouts have a separate opt-in check. `--check-turnouts` validates logical
positions, commands a turnout, checks its confirmed state, and verifies that an
unknown position is rejected. It requires `--admin` and `--admin-pass` and must
target an explicitly selected test instance.

Real-hardware validation is a separate campaign. The guide under
[`docs/hardware-tests/turnouts/README.md`](docs/hardware-tests/turnouts/README.md)
and `scripts/test-turnouts.sh` cover addressing, z21 pulses, external reports,
compound turnouts, endurance, and reconnection without replay. The script sends
no command without `--acknowledge-hardware-risk` and supports `--dry-run`.
Until a dated result sheet is completed, accessory support is considered
validated by simulators and fakes only.

Both opt-in families can be combined on such an instance:

```bash
go run ./cmd/dcc-api-conformance \
  --server http://127.0.0.1:8080 \
  --user1 alice --pass1 correct-horse-1 \
  --user2 bob --pass2 correct-horse-2 \
  --admin admin --admin-pass correct-horse-admin \
  --allow-active-commands \
  --allow-configuration-mutations \
  --check-turnouts
```

These options are disabled by default. Never target a real command station
without an explicit operator decision.

Natural access-token and refresh-token expiration is checked only with
`--check-session-expiration`. Use a test server configured with short TTLs:

```json
"security": {
  "accessTokenTTL": "2s",
  "refreshTokenTTL": "5s"
}
```

Then run:

```bash
go run ./cmd/dcc-api-conformance \
  --server http://127.0.0.1:8080 \
  --user1 alice --pass1 correct-horse-1 \
  --user2 bob   --pass2 correct-horse-2 \
  --check-session-expiration \
  --session-expiration-max-wait 15s
```

The maximum wait defaults to `15s` and prevents accidental waiting with
production TTLs. The scenario uses two dedicated sessions. It first checks that
an expired access token is rejected while its refresh token remains valid. It
then verifies that a naturally expired refresh token cannot issue a new token
pair. Without the option, these checks are skipped and do not slow the standard
suite.

The active suite also verifies:

- authentication of both users;
- rolling-stock listing;
- exclusive control acquisition;
- rejection of a conflicting second acquisition;
- a speed command from the owner;
- controlled release;
- absence of remote user administration;
- rolling-stock export;
- rejection of an import by a non-administrator.

## Rolling-stock management

The API provides basic locomotive CRUD. Any authenticated user can read it;
creation, modification, and deletion require the `administrator` role. A
locomotive with an active lease cannot be modified. A locomotive referenced by
lease history cannot be deleted.

Create a short-address locomotive for a hardware test:

```bash
DCC_ADMIN_PASSWORD='correct-horse-admin' go run ./cmd/dccctl \
  --server http://127.0.0.1:8080 --username admin \
  --password-env DCC_ADMIN_PASSWORD \
  locomotive-add 'z21 test locomotive' 3 short 128
```

List and inspect a locomotive:

```bash
DCC_PASSWORD='correct-horse-1' go run ./cmd/dccctl \
  --server http://127.0.0.1:8080 --username alice \
  --password-env DCC_PASSWORD locomotives

DCC_PASSWORD='correct-horse-1' go run ./cmd/dccctl \
  --server http://127.0.0.1:8080 --username alice \
  --password-env DCC_PASSWORD locomotive-show <locomotive-id>
```

Corresponding routes:

```text
GET    /api/v1/locomotives
POST   /api/v1/locomotives
GET    /api/v1/locomotives/{id}
PUT    /api/v1/locomotives/{id}
DELETE /api/v1/locomotives/{id}
```

For initial z21 testing, prefer a short address such as `3`. This isolates
driving and feedback validation from long-address details.

### Control with `dccctl`

`dccctl` stores its session and acquired leases in the user's configuration
directory, for example `~/.config/dccctl/state.json` on Linux, with `0600`
permissions. Override the path with `--state-file`. The password is not stored.
After the first login, later commands reuse the same session and refresh its
tokens automatically when required.

The CLI locates a lease automatically from the server, user, and locomotive:

```bash
DCC_PASSWORD='correct-horse-1' go run ./cmd/dccctl \
  --server http://127.0.0.1:8080 --username alice \
  --password-env DCC_PASSWORD acquire loco-bb26001

go run ./cmd/dccctl \
  --server http://127.0.0.1:8080 --username alice \
  throttle loco-bb26001 40 forward

go run ./cmd/dccctl \
  --server http://127.0.0.1:8080 --username alice \
  function loco-bb26001 0 true

go run ./cmd/dccctl \
  --server http://127.0.0.1:8080 --username alice \
  release loco-bb26001
```

Global track commands do not require a lease:

```bash
dccctl --server http://127.0.0.1:8080 --username alice power off
dccctl --server http://127.0.0.1:8080 --username alice power on
dccctl --server http://127.0.0.1:8080 --username alice power status
dccctl --server http://127.0.0.1:8080 --username alice emergency-stop
```

`power status` queries the command station and displays track power, emergency
stop, short circuits, programming mode, current, voltage, and temperature when
available. With Z21, these values come from `LAN_X_GET_STATUS` and
`LAN_SYSTEMSTATE_GETDATA`. For a driver that cannot read status yet, track power
is `unknown` until the first successful `power on` or `power off` command.

Connectivity becomes `online` after valid communication and `degraded` on the
first error. Optional `station.offlineAfter` uses the Go duration format (`ms`,
`s`, `m`, and so on) and defines the maximum time spent in that state. It
defaults to `10s`. The timer starts at the first communication error. A valid
response before expiration immediately restores `online`; otherwise health
becomes `offline` when the delay expires. Z21 status polling continues even
while offline. With DCC-EX TCP, a confirmed socket loss immediately rejects
commands, including during the `degraded` interval, and starts reconnection.
Rejected commands are never queued or replayed when the station returns.
Station unavailability produces HTTP 503 with code `station_offline`.

Safety commands are scheduled before ordinary commands. A queued emergency
stop, track-power cut, or zero-speed throttle takes precedence over new speed
and function commands. After an emergency stop, positive speeds and functions
remain inhibited until an explicit `power on` succeeds. They are also rejected
while track power is off or unknown. The API returns HTTP 409 with one of these
stable codes: `emergency_stop_active`, `track_power_off`,
`track_power_unknown`, or `safety_command_preempted`.

A valid throttle or function command extends its lease by ten minutes. Without
activity or heartbeat during that period, the server starts a controlled stop
and then releases the lease. `throttle` never acquires a locomotive implicitly;
`acquire` is mandatory.

## Import and export

Exports are version 3 ZIP archives containing `manifest.json` and a JSON
document. Version 1 and 2 archives remain importable. Imports use `merge` by
default. `--replace` replaces the corresponding library after validation.

```bash
# Export is available to every authenticated user
DCC_PASSWORD='correct-horse-1' go run ./cmd/dccctl \
  --server http://127.0.0.1:8080 --username alice \
  --password-env DCC_PASSWORD \
  export-rolling-stock rolling-stock.dcclib

DCC_PASSWORD='correct-horse-1' go run ./cmd/dccctl \
  --server http://127.0.0.1:8080 --username alice \
  --password-env DCC_PASSWORD \
  export-layout layout.dcclayout

# Import requires the application administrator role
DCC_ADMIN_PASSWORD='correct-horse-admin' go run ./cmd/dccctl \
  --server http://127.0.0.1:8080 --username admin \
  --password-env DCC_ADMIN_PASSWORD \
  import-layout layout.dcclayout --replace
```

An archive is limited to 25 MiB in total and 10 MiB per entry. Suspicious
paths, unknown versions, broken references, and duplicate identifiers are
rejected before the database is modified.

## Contract for client developers

The HTTP contract is [`api/openapi.yaml`](api/openapi.yaml). The WebSocket
contract is [`api/asyncapi.yaml`](api/asyncapi.yaml). Archive formats are
documented in [`docs/ARCHIVE_FORMAT.md`](docs/ARCHIVE_FORMAT.md). Versioning,
deprecation, and migration policy is documented in
[`docs/API_COMPATIBILITY.md`](docs/API_COMPATIBILITY.md).

Core rules:

1. The server is the source of truth.
2. A driving command requires a valid session and an active lease owned by that
   session.
3. A locomotive can have only one live lease (`active` or `stopping`).
4. A lease can move between two sessions of the same user only through the
   explicit `POST /api/v1/control-leases/{leaseId}/takeover` endpoint. The
   server stops the locomotive before atomically transferring the same lease.
   Standard acquisition never performs a takeover.
5. A valid driving command renews the lease. After ten minutes of inactivity,
   it first becomes `stopping`, receives a zero-speed command, and becomes
   `released` after the safety delay.
6. Safety commands preempt queued driving commands. Recovery after emergency
   stop is never implicit.
7. Route actions are rejected when a block is occupied or an incompatible route
   is active.
8. WebSocket events have a monotonic sequence during the process lifetime.
9. User accounts can be administered only through the operating system's local
   socket.

When a WebSocket connection opens, the server sends a complete
`system.snapshot` whose `sequence` is the event bus's current sequence. It
contains station status, locomotives, full leases for the connected session,
public ownership state for all controlled locomotives, blocks, turnouts, and
routes. `controlLeases` remains private to the session.
`locomotiveControlStates` distinguishes `mine`,
`same_user_other_session`, and `other` without exposing lease identifiers from
other sessions. A locomotive absent from that array is free.

The client ignores events with a sequence less than or equal to the snapshot.
The server also filters old or duplicate events generated while building the
snapshot. If the client later detects a gap, it sends:

```json
{
  "type": "client.snapshot_request",
  "lastSequence": 42
}
```

The server answers with a new `system.snapshot`. `lastSequence` is currently
informational: the response always contains the full current state.
`client.heartbeat` neither extends the access token nor a lease and consumes no
server sequence number. The WebSocket closes when the access token used to open
it expires. After refresh, the client must reconnect with the new token. Logout
or session revocation also closes the connection. The server does not retain
intermediate events for replay.

A successful takeover publishes `locomotive.control.transferred` to all
connected sessions. The old session must immediately stop its heartbeats and
commands. The new session receives the same `leaseId` with a new expiry. The
server neither retains nor restores the previous speed.

Each connection has a 64-event queue. If it overflows, or a WebSocket write
takes more than five seconds, the server closes the connection rather than let
the client continue with incomplete state. The client then reconnects and
starts from a new complete snapshot.

Closing a WebSocket does not immediately release leases. A brief network outage
must not cause loss of control. Leases remain valid until explicit release or
heartbeat expiration, which triggers the normal controlled-stop workflow.

## Turnouts and compound accessories

The model separates a logical turnout from its binary DCC outputs. A turnout
has a `kind`, endpoints, and logical positions defined by `position1` and
`position2` vectors.

The command-station abstraction uses `SetBasicAccessory` with a portable linear
address in `1..2040` and a typed position. Drivers never receive logical names
such as `straight` or `diverging`. An optional provider distinguishes station
reports, assumed state, and future physical sensors.

The model represents simple, three-way, double-slip, single-slip, and custom
turnouts. An undeclared physical vector remains unknown and never becomes a
commandable position.

The simulator applies vectors sequentially and publishes one physical-quality
`AccessoryStateEvent` for each confirmation. Tests can inject `station`,
`assumed`, or `physical` reports. An undeclared complete vector clears
`reportedPosition` and sets `reportedStatus` to `invalid`.

The service serializes commands per turnout. It computes a path where each step
changes one endpoint. A three-way turnout therefore passes through `straight`
between `left` and `right`. Each step must be confirmed before the next starts.
External changes update `reportedPosition` without resending the desired
command.

`turnout.confirmationTimeout` is a Go duration and defaults to `2s`. It applies
to every step of a compound transition. On timeout, the target remains in
`desiredPosition`, the last report is preserved, and `commandStatus` becomes
`timeout`.

The public API commands a logical position declared by the turnout:

```http
PUT /api/v1/turnouts/T3
Content-Type: application/json

{"position":"right"}
```

A `204` response means every step was confirmed. Clients read valid choices
from `positions` and then follow `desiredPosition`, `reportedPosition`,
`pending`, `reportedStatus`, `reportQuality`, and `commandStatus`. The legacy
`state` request field is accepted only for a `simple` turnout and is deprecated.

The CLI uses the same contract:

```bash
dccctl turnouts
dccctl turnout T3 --positions
dccctl turnout T3 right
```

Legacy one-address databases and archives are converted automatically to
simple turnouts. Layout v3 exports include physical configuration but exclude
runtime state. Legacy fields remain temporarily exposed for simple turnouts.
After partial failure, the server performs no blind rollback.

A turnout definition cannot be replaced or removed while `pending=true`.
Conflicting imports return HTTP 409 with `turnout_configuration_pending`.
Likewise, one linear accessory address can belong to only one logical turnout.
An import that shares an address returns HTTP 409 with
`accessory_address_conflict` and makes no partial change. Deliberate coupling
must be represented as multiple endpoints of one `custom` turnout.

See [`docs/TURNOUTS.md`](docs/TURNOUTS.md) for the complete model, addressing
rules, validation, driver semantics, and examples.

## Command-station configuration

### Simulator

```json
"station": {
  "driver": "simulator"
}
```

### DCC-EX over TCP

```json
"station": {
  "driver": "dccex",
  "address": "192.168.1.50",
  "port": 2560,
  "transport": "tcp",
  "offlineAfter": "10s"
}
```

The initial TCP connection must succeed for the server to start. If the socket
is later lost, the driver becomes `degraded`, automatically reconnects, and
becomes `offline` after `offlineAfter` if DCC-EX does not return. A successful
reconnection immediately restores `online`, and sensor feedback resumes on the
existing channel. Commands submitted while the socket is unavailable are
rejected without queuing or replay. Earlier speed, function, or accessory
positions are never restored automatically.

DCC-EX accessories use the raw linear form:

```text
position1 -> <a LINEAR_ADDRESS 0>
position2 -> <a LINEAR_ADDRESS 1>
```

TrainPilot uses the portable range `1..2040`. The driver never creates a
persistent `<T>` definition in the command station: IDs and compound turnout
definitions remain owned by TrainPilot. After a successful TCP write, it
publishes an `assumed` report. DCC-EX does not retain raw `<a>` command state, so
this report confirms neither decoder reception nor blade movement. No external
change is inferred without a reliable feedback source. Other systems may show
the same output as a decoder/subaddress group of four; the diagnostic procedure
is documented in `docs/TURNOUTS.md`.

### z21/Z21 over UDP

```json
"station": {
  "driver": "z21",
  "address": "192.168.0.111",
  "port": 21105,
  "offlineAfter": "10s",
  "accessoryPulse": "100ms"
}
```

`offlineAfter` is shared by Z21 and DCC-EX. It accepts `time.ParseDuration`
syntax such as `500ms`, `5s`, `30s`, or `1m`. An invalid, zero, or negative
value prevents startup.

`accessoryPulse` configures how long a z21 binary output stays active and
defaults to `100ms`. It uses the Go duration format. Invalid, zero, or negative
values also prevent startup. The driver activates the output, waits for the
configured duration, and deactivates it. Deactivation uses an internal safety
context even if the client request is cancelled.

Logical turnout confirmation has a separate setting:

```json
"turnout": {
  "confirmationTimeout": "2s"
}
```

It applies to every step of a compound transition, uses Go duration syntax, and
must be strictly positive.

The driver accepts TrainPilot linear addresses `1..2040`, converts them to
`FAdr = address - 1`, then queries `LAN_X_GET_TURNOUT_INFO` and publishes
spontaneous reports. z21 states “not switched yet” and “invalid” remain unknown
to the turnout; the server never invents a position. A command-station report
does not prove physical blade movement. Validation on a real white z21 must use
the AIG-009 campaign under `docs/hardware-tests/turnouts/`.

## Feedback

The `feedback_mappings` table maps a physical source to a logical block:

```text
provider = dccex, address = 14   -> block_id = station-track-1
provider = z21-rbus, address = 9 -> block_id = main-line
```

Drivers publish generic `FeedbackEvent` values. The railway service updates the
block and then publishes `block.occupancy.changed` over WebSocket.

## Network security

The demonstration configuration listens only on `127.0.0.1` and uses HTTP. To
listen on a LAN, configure `tlsCert` and `tlsKey`, or place the server behind a
correctly configured TLS reverse proxy.

Protect the administration Unix socket with operating-system permissions. The
decimal JSON value `432` represents octal mode `0660`.

## Suggested next steps

1. Validate both drivers with real DCC-EX and white z21 hardware.
2. Extend rolling stock beyond locomotives and complete layout editing.
3. Extend archives with graphical resources, images, and future format
   migrations.
4. Add signals, explicit route conflicts, and progressive route release.
5. Add hardware tests that run only on a dedicated test bench.
6. Build Swift and Linux clients against the simulator and published contracts.
