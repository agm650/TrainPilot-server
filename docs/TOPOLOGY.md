# Physical railway topology

## Concepts

TrainPilot represents physical connectivity independently from occupancy
detection. A `TrackSection` describes fixed track on which a train can travel.
A `Block` describes an area whose occupancy can be detected. A block may cover
several track sections or turnouts, while a track section may be undetected.
`BlockDefinition` stores that static membership separately from the runtime
`Block.Occupied` state.

```text
physical path:  boundary -- TrackSection -- turnout -- TrackSection -- buffer
                                  \________ detection Block ________/
```

Topology support describes where a train could travel. It does not locate a
train, reserve a route, or decide whether movement is safe.

## Track sections, blocks, and physical resources

A block can own any number of track sections and turnouts. A physical resource
belongs to zero or one block: leaving it unassigned explicitly means that the
resource is not covered by a detection zone. Duplicate ownership is rejected.

The selected resources of one block must be connected in the static union
graph. Several branches around a turnout, including a double-slip crossing,
can therefore share one block. Two disjoint physical islands cannot.

`BlockForTrackSection`, `BlockForTurnout`, and `ResourcesForBlock` provide
direct inverse lookups in both the topology graph and SQLite store. A legacy
block with no resource membership remains valid.

Topology membership and feedback mapping have different meanings:

```text
Block -> physical resources
provider/address -> Block -> Occupied
```

Feedback mappings remain the only source of runtime occupancy. Importing block
configuration never restores or infers `Occupied`.

## Nodes and track sections

A `TopologyNode` is a physical connection point. Its kind is one of:

- `joint`: a normal connection;
- `buffer`: a physical dead end;
- `boundary`: the edge of the modeled railway.

A `TrackSection` joins exactly two different nodes. Track is physically
bidirectional in topology V1. The order of its two nodes does not impose an
operating direction. Parallel sections may join the same pair of nodes, and
cycles are valid. `lengthMm` is optional: zero means unknown and negative
values are invalid.

A fixed crossing without a junction is represented by two track sections that
do not share a node. A siding can end at a `buffer` node, while track leaving
the modeled area ends at a `boundary` node.

## Turnout ports

The physical topology of a turnout is separate from its DCC endpoints. Each
`TurnoutPort` attaches one physical port to a topology node. Each logical
turnout position declares the bidirectional port connections enabled in that
position. The topology engine therefore does not infer geometry from the
turnout kind.

## Simple turnouts

A simple turnout normally has `stem`, `straight`, and `diverging` ports. Its
straight position connects `stem` to `straight`; its diverging position
connects `stem` to `diverging`.

```text
                 straight
                /
stem -----------
                \
                 diverging
```

## Three-way turnouts

A three-way turnout normally has `stem`, `left`, `straight`, and `right` ports.
Each of its three logical positions connects the stem to the selected branch.
No fourth topology position is implied.

## Double-slip and single-slip crossings

Double-slip crossings (TJD) use four ports. One logical position may enable
multiple connections simultaneously, for example `a` to `c` and `b` to `d`.
Single-slip crossings (TJS) use the same model. Their actual connection tables
belong to the layout definition and depend on the device and wiring; they are
not hard-coded by turnout kind.

Every logical position of a turnout with a declared `TurnoutTopology` must have
one topology definition. Unknown positions, ports, nodes, duplicate undirected
connections, and incomplete position tables are rejected by
`ValidateTopologyDefinition`.

## Import, export, and persistence

SQLite stores nodes, track sections, turnout ports, positions, and connections
in normalized tables. Export order is deterministic: resources are ordered by
ID, while ports, positions, and connections preserve their declared order.
Referenced nodes and turnouts cannot be deleted implicitly. A complete layout
replacement removes their topology explicitly within the same transaction.

Layout archive version 7 stores graphical `presentation` alongside block
`trackSectionIds` and `turnoutIds`, `nodes`, `trackSections`, and
`turnoutTopologies`. Versions 1 through 6 remain importable. Blocks from
versions 1 through 4 have empty resource memberships. Versions 1 through 3
also produce an empty topology. TrainPilot never infers physical membership
from route block references during database or archive migration.

## Legacy compatibility

Layouts and databases created before topology support remain valid. Their
topology and block resource memberships are empty until explicitly configured.
Legacy single-address turnouts are normalized to simple logical turnouts, but
TrainPilot never guesses track connections from DCC addresses, route blocks,
or turnout kinds.

