# API de test du simulateur

Cette API pilote le banc virtuel TrainPilot depuis un processus externe. Elle
ne fait pas partie de l'API publique de production et n'est pas décrite dans
`api/openapi.yaml`.

## Activation et sécurité

Les routes existent uniquement lorsque les deux conditions suivantes sont
réunies :

```json
{
  "station": {
    "driver": "simulator"
  },
  "testAPI": true
}
```

Si `testAPI` vaut `false`, ou si le pilote actif n'est pas `simulator`, toutes
les routes `/test/v1/simulator/...` retournent `404 Not Found`. Elles ne peuvent
donc pas modifier une z21 ou une centrale DCC-EX. Toutes les routes enregistrées
exigent un access token TrainPilot valide :

```http
Authorization: Bearer <access-token>
```

Les corps JSON sont limités à 1 Mio, refusent les champs inconnus et doivent
contenir exactement une valeur JSON. Les erreurs utilisent
`application/problem+json` comme l'API principale.

## État et reset

### `GET /test/v1/simulator/state`

Retourne l'état complet du banc : connexion, connectivité, puissance, arrêt
d'urgence, locomotives, accessoires, comportements d'accessoire, télémétrie,
capteurs physiques, faults actifs et scénario chargé.

Les maps de locomotives et accessoires utilisent l'adresse DCC comme clé JSON.
Les capteurs sont une liste triée par `source`, `kind`, puis `address`. Les
durées des faults, comportements et scénarios utilisent une chaîne Go telle que
`0s`, `500ms` ou `1m0s`.

### `POST /test/v1/simulator/reset`

Arrête et décharge le scénario courant, puis appelle le reset déterministe du
simulateur. La connexion du driver est conservée, tandis que puissance, arrêt
d'urgence, locomotives, accessoires, feedbacks et faults sont effacés.

Réponse : `204 No Content`.

## Connectivité et télémétrie

### `PUT /test/v1/simulator/connectivity`

```json
{
  "connectivity": "degraded"
}
```

Valeurs autorisées : `online`, `degraded`, `offline`.

### `PUT /test/v1/simulator/electrical`

Remplace atomiquement l'état électrique simulé. Les champs absents prennent
leur valeur zéro ; envoyer tous les champs utiles à l'essai.

```json
{
  "mainCurrentMilliAmps": 327,
  "programmingCurrentMilliAmps": 0,
  "filteredMainCurrentMilliAmps": 300,
  "temperatureCelsius": 42,
  "supplyVoltageMilliVolts": 17950,
  "trackVoltageMilliVolts": 17890,
  "programmingMode": false,
  "highTemperature": false,
  "powerLost": false,
  "externalShortCircuit": true,
  "internalShortCircuit": false
}
```

Ces opérations retournent `204 No Content`.

## Feedback physique

### `PUT /test/v1/simulator/feedback`

```json
{
  "source": "simulator",
  "kind": "occupancy",
  "address": 12,
  "active": true,
  "emit": true
}
```

Avec `emit: true`, l'état physique est modifié et un
`station.FeedbackEvent` est livré au flux normal. `RailwayService` applique alors
le mapping, met à jour le canton et publie `block.occupancy.changed` sur le
WebSocket. La livraison est atomique : un buffer saturé produit une erreur et
ne modifie pas le capteur.

Avec `emit: false`, seul l'état physique est modifié. Ce mode représente un
message perdu.

## Accessoires

### `PUT /test/v1/simulator/accessories/{address}/reported-state`

```json
{
  "state": "straight"
}
```

Valeurs : `straight`, `diverging`, `unknown`.

### `PUT /test/v1/simulator/accessories/{address}/behavior`

Confirmation différée :

```json
{
  "mode": "delayed",
  "delay": "500ms"
}
```

Retour incohérent :

```json
{
  "mode": "inconsistent",
  "reportedState": "straight"
}
```

Modes : `immediate`, `delayed`, `no_confirmation`, `inconsistent`.

## Faults d'opération

### `PUT /test/v1/simulator/faults/{operation}`

```json
{
  "delay": "500ms",
  "remaining": 2,
  "error": "injected_failure"
}
```

