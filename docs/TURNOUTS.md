# Aiguillages et accessoires DCC

Dernière mise à jour : 31 août 2026.

Ce document décrit le modèle canonique des aiguillages de TrainPilot.
Il couvre les appareils simples et composés.

Les contrats machine-readable restent :

- [`../api/openapi.yaml`](../api/openapi.yaml) pour HTTP ;
- [`../api/asyncapi.yaml`](../api/asyncapi.yaml) pour les snapshots et événements ;
- [`ARCHIVE_FORMAT.md`](ARCHIVE_FORMAT.md) pour les archives de circuit.

## 1. Vocabulaire

Un `Turnout` est un appareil de voie logique.
Il possède une ou plusieurs positions ferroviaires nommées.

Un `AccessoryEndpoint` est une sortie binaire physique.
Il correspond à une adresse linéaire DCC.

Une `TurnoutPositionDefinition` associe une position logique à un vecteur
complet d'endpoints.

Exemple :

```text
position logique "left"
  A = position2
  B = position1
```

La géométrie reste dans le `Turnout`.
Les endpoints utilisent seulement `position1` et `position2`.

## 2. Adresse linéaire canonique

TrainPilot utilise une adresse d'accessoire linéaire comprise entre `1` et
`2040` inclus. Cette plage commune évite les adresses `2041..2044`, réservées
à la diffusion par DCC-EX. Cette valeur est indépendante du pilote.

Chaque pilote convertit cette adresse vers son protocole.
Il valide aussi la borne réellement supportée par ce protocole.
Il ne faut pas appliquer un décalage propre à un constructeur dans les données
TrainPilot.

Certains matériels affichent une adresse de décodeur et une sortie de 1 à 4.
D'autres affichent directement une adresse linéaire.
Un décalage par groupe de quatre peut donc apparaître.

Procédure de diagnostic :

1. commander une seule sortie connue ;
2. noter l'adresse affichée par le fabricant ;
3. vérifier sa convention de numérotation ;
4. convertir une seule fois vers l'adresse linéaire TrainPilot ;
5. ne pas ajouter un second décalage dans le pilote ou le client.

## 3. Positions binaires

Les seules valeurs physiques sont :

```text
position1
position2
```

Elles ne signifient pas directement `straight` ou `diverging`.
Le câblage détermine la géométrie obtenue.

Un endpoint peut avoir `inverted=true`.
Dans ce cas :

```text
position1 logique -> position2 envoyée au pilote
position2 logique -> position1 envoyée au pilote
```

La définition des positions métier ne change pas.
La résolution d'un retour physique applique l'inversion inverse.

## 4. Abstraction station

`station.CommandStation` reçoit une commande binaire typée :

```go
station.AccessoryCommand{
    Address:  12,
    Position: station.AccessoryPosition2,
}
```

La méthode s'appelle `SetBasicAccessory`.
Elle reste distincte d'un futur contrat d'accessoire étendu pour les signaux.
Une adresse ou une position invalide produit respectivement
`station.ErrInvalidAccessoryAddress` ou
`station.ErrInvalidAccessoryPosition`.

| Niveau | Exemple | Sens |
| --- | --- | --- |
| logique | `left`, `straight`, `route_a` | position métier du turnout |
| station | `position1`, `position2` | choix binaire indépendant de la géométrie |
| protocole z21 | `P=0`, `P=1` | choix de la sortie 1 ou 2 |
| DCC-EX | `activate=0`, `activate=1` | commande binaire à l'adresse linéaire |

Le provider facultatif `station.AccessoryStateEventProvider` publie :

- l'adresse linéaire ;
- la position binaire lorsqu'elle est connue ;
- l'état du rapport `known`, `unknown` ou `invalid` ;
- l'heure d'observation ;
- la qualité `station`, `assumed` ou `physical`.

`station` signifie que la centrale rapporte son état de fonction.
`assumed` signifie que l'état est déduit d'une commande ou d'un écho.
`physical` est réservé à un capteur mécanique réel.
Aucun de ces niveaux ne doit être promu en preuve physique sans capteur adapté.

La capability historique `accessoryControl` reste exposée pour compatibilité.
La présence de retours s'observe côté serveur par le provider facultatif ; elle
n'impose pas à un driver de fabriquer des événements.

Le simulateur expose ce provider avec une file de 64 événements.
La publication ne bloque jamais une commande. Si la file est pleine, le nouvel
événement est abandonné ; le snapshot du simulateur permet la resynchronisation.