## Static physical graph

`internal/topology.Build` creates an indexed in-memory graph after validating
the complete topology definition. Each track section becomes a bidirectional
fixed edge carrying its section ID and length. Each turnout connection becomes
a conditional edge carrying the turnout ID, port pair, and every logical
position in which that connection is possible.

The static graph is the union of all declared turnout positions. It describes
every physically possible connection, not the connections enabled by the
current reported state.

Nodes, sections, turnout topologies, blocks, resource owners, and incident
edges have direct indexes.
All public graph enumerations are deterministic. `ConnectedComponents`
identifies independent areas, including isolated nodes. A fixed crossing has
two separate components when its two tracks share no node. Cycles and parallel
sections are valid.

The builder applies these structural constraints:

- a `joint` with more than two fixed sections must also be a turnout port;
- a `buffer` has at most one fixed section and cannot be a turnout port;
- a `boundary` has at most one fixed section in topology V1.

Layouts needing several connections at a boundary must model an explicit
internal `joint` and connect that joint to the boundary with one section.

## Static graph vs active graph

`Graph.ActiveView` creates an immutable snapshot without changing the static
graph. Fixed track-section edges are always present. A turnout edge is present
only when all these conditions hold:

- `Pending` is false;
- `ReportedStatus` is `known`;
- `ReportedPosition` names a position that enables the edge.

`DesiredPosition` is never used for connectivity. If desired and reported
positions differ, the active graph follows the reported position. A turnout
whose reported position is unknown, invalid, missing, or inconsistent with its
topology provides no confirmed internal connection.

The report quality (`assumed`, `station`, or `physical`) does not block a known
position in topology V1. It remains attached to each active turnout edge so a
future safety policy can distinguish its evidence source.

Active views are computed directly from the supplied turnout states. There is
no shared cache or event-driven invalidation. A new reported state is visible
on the next `ActiveView` call, while an existing view remains unchanged.

## Unknown and pending turnout states

`unknown`, missing, invalid, and `pending` reports enable no internal turnout
connection. Fixed track remains present. This fail-closed graph behavior does
not send a stop command and does not replace runtime route safety checks.

## Queries and physical pathfinding

Both static and active graphs expose deterministic queries for neighboring
nodes, track sections at a node, adjacent sections, resources at a node, and
adjacent resources. Block ownership remains available through the resource
indexes described above.

`FindPath` uses breadth-first search and minimizes the number of traversals.
This V1 cost model is explicitly named `traversal_count`. It does not compare
millimeters because a zero section length means unknown, not zero distance.
A future cost model can therefore replace traversal count without changing the
orientation or resource metadata returned by a path.

Every traversal records its `from` and `to` nodes. A track section therefore
remains bidirectional while its use in one path is oriented. A turnout
traversal also records its entry and exit ports. Static paths return a
deterministic required position plus every compatible position; requirements
are intersected if the same turnout is encountered more than once. Active
paths return the confirmed reported position and its report quality.

Static pathfinding can use connections from any declared turnout position. An
active path uses only connections present in the immutable `ActiveView`; an
unknown, pending, or invalid turnout can therefore make a static path
unavailable in the active graph. Neither variant sends commands.

Callers can exclude track sections, turnouts, or blocks with
`PathConstraints`. Block occupancy is never read or converted automatically
into an exclusion. Reservation and interlocking policies remain the caller's
responsibility. Neighbor order is stable, cycles are bounded by visited search
states, and the same graph and constraints produce the same path.

## Reference fixtures

`internal/model/topologyfixture` provides stable layouts for regression tests:

| Fixture | Purpose |
| --- | --- |
| `simple-line` | two sections between a buffer and a boundary |
| `passing-loop` | direct and passing tracks between two simple turnouts |
| `three-way-yard` | three-way turnout and three buffer stops |
| `double-slip-station` | four approaches around a TJD |
| `fixed-crossing` | two crossing tracks with no physical connection |
| `multi-section-block` | one block spanning sections and a turnout |
| `undetected-section` | valid track with no detection block |
| `loop` | a complete cycle |
| `conceptual-five-detection-zones` | five planned TrainPilot detection zones |

