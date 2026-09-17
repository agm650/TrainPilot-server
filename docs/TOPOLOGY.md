# Physical railway topology

TrainPilot represents physical connectivity independently from occupancy
detection. A `TrackSection` describes fixed track on which a train can travel.
A `Block` describes an area whose occupancy can be detected. A block may cover
several track sections or turnouts, while a track section may be undetected.
The association between these resources is outside the TOP-001 model.

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

## Scope of topology V1

Topology V1 stores logical connectivity only. It has no screen coordinates,
curves, radii, drawing geometry, operating direction, signaling rules,
pathfinding, resource reservation, train location, or progressive route
release. Those layers can use this graph without changing its physical model.