Le pilote z21 expose le même provider. Sa qualité est toujours `station` : la
centrale confirme son état de fonction, pas la position mécanique des lames.

## 5. Modèle JSON

```json
{
  "id": "turnout-1",
  "name": "Aiguille entrée",
  "kind": "simple",
  "endpoints": [
    {
      "id": "main",
      "linearAddress": 1,
      "inverted": false
    }
  ],
  "positions": [
    {
      "id": "straight",
      "label": "Directe",
      "endpoints": { "main": "position1" }
    },
    {
      "id": "diverging",
      "label": "Déviée",
      "endpoints": { "main": "position2" }
    }
  ],
  "desiredPosition": "straight",
  "reportedPosition": "straight",
  "pending": false
}
```

`kind` accepte :

- `simple` ;
- `three_way` ;
- `double_slip` ;
- `single_slip` ;
- `custom`.

Le `kind` aide le domaine et l'interface graphique.
Il ne définit jamais les vecteurs valides.
La liste `positions` reste la source de vérité.

## 6. États courants

`desiredPosition` contient la position logique demandée.

`reportedPosition` contient la position résolue depuis les endpoints.
Une chaîne vide signifie que la combinaison est inconnue ou invalide.

`pending=true` signifie qu'une transition attend encore sa confirmation.

`unknown` et `invalid` sont réservés à la qualité d'état.
Ils ne peuvent pas être déclarés comme identifiants de positions normales.

Les champs historiques suivants restent présents pour un aiguillage simple :

```text
dccAddress
desiredState
reportedState
```

Ils sont dépréciés.
Les appareils composés ne les exposent pas.

## 7. Aiguillage simple

```text
straight  -> main=position1
diverging -> main=position2
```

Un aiguillage enroulé, symétrique ou courbe reste généralement `simple`.
Ses libellés peuvent être adaptés à l'interface.

## 8. Aiguillage triple

```json
{
  "id": "T3",
  "name": "Aiguillage triple",
  "kind": "three_way",
  "endpoints": [
    { "id": "A", "linearAddress": 20 },
    { "id": "B", "linearAddress": 21 }
  ],
  "positions": [
    {
      "id": "left",
      "endpoints": { "A": "position2", "B": "position1" }
    },
    {
      "id": "straight",
      "endpoints": { "A": "position1", "B": "position1" }
    },
    {
      "id": "right",
      "endpoints": { "A": "position1", "B": "position2" }
    }
  ],
  "desiredPosition": "straight",
  "reportedPosition": "",
  "pending": false
}
```

La combinaison suivante n'est pas déclarée :

```text
A=position2, B=position2
```

Elle ne peut pas être commandée comme position logique.
Si elle est observée, `reportedPosition` reste vide.

## 9. Traversée-jonction double

Une TJD peut déclarer quatre positions génériques :

```text
route_a -> A=position1, B=position1
route_b -> A=position1, B=position2
route_c -> A=position2, B=position1
route_d -> A=position2, B=position2
```

Ces noms ne décrivent pas une géométrie universelle.
Le câblage, la motorisation et le fabricant déterminent la correspondance.
La configuration explicite doit toujours être vérifiée sur le réseau concerné.

## 10. Traversée-jonction simple

Une TJS peut avoir deux endpoints sans utiliser les quatre combinaisons.

Exemple :

```text
route_a -> A=position1, B=position1
route_b -> A=position1, B=position2
route_c -> A=position2, B=position1
```

La combinaison absente reste invalide.
Aucun traitement spécial n'est nécessaire dans le modèle.

## 11. Appareil personnalisé

`custom` accepte un nombre quelconque d'endpoints.

Exemple à trois endpoints :

```text
position one -> A=position1, B=position1, C=position1
position two -> A=position2, B=position1, C=position2
```

Chaque position doit définir tous les endpoints.
Deux positions ne peuvent pas partager le même vecteur.

## 12. Validation

Une définition est refusée si :

- l'identifiant ou le nom est vide ;
- le `kind` est inconnu ;
- aucun endpoint ou aucune position n'est défini ;
- une adresse est hors de la plage portable `1..2040` ;
- deux endpoints partagent un identifiant ou une adresse ;
- une position référence un endpoint absent ;
- une position omet un endpoint ;
- une valeur diffère de `position1` ou `position2` ;
- deux positions ont le même identifiant ;
- deux positions correspondent au même vecteur ;
- l'état demandé ou rapporté référence une position absente.

