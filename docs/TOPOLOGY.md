# Physical railway topology

TrainPilot represents physical connectivity independently from occupancy
detection. A `TrackSection` describes fixed track on which a train can travel.
A `Block` describes an area whose occupancy can be detected. A block may cover
several track sections or turnouts, while a track section may be undetected.
`BlockDefinition` stores that static membership separately from the runtime
`Block.Occupied` state.

## Detection blocks and physical resources

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

## Turnout ports and positions

The physical topology of a turnout is separate from its DCC endpoints. Each
`TurnoutPort` attaches one physical port to a topology node. Each logical
turnout position declares the bidirectional port connections enabled in that
position. The topology engine therefore does not infer geometry from the
turnout kind.

A simple turnout normally has `stem`, `straight`, and `diverging` ports. Its
straight position connects `stem` to `straight`; its diverging position
connects `stem` to `diverging`.

A three-way turnout normally has `stem`, `left`, `straight`, and `right` ports.
Each of its three logical positions connects the stem to the selected branch.
No fourth topology position is implied.

Double-slip crossings (TJD) use four ports. One logical position may enable
multiple connections simultaneously, for example `a` to `c` and `b` to `d`.
Single-slip crossings (TJS) use the same model. Their actual connection tables
belong to the layout definition and depend on the device and wiring; they are
not hard-coded by turnout kind.

Every logical position of a turnout with a declared `TurnoutTopology` must have
one topology definition. Unknown positions, ports, nodes, duplicate undirected
connections, and incomplete position tables are rejected by
`ValidateTopologyDefinition`.

## Persistence and layout archives

SQLite stores nodes, track sections, turnout ports, positions, and connections
in normalized tables. Export order is deterministic: resources are ordered by
ID, while ports, positions, and connections preserve their declared order.
Referenced nodes and turnouts cannot be deleted implicitly. A complete layout
replacement removes their topology explicitly within the same transaction.

Layout archive version 5 stores block `trackSectionIds` and `turnoutIds` with
`nodes`, `trackSections`, and `turnoutTopologies`. Versions 1 through 4 remain
importable. Their blocks have empty resource memberships. Versions 1 through 3
also produce an empty topology. TrainPilot never infers physical membership
from route block references during database or archive migration.

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

`topologyfixture.PassingStation` provides a realistic through station with a
main platform, passing loop, siding, two station throats, and a branch turnout.

## Public API, revision, and CLI

Every authenticated role can read the complete static definition with
`GET /api/v1/topology`. The response contains canonical, deterministically
ordered `nodes`, `trackSections`, `turnoutTopologies`, and block resource
membership. It references turnout IDs and does not duplicate DCC endpoints,
reported positions, pending commands, or occupancy.

The response also contains a `revision`: a lowercase SHA-256 of the canonical
topology JSON. The digest covers physical topology, names, and block membership.
It excludes runtime block and turnout state. `system.snapshot` carries only this
value as `topologyRevision`, keeping resynchronization snapshots small.

A client loads the topology through REST and caches it by revision. It reloads
when a later snapshot has a different `topologyRevision` or after
`layout.imported`. That event is published only after a successful layout
transaction. V1 intentionally exposes no topology CRUD; changes use the
validated, atomic layout import pipeline.

`dccctl topology` prints counts and the static connected-component count.
`dccctl topology --json` emits the canonical response. `dccctl topology
validate` rebuilds the graph against the referenced turnout definitions,
verifies the revision, and exits non-zero on failure.

## Scope of topology V1

Topology V1 stores logical connectivity only. It has no screen coordinates,
curves, radii, drawing geometry, operating direction, signaling rules,
resource reservation, train location, or progressive route release. Physical
pathfinding is descriptive only and makes no operating or safety decision.