Opérations : `status`, `track_power`, `emergency_stop`, `throttle`, `function`,
`accessory`. `remaining: 0` conserve le fault jusqu'à son effacement ou au
reset. Une durée positive, une erreur, ou les deux sont obligatoires.

### `DELETE /test/v1/simulator/faults`

Efface tous les faults et invalide les opérations artificiellement retardées.

## Scénarios

### Charger

```http
POST /test/v1/simulator/scenarios
Content-Type: application/json
```

Le body est directement un scénario JSON v1, et non un chemin de fichier :

```json
{
  "version": 1,
  "name": "offline-recovery",
  "initial": {
    "connectivity": "online"
  },
  "steps": [
    {
      "at": "5s",
      "action": "station.connectivity",
      "connectivity": "degraded"
    },
    {
      "at": "10s",
      "action": "station.connectivity",
      "connectivity": "offline"
    }
  ]
}
```

La réponse `201 Created` expose l'état `loaded`.

### Démarrer

```http
POST /test/v1/simulator/scenarios/start
```

L'API de test démarre toujours le scénario en mode manuel.

### Avancer

```http
POST /test/v1/simulator/scenarios/advance
Content-Type: application/json
```

```json
{
  "duration": "5s"
}
```

L'horloge logique avance immédiatement, sans `sleep`. Toutes les étapes échues
sont appliquées dans l'ordre du fichier.

### Arrêter

```http
POST /test/v1/simulator/scenarios/stop
```

Empêche l'application des étapes futures.

Les réponses de contrôle contiennent :

```json
{
  "name": "offline-recovery",
  "state": "running",
  "elapsed": "5s",
  "nextStep": 1,
  "stepCount": 2
}
```

Les états possibles sont `loaded`, `running`, `completed`, `stopped` et
`failed`. Une erreur de scénario est conservée dans le champ `error`.

### Scénarios de référence

Le répertoire `tests/simulator/scenarios/` contient les douze scénarios SIM-008
utilisés par la CI :

- `nominal-driving`, `emergency-stop` ;
- `station-degraded-recovery`, `station-offline-recovery`,
  `electrical-short-circuit` ;
- `feedback-single-block`, `feedback-multiple-blocks`, `feedback-bounce`,
  `feedback-event-loss` ;
- `accessory-confirmation-success`, `accessory-confirmation-timeout-base`,
  `accessory-wrong-confirmation`.

Le chargement HTTP transmet le contenu du document, jamais son chemin sur le
serveur. Après authentification et copie de l'access token :

```bash
export TRAINPILOT_TOKEN='<accessToken>'

curl -sS -X POST http://127.0.0.1:8080/test/v1/simulator/scenarios \
  -H "Authorization: Bearer $TRAINPILOT_TOKEN" \
  -H 'Content-Type: application/json' \
  --data-binary @tests/simulator/scenarios/feedback-bounce.json

curl -sS -X POST http://127.0.0.1:8080/test/v1/simulator/scenarios/start \
  -H "Authorization: Bearer $TRAINPILOT_TOKEN"

curl -sS -X POST http://127.0.0.1:8080/test/v1/simulator/scenarios/advance \
  -H "Authorization: Bearer $TRAINPILOT_TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{"duration":"1040ms"}'

curl -sS http://127.0.0.1:8080/test/v1/simulator/state \
  -H "Authorization: Bearer $TRAINPILOT_TOKEN"
```

`TestReferenceSimulatorScenarios`, inclus dans `go test ./...`, valide les
documents et leurs intégrations HTTP/WebSocket sans port fixe ni attente longue.
Il couvre explicitement l'absence de rejeu après une période `offline`, les
feedbacks simultanés, le rebond, la perte volontaire et les trois bases de
confirmation d'accessoire.

## Route historique

`POST /test/v1/simulator/blocks/{id}/occupancy` reste disponible pour
compatibilité. Elle modifie directement le canton. Les nouveaux tests de
rétrosignalisation doivent préférer `PUT /test/v1/simulator/feedback`, qui
exerce le chemin réel capteur → mapping → canton → WebSocket.
