# Format des archives DCC Control

Version actuelle : **5**. Les archives versions 1 à 4 restent importables.

Les extensions recommandées sont :

- `.dcclib` pour une bibliothèque de matériel roulant ;
- `.dcclayout` pour un circuit ;
- le contenu reste un fichier ZIP standard inspectable avec les outils habituels.

## Manifeste

Chaque archive contient obligatoirement `manifest.json` :

```json
{
  "format": "org.dcc-control.package",
  "version": 5,
  "packageType": "rolling-stock",
  "createdAt": "2026-07-29T20:00:00Z"
}
```

Chaque `linearAddress` d'endpoint doit être compris entre `1` et `2040`.
Les adresses `2041..2044` sont exclues de la plage portable TrainPilot.

`packageType` vaut `rolling-stock` ou `layout`. Un format, une version ou un type inconnu est refusé.

## Bibliothèque de matériel

Une archive de type `rolling-stock` contient `rolling-stock.json` :

```json
{
  "locomotives": [
    {
      "id": "loco-bb26001",
      "name": "BB 26001",
      "dccAddress": 2601,
      "addressKind": "long",
      "speedSteps": 128,
      "manufacturer": "Jouef",
      "model": "BB 26000"
    }
  ]
}
```

Les identifiants doivent être stables. L’adresse DCC est validée dans l’intervalle 1–9999 et les pas de vitesse acceptés sont 14, 28 et 128.

## Circuit

Une archive de type `layout` contient `layout.json` :

```json
{
  "layout": {
    "nodes": [
      { "id": "boundary-west", "kind": "boundary" },
      { "id": "turnout-stem", "kind": "joint" },
      { "id": "turnout-straight", "kind": "joint" },
      { "id": "turnout-diverging", "kind": "joint" }
    ],
    "trackSections": [
      {
        "id": "approach-west",
        "name": "Approche ouest",
        "nodeAId": "boundary-west",
        "nodeBId": "turnout-stem",
        "lengthMm": 1200
      }
    ],
    "turnoutTopologies": [
      {
        "turnoutId": "turnout-1",
        "ports": [
          { "id": "stem", "nodeId": "turnout-stem" },
          { "id": "straight", "nodeId": "turnout-straight" },
          { "id": "diverging", "nodeId": "turnout-diverging" }
        ],
        "positions": [
          {
            "positionId": "straight",
            "connections": [
              { "portAId": "stem", "portBId": "straight" }
            ]
          },
          {
            "positionId": "diverging",
            "connections": [
              { "portAId": "stem", "portBId": "diverging" }
            ]
          }
        ]
      }
    ],
    "blocks": [
      {
        "id": "block-a",
        "name": "Gare voie 1",
        "trackSectionIds": ["approach-west"],
        "turnoutIds": ["turnout-1"]
      }
    ],
    "turnouts": [
      {
        "id": "turnout-1",
        "name": "Aiguille entrée",
        "kind": "simple",
        "endpoints": [
          { "id": "main", "linearAddress": 1 }
        ],
        "positions": [
          {
            "id": "straight",
            "endpoints": { "main": "position1" }
          },
          {
            "id": "diverging",
            "endpoints": { "main": "position2" }
          }
        ]
      }
    ],
    "routes": [
      {
        "id": "route-a-b",
        "name": "Gare vers pleine voie",
        "entryNodeId": "boundary-west",
        "exitNodeId": "turnout-straight",
        "blockIds": ["block-a"],
        "turnoutStates": { "turnout-1": "straight" },
        "conflictRouteIds": []
      }
    ],
    "feedbackMappings": [
      { "provider": "z21-rbus", "address": 1, "blockId": "block-a" }
    ]
  }
}
```

L’import vérifie toutes les références avant d’ouvrir la transaction d’écriture :
nœuds, sections, ports, connexions, cantons d’itinéraire, aiguillages,
positions logiques, conflits et mappings de rétrosignalisation. Lorsqu'une
route version 5 possède `entryNodeId` et `exitNodeId`, son chemin physique, ses
positions d'aiguillage et tous les blocks traversés sont aussi validés. Ces
champs restent optionnels pour les anciennes routes.

Les exports version 5 séparent configuration et état opérationnel. Ils ne
contiennent pas `desiredPosition`, `reportedPosition`, `pending`,
`reportedStatus`, `reportQuality`, `commandStatus` ni `occupied`.

`trackSectionIds` et `turnoutIds` décrivent les ressources physiques couvertes
par chaque block. Une ressource appartient au plus à un block et l'union des
ressources d'un block doit être connexe dans le graphe statique. Le mapping de
feedback reste indépendant et demeure la source de l'occupation runtime.

Les archives versions 1 à 3 ne contiennent aucune topologie. Leur import crée
une topologie vide. Les archives versions 1 à 4 importent les anciens blocks
avec des memberships vides. TrainPilot ne déduit jamais des ressources depuis
les cantons d'itinéraire, car ces informations sont insuffisantes.

Les champs `dccAddress`, `desiredState` et `reportedState` des anciennes
archives sont acceptés. Ils sont dépréciés. Une archive version 1 est convertie
automatiquement vers un endpoint `main` et les positions `straight` et
`diverging`.

Le modèle complet des appareils composés est décrit dans
[`TURNOUTS.md`](TURNOUTS.md).

## Modes d’import

- `merge` crée ou met à jour les objets portant le même identifiant ;
- `replace` efface la bibliothèque correspondante puis importe le document dans une transaction unique ;
- un remplacement du parc est refusé lorsqu’une réservation de locomotive est encore `active` ou `stopping`.

## Limites et sécurité

- archive complète : 25 Mio maximum ;
- entrée ZIP : 10 Mio maximum ;
- chemins absolus, remontées `..` et documents manquants refusés ;
- propriétés JSON inconnues refusées ;
- import autorisé uniquement au rôle applicatif `administrator` ;
- les imports réussis publient `rolling-stock.imported` ou `layout.imported` sur le WebSocket.

Les ressources graphiques et images ne sont pas encore définies dans la version 5.