The conceptual fixture represents the documented three outer and two inner
zones as five disconnected sections. It asserts no unconfirmed geometry,
turnout, or physical connection. Replace it only from an observed track plan.

## Public API, revision, and CLI

Every authenticated role can read the complete static definition with
`GET /api/v1/topology`. The response contains canonical, deterministically
ordered `nodes`, `trackSections`, `turnoutTopologies`, and block resource
membership. It references turnout IDs and does not duplicate DCC endpoints,
reported positions, pending commands, or occupancy.

The response also contains a `revision`: a lowercase SHA-256 of the canonical
topology JSON. The digest covers physical topology, names, and block membership.
It excludes runtime block and turnout state.

Every authenticated role can read the separate graphical definition with
`GET /api/v1/layout/presentation`. Its arrays are ordered by resource ID. Its
`revision` is a lowercase SHA-256 of canonical presentation JSON without the
revision field. It excludes physical topology, runtime state, zoom, and viewport
offset. Existing layouts return empty arrays, `layout-units`, and grid spacing
20. A presentation-only edit leaves `topologyRevision` unchanged.

`system.snapshot` carries `topologyRevision` and
`layoutPresentationRevision`, keeping resynchronization snapshots small.
Clients cache the two resources independently and reload only the one whose
revision changed. After `layout.imported`, they compare both revisions. That
event is published only after a successful layout transaction. V1 exposes no
topology CRUD; changes use the validated, atomic layout import pipeline.

`dccctl topology` prints counts and the static connected-component count.
`dccctl topology --json` emits the canonical response. `dccctl topology
validate` rebuilds the graph against the referenced turnout definitions,
verifies the revision, and exits non-zero on failure.

## Topological route validation

`RouteDefinition` may declare optional `entryNodeId` and `exitNodeId`. Routes
without either field remain compatible and keep their previous validation.
When one endpoint is present, both are required.

For a route with endpoints, configuration validation constrains the static
graph with the declared turnout positions and finds a continuous physical path.
It derives every traversed track section, turnout, and detection block. A
traversed block absent from `blockIds`, or a required turnout position absent
from `turnoutStates`, rejects the layout before its database transaction.

Additional blocks and turnouts remain allowed as intentional protection. They
produce warnings. Two routes that share a physical resource or traversed block
also produce a warning for each missing directional `conflictRouteIds` entry.
The validator does not add conflicts automatically.

Stable diagnostics are:

| Severity | Code | Meaning |
| --- | --- | --- |
| error | `route_no_path` | endpoints are missing, unknown, or disconnected |
| error | `route_missing_block` | a traversed block is not protected |
| error | `route_missing_turnout_position` | a required turnout or position is absent or incompatible |
| warning | `route_extra_block` | an additional non-traversed block is protected |
| warning | `route_extra_turnout` | an additional non-traversed turnout is locked |
| warning | `route_possible_undeclared_conflict` | shared resources lack a directional conflict declaration |

Topological validation is not atomic runtime reservation. It validates imported
configuration only. `RouteService.Reserve` and `RouteService.Activate` retain
their existing behavior, including the immediate P0 occupancy/conflict
revalidation before the first physical turnout command. No SQLite transaction
is held during hardware confirmation waits.

## Limitations of topology V1

Topology V1 stores logical connectivity only. Graphical presentation is stored
separately in abstract layout units: node positions, line and cubic track paths,
turnout placement, and block styling. It does not change physical connectivity.
The coordinate system is `layout-units`; omitted grid spacing defaults to 20
layout units and defines the layout grid interval. Zoom and viewport offsets
stay in the client. Block colors use `#RRGGBB`, with opacity from 0 to 1.
The store persists this presentation, exposes it through
`GET /api/v1/layout/presentation`, and exports it in `.dcclayout` version 7.
Topology V1 has no operating direction,
signaling rules, resource reservation, train location, or progressive route
release. Physical pathfinding is descriptive only and makes no operating or
safety decision.

## Preparation for train localization

Topology V1 supplies the physical resources and detection memberships needed
by a future localization layer. That layer must still define train identity,
direction, length, ambiguous occupancy, initialization, and recovery after
missing feedback. It must not infer an exact section from a multi-resource
block without additional evidence.

The planned order is train localization first, then safe route interlocking
based on atomic resource reservations, then signaling. Real z21/R-BUS behavior
and the five-zone physical layout remain hardware validation work.
