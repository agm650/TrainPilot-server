# Format des archives DCC Control

Version actuelle : **1**.

Les extensions recommandées sont :

- `.dcclib` pour une bibliothèque de matériel roulant ;
- `.dcclayout` pour un circuit ;
- le contenu reste un fichier ZIP standard inspectable avec les outils habituels.

## Manifeste

Chaque archive contient obligatoirement `manifest.json` :

```json
{
  "format": "org.dcc-control.package",
  "version": 1,
  "packageType": "rolling-stock",
  "createdAt": "2026-07-29T20:00:00Z"
}
```

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
        "dccAddress": 1,
        "desiredState": "straight",
        "reportedState": "straight"
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

L’import vérifie toutes les références avant d’ouvrir la transaction d’écriture : cantons d’itinéraire, aiguillages, conflits et mappings de rétrosignalisation.

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

Les ressources graphiques, images et migrations entre versions futures ne sont pas encore définies dans la version 1.
