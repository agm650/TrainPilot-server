# Block occupancy

TrainPilot models block occupancy independently from the detection technology.
The runtime state is separate from the static `BlockDefinition`.

## States

An occupancy state is `unknown`, `free`, or `occupied`.

- `unknown` means that TrainPilot lacks sufficient current information.
- `free` means that the configured sources provide enough information to declare
  the block free.
- `occupied` means that at least one fresh source reports occupancy.

`unknown` is never equivalent to `free` in business or safety decisions. The
legacy `occupied` boolean is derived as `state == occupied` only for backward
compatibility. Code making safety decisions must use the explicit state.

## Providers and sensors

An occupancy provider defines an ID, an extensible type, a default priority, a
required flag, and its freshness policy. A sensor mapping associates a provider
and sensor ID with one block. A mapping can override the provider's `required`
and `priority` defaults.

Provider and mapping configuration is persisted. Runtime observations are not
persisted. After a server restart, every block therefore starts as `unknown`
until the aggregation service receives sufficient current observations.

## Required and optional sources

A missing, unavailable, or stale required source makes the aggregated state
`unknown`, unless another fresh source reports `occupied`. An unavailable or
stale optional source does not prevent other sufficient sources from declaring
the block free.

## Priority

Priority is an integer from 0 through 100. It resolves non-safety metadata, such
as an occupant identity. It never allows a higher-priority `free` observation to
override an `occupied` observation.

## Occupants

An optional occupant reference contains an extensible type and an ID. It is
valid only with the `occupied` state. `occupied` without a known occupant is
valid. V1 consumers can use the `locomotive` type.

## Freshness

Providers that require time-based freshness must configure a positive
`staleAfter` duration. A zero duration disables timer-based expiry for sources
whose liveness is established differently, such as sticky R-BUS state tied to
command-station health. Negative durations are invalid.

Observations carry both `observedAt` and `receivedAt`. Freshness is evaluated
from `receivedAt`; `observedAt` remains diagnostic information. Protocols that
require sequencing reject sequence zero and must later reject observations that
do not advance the last accepted sequence.

## Safety rules

- Any fresh `occupied` observation wins over every `free` observation.
- A required source cannot silently degrade to `free` when data becomes stale.
- Startup and restart never restore an old runtime `free` observation.
- Occupancy configuration does not imply that a sensor observation exists.

The aggregation order is fixed: any fresh `occupied` wins; otherwise a missing,
unavailable, stale, or `unknown` required source produces `unknown`; otherwise
fresh required sources produce `free`. With no required source, at least one
fresh `free` is needed to produce `free`.

The service keeps only the last accepted observation for each provider and
sensor. Lower sequences are rejected. An identical duplicate sequence is an
accepted no-op, while a conflicting duplicate is rejected. A higher-sequence
refresh updates freshness without publishing another business event when the
aggregated state is unchanged. Providers must keep sequences monotonic for a
logical stream; a future stream or epoch identifier will be needed for explicit
provider-side sequence resets.

A single central sweep recalculates time-based freshness, so a block can move
to `unknown` without another external observation. Occupant metadata comes from
the highest-priority fresh occupied source. Conflicting identities at the same
highest priority yield no occupant identity.

The service exports bounded occupancy metrics without block or sensor labels:

- `trainpilot_occupancy_observations_total`;
- `trainpilot_occupancy_observations_rejected_total`;
- `trainpilot_occupancy_block_state`;
- `trainpilot_occupancy_source_stale`;
- `trainpilot_occupancy_conflicts` with bounded `state` and `identity` types;
- `trainpilot_occupancy_external_observation_latency_seconds`.

## R-BUS and station feedback

The z21 adapter publishes R-BUS inputs through provider `z21-rbus`. Active
inputs become `occupied`; inactive inputs become `free`. TrainPilot assigns a
runtime monotonic sequence to each provider and address before passing the
observation to `OccupancyService`.

