# Format des archives DCC Control

Versions actuelles : **7** pour `.dcclayout`, **6** pour `.dcclib`.
Les archives de circuit versions 1 à 6 restent importables.

Les extensions recommandées sont :

- `.dcclib` pour une bibliothèque de matériel roulant ;
- `.dcclayout` pour un circuit ;
- le contenu reste un fichier ZIP standard inspectable avec les outils habituels.

## Manifeste

Chaque archive contient obligatoirement `manifest.json` :

```json
{
  "format": "org.dcc-control.package",
  "version": 7,
  "packageType": "layout",
  "createdAt": "2026-07-29T20:00:00Z"
}
```

Chaque `linearAddress` d'endpoint doit être compris entre `1` et `2040`.
Les adresses `2041..2044` sont exclues de la plage portable TrainPilot.

`packageType` vaut `rolling-stock` ou `layout`. Un manifeste `rolling-stock` utilise la version 6 au maximum.
Un format, une version ou un type inconnu est refusé.

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
    "presentation": {
      "coordinateSystem": "layout-units",
      "gridSpacing": 20,
      "nodes": [
        { "nodeId": "boundary-west", "x": 0, "y": 0 },
        { "nodeId": "turnout-stem", "x": 100, "y": 0 }
      ],
      "trackSections": [
        {
          "trackSectionId": "approach-west",
          "segments": [
            { "type": "line", "to": { "x": 100, "y": 0 } }
          ]
        }
      ],
      "turnouts": [
        { "turnoutId": "turnout-1", "x": 100, "y": 0, "rotationDegrees": 0, "mirrored": false }
      ],
      "blocks": [
        { "blockId": "block-a", "color": "#33AADD", "opacity": 0.8 }
      ]
    },
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
    ],
    "occupancyProviders": [
      {
        "id": "camera-yard",
        "type": "vision",
        "priority": 90,
        "required": false,
        "staleAfter": "3m0s",
        "freshnessRequired": true
      }
    ],
    "occupancySensorMappings": [
      { "providerId": "camera-yard", "sensorId": "zone-12", "blockId": "block-a" }
    ]
  }
}
```

L’import vérifie les références logiques avant la transaction, puis valide la
présentation et ses références dans la transaction avant toute modification :
nœuds, sections, ports, connexions, cantons d’itinéraire, aiguillages,
positions logiques, conflits et mappings de rétrosignalisation. Lorsqu'une
route version 5 à 7 possède `entryNodeId` et `exitNodeId`, son chemin physique, ses
positions d'aiguillage et tous les blocks traversés sont aussi validés. Ces
champs restent optionnels pour les anciennes routes.

Les exports de circuit version 7 séparent configuration et état opérationnel. Ils ne
contiennent pas `desiredPosition`, `reportedPosition`, `pending`,
`reportedStatus`, `reportQuality`, `commandStatus` ni `occupied`.

`trackSectionIds` et `turnoutIds` décrivent les ressources physiques couvertes
par chaque block. Une ressource appartient au plus à un block et l'union des
ressources d'un block doit être connexe dans le graphe statique. Les anciens
`feedbackMappings` numériques restent importables et sont migrés vers
`occupancySensorMappings`. Les providers et mappings d'occupation sont de la
configuration ; les observations runtime ne sont jamais archivées.

Les archives versions 1 à 3 ne contiennent aucune topologie. Leur import crée
une topologie vide. Les archives versions 1 à 4 importent les anciens blocks
avec des memberships vides. TrainPilot ne déduit jamais des ressources depuis
les cantons d'itinéraire, car ces informations sont insuffisantes.
Les archives versions 1 à 6 sans `presentation` donnent une présentation vide
en mode `replace`. En mode `merge`, une présentation absente conserve celle
du serveur.

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

Pour la présentation, `merge` remplace les éléments graphiques des identifiants
importés et conserve ceux absents de l'archive. `replace` remplace toute la
présentation. Les lignes graphiques des ressources logiques supprimées sont
effacées avec elles. L'export complet conserve les routes et les mappings de
rétrosignalisation si un éditeur ne modifie que la topologie, les aiguillages,
les cantons et la présentation.

## Validation avant publication

`POST /api/v1/layout/validate?mode=merge|replace` accepte la même archive
que l'import. Le rôle `administrator` est requis. Une réponse HTTP 200 contient
`valid`, `errors` et `warnings`. Chaque diagnostic porte un `code` stable, un
`message` et, si connu, `resourceType` et `resourceId`. Un document invalide
donne `valid: false` ; une requête non autorisée conserve le format `Problem`.
Les codes incluent `invalid_archive`, `topology_invalid`,
`block_membership_invalid`, `accessory_address_conflict`,
`layout_presentation_reference_invalid`, `layout_node_position_missing`,
`layout_track_path_invalid` et `layout_block_style_invalid`. Les avertissements
de route existants gardent leurs codes. Le serveur exécute les mêmes contrôles
que l'import dans une transaction annulée : aucune révision, donnée runtime,
commande DCC ni événement `layout.imported` ne change. Un import explicite reste
nécessaire après validation ; un état concurrent peut rendre sa validation
différente.
Pour les aiguillages, voir les exemples complets et les codes de validation
dans [`TURNOUTS.md`](TURNOUTS.md).

## Limites et sécurité

- archive complète : 25 Mio maximum ;
- entrée ZIP : 10 Mio maximum ;
- chemins absolus, remontées `..` et documents manquants refusés ;
- propriétés JSON inconnues refusées ;
- import autorisé uniquement au rôle applicatif `administrator` ;
- les imports réussis publient `rolling-stock.imported` ou `layout.imported` sur le WebSocket.

La présentation version 7 couvre les coordonnées des nœuds, les chemins de
voies (lignes et courbes cubiques), les placements d'aiguillages et les styles
des cantons. Zoom, position de la vue, images et état runtime ne sont pas archivés.