Les erreurs incluent l'identifiant de l'appareil et la cause exploitable.

## 13. Persistance et migration

Les définitions sont normalisées dans quatre ensembles :

```text
turnouts
turnout_endpoints
turnout_positions
turnout_position_endpoints
```

Les enfants utilisent des clés étrangères et `ON DELETE CASCADE`.
Les ordres des endpoints et positions sont persistés.

La migration d'une ancienne base est automatique et idempotente.
Un ancien aiguillage devient :

```text
kind=simple
endpoint main à l'ancienne adresse
straight  -> main=position1
diverging -> main=position2
```

Une ancienne valeur `unknown` devient une position rapportée vide.

## 14. Archives

Les exports utilisent le format d'archive version 2.
Ils sont déterministes pour un état et un timestamp identiques.

Les archives version 1 restent importables.
Leur modèle à une adresse est converti en aiguillage simple.

## 15. Confirmation et sécurité

`reportedPosition` indique ce que TrainPilot peut résoudre depuis les retours
disponibles.

Une confirmation de centrale ne garantit pas toujours la position physique des
lames.
Cette garantie exige une détection physique adaptée.

Une combinaison inconnue ne devient jamais une position inventée.
Une transition partiellement exécutée ne doit jamais être déclarée réussie.
Une ancienne confirmation ne doit pas écraser une commande récente.

Le service compose déjà les endpoints dans l'ordre déclaré et résout leurs
rapports. La sérialisation avancée, le rollback après succès partiel et les
transitions sûres seront ajoutés par les tickets suivants du lot.

## 16. Simulation et appareils composés

Le simulateur travaille uniquement au niveau des endpoints physiques. Son état
utilise `position1` et `position2`, jamais les noms logiques du turnout.

Une confirmation immédiate ou différée publie un
`station.AccessoryStateEvent` de qualité `physical`. Une injection externe peut
utiliser `station`, `assumed` ou `physical`. Elle modifie `Reported`, conserve
`Desired` et annule toute confirmation retardée devenue obsolète.

Pour un triple, le service résout les rapports reçus :

```text
A=position2, B=position1 -> left
A=position1, B=position1 -> straight
A=position1, B=position2 -> right
A=position2, B=position2 -> position rapportée vide, pending=true
```

Le simulateur accepte volontairement le dernier vecteur. Il décrit un état
physique possible, même s'il est interdit par la définition logique.

Un fault `accessory` peut cibler une adresse. Il permet de faire réussir A puis
échouer B sans hasard et sans rejeu.

## 17. Hors modèle

Les ponts tournants, plaques tournantes et traversers ne sont pas des
`Turnout`.

Ils ont des positions indexées nombreuses, une durée de mouvement, un
verrouillage et parfois une occupation propre.
Ils nécessitent une famille métier distincte.

## 18. Protocole accessoire z21

Le pilote convertit l'adresse linéaire canonique avec :

```text
FAdr = linearAddress - 1
linearAddress = FAdr + 1
```

Exemples de référence :

| Adresse TrainPilot | FAdr z21 |
| ---: | ---: |
| 1 | `0x0000` |
| 4 | `0x0003` |
| 5 | `0x0004` |
| 8 | `0x0007` |
| 9 | `0x0008` |
| 2040 | `0x07F7` |

Une commande `LAN_X_SET_TURNOUT` utilise `Q=1`. Le bit `P` vaut `0` pour
`position1` et `1` pour `position2`. Le bit d'activation `A` vaut d'abord `1`,
puis `0` après `station.accessoryPulse`, qui vaut `100ms` par défaut. Une
annulation cliente ne supprime pas la tentative de désactivation de sécurité.

Après l'impulsion, le pilote envoie `LAN_X_GET_TURNOUT_INFO`. Il corrèle les
réponses par `FAdr` et traite aussi les broadcasts externes. Le champ z21 `ZZ`
est interprété sans approximation :

| ZZ | État TrainPilot | Position |
| --- | --- | --- |
| `00` | `unknown` | aucune |
| `01` | `known` | `position1` |
| `10` | `known` | `position2` |
| `11` | `invalid` | aucune |

La capability `accessoryControl` du pilote z21 est active. Les paquets exacts,
les limites d'adresse, les réponses concurrentes, broadcasts, annulations et
refus hors ligne sont couverts par un faux serveur UDP. Une validation sur
centrale réelle reste requise avant de considérer l'adressage affiché par un
constructeur ou la position mécanique comme confirmés.
