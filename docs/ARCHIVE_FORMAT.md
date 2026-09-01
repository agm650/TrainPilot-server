# Format des archives DCC Control

Version actuelle : **3**. Les archives versions 1 et 2 restent importables.

Les extensions recommandées sont :

- `.dcclib` pour une bibliothèque de matériel roulant ;
- `.dcclayout` pour un circuit ;
- le contenu reste un fichier ZIP standard inspectable avec les outils habituels.

## Manifeste

Chaque archive contient obligatoirement `manifest.json` :

```json
{
  "format": "org.dcc-control.package",
  "version": 3,
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
    "blocks": [
      { "id": "block-a", "name": "Gare voie 1", "occupied": false }
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

L’import vérifie toutes les références avant d’ouvrir la transaction d’écriture : cantons d’itinéraire, aiguillages, positions logiques, conflits et mappings de rétrosignalisation.

Les exports version 3 séparent configuration et état opérationnel. Ils ne
contiennent pas `desiredPosition`, `reportedPosition`, `pending`,
`reportedStatus`, `reportQuality` ni `commandStatus`.

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

Les ressources graphiques et images ne sont pas encore définies dans la version 3.
