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
until the future aggregation service receives sufficient current observations.

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
- `trainpilot_occupancy_source_stale`.

Station adapters, external observation APIs, and public runtime contracts are
implemented by OCC-003 through OCC-005.