At connection time, the z21 driver subscribes to R-BUS broadcasts and explicitly
requests both groups with `LAN_RMBUS_GETDATA` (groups 0 and 1, inputs 1 through
160). This reads detectors that are already occupied without requiring a new
physical transition. The requests follow section 7.2 of the
[Z21 LAN protocol specification](https://www.z21.eu/media/Kwc_Basic_DownloadTag_Component/root-en-main_47-1652-959-downloadTag-download/default/d559b9cf/1699290380/z21-lan-protokoll-en.pdf).

Groups without a complete reply are requested again by the station status
loop. A missing reply never implies `free`: the corresponding blocks remain
`unknown` until enough current observations arrive. Initial replies use the
same occupancy aggregation and client event pipeline as subsequent changes.
The driver buffers a complete 160-input snapshot before the feedback consumer
starts and waits for queue space rather than discarding observations if the
consumer falls behind. Closing the driver interrupts that wait.

R-BUS feedback is event-driven and has no arbitrary timer expiry. Its
`staleAfter` is disabled and freshness follows command-station health. When the
station becomes `degraded`, accepted observations from its provider are cleared
and required mappings become `unknown`. Returning `online` does not revive the
old observation or invent `free`; a new feedback observation is required.
The public station still progresses from `degraded` to `offline` according to
`station.offlineAfter`; occupancy invalidation does not wait for that deadline.

On a z21 network failure, TrainPilot advances a feedback generation, resets
both group-received flags and renews the UDP endpoint before reacquiring data.
The previous endpoint is reserved until the new one is bound, so delayed UDP
replies to the previous endpoint cannot enter the new stream. Broadcast
subscriptions are refreshed and only missing groups are queried again.
Queued observations carry their generation and are checked again at occupancy
mutation time. Generation changes invalidate both explicit `z21-rbus` mappings
and station wildcard mappings. Independent providers are unaffected. A partial
response restores only observed sensors; a station status reply does not imply
that either R-BUS group has been synchronized.

Each R-BUS wire frame is delivered as one internal feedback event containing
all 80 inputs. The service resolves explicit source mappings before wildcard
mappings, ignores unmapped inputs, and submits the remaining observations in
one `ObserveBatch` operation. Empty or completely unmapped frames do not invoke
the batch observation API. Other drivers may continue emitting individual
feedback events, which use the same pipeline as single-observation batches.

All affected blocks are recomputed before the first business event is
published. A train transferring between two inputs of the same block in one
frame cannot produce an intermediate `free` state. Unchanged frames refresh
observation sequences without emitting another business event. R-BUS transition
logs use the final occupied-address set for the entire frame, including unmapped
inputs. The internal queue is bounded to 64 frames and applies backpressure.
This atomicity applies to one group frame, not to two independently received
groups. Public WebSocket events remain individual block changes; the contract
does not promise an atomic multi-block update in each client view.

The existing block occupancy objects and WebSocket payload shapes remain unchanged. Hardware
characterization and the post-fix acceptance procedure are recorded in
[`hardware-tests/rbus/lot-A.md`](hardware-tests/rbus/lot-A.md).

Legacy numeric feedback mappings are migrated once to the generic
provider/sensor/block table. The compatibility store methods use that same
table, so there is no second occupancy pipeline. Migrated providers default to
priority 100, required, and sticky freshness. Their settings can be changed in
the persisted provider configuration.

## Track-power-dependent providers

Providers can declare `trackPowerDependent` as an optional boolean. `true`
requires known ON track power without a short circuit or power-loss indication.
`false` explicitly declares an independent sensor. When omitted, legacy
`current-detection`, `z21-rbus`, `dccex` and wildcard providers default to
dependent; other providers default to independent. An omitted field during a
provider update or layout merge preserves a previously explicit declaration.

The server starts with unknown track power. Dependent observations cannot
declare a block free or occupied while power is OFF, unknown or faulty. Such
observations are ignored for runtime occupancy, including during a batch that
also contains usable independent observations. Independent observations continue
through the normal freshness and aggregation rules. Any fresh independent
occupied source still wins over missing dependent observations.

Both power loss and restoration clear dependent observations. Restoration
therefore cannot revive the previous free or occupied state. A separate power
generation fences queued observations without invalidating independent sources.
z21 renews its UDP endpoint and explicitly queries both groups; DCC-EX renews
its TCP stream and sends the read-only `<Q>` sensor-status query documented in
the [official DCC-EX command reference](https://github.com/DCC-EX/dcc-ex.github.io/blob/sphinx/docs/reference/software/command-summary-consolidated.rst).
Missing responses remain unknown. No locomotive or accessory commands are
replayed. A power-only synchronization does not report a network failure.
Emergency stop alone does not imply electrical power loss.
For stations exposing an observed status (including z21), an ON command is
followed by a status query before detector validity is restored. A successful
command write cannot override a station that still reports OFF or a fault.

`GET /api/v1/blocks/{id}/occupancy-sources` includes the resolved
`trackPowerDependent` flag; `available` and `fresh` include power validity.
The HTTP contract is version 1.15.0; existing WebSocket payload shapes and their
1.13.0 event contract remain unchanged. Layout archives version 8 retain provider
power declarations; versions 1 through 7 remain importable.

## External observation API

External providers use a dedicated account with the `sensor` role. This role
has only the `occupancy:write` capability. It cannot drive locomotives, command
turnouts, import layouts, or administer users. Providers accepted by the public
API must be configured with `freshnessRequired=true` and a positive
`staleAfter`; this prevents a sensor credential from impersonating sticky
station feedback.

Provider and sensor mappings are part of layout archive format 6. `staleAfter`
uses Go duration syntax, for example `3m0s`. A camera provider should normally
refresh every 60 seconds with `staleAfter` set to 180 seconds.

Send one observation with:

```http
POST /api/v1/occupancy/observations
Authorization: Bearer <sensor access token>
Content-Type: application/json

{
  "providerId": "camera-yard",
  "sensorId": "zone-12",
  "state": "occupied",
  "sequence": 1842,
  "observedAt": "2026-09-17T17:12:42.382Z",
  "occupant": { "type": "locomotive", "id": "BB72084" }
}
```

Send a startup or periodic refresh with
`POST /api/v1/occupancy/snapshot`. A snapshot contains 1 to 256 observations,
is validated atomically, and is limited by the common 1 MiB JSON body limit.
An omitted configured sensor is missing, never implicitly `free`.

Accepted requests return `202`. An identical duplicate sequence is an accepted
no-op. Older sequences return `409 occupancy_sequence_stale`; a conflicting
duplicate returns `409 occupancy_sequence_conflict`; an unknown provider or
sensor returns `404 occupancy_sensor_not_found`; invalid content returns
`400 invalid_occupancy_observation`. `observedAt` is required and cannot be more
than five minutes in the future. Server receipt time controls freshness.

## Public runtime contracts

`GET /api/v1/blocks` exposes `occupancy.state`, optional `occupancy.occupant`,
and `occupancy.updatedAt`. The deprecated `occupied` boolean remains derived
from `state == occupied`; it must not be used for safety decisions.

Dispatcher and administrator accounts can inspect each configured source with
`GET /api/v1/blocks/{id}/occupancy-sources`. The response includes its current
state, sequence, required/priority settings, availability, and freshness. This
diagnostic endpoint never changes occupancy.

WebSocket clients receive `block.occupancy.changed` only when the aggregated
state changes. Higher-sequence refreshes of the same state do not emit another
business event. Initial and requested `system.snapshot` messages contain the
same block occupancy object as REST, including after same-connection resync.

## Route safety semantics

- `occupied` blocks reject reservation and activation with
  `409 route_occupied`.
- `unknown` blocks reject reservation and activation with
  `409 route_occupancy_unknown`.
- only `free` permits the remaining conflict and turnout validations to run.

Activation revalidates occupancy before reading or commanding turnout
requirements. A rejected activation preserves route ownership and emits no
turnout command or `route.activated` event. Occupancy can still change while a
multi-turnout activation is in progress; full atomic resource locking remains
outside this lot.
