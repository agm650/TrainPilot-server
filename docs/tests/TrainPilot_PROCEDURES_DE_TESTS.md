# TrainPilot-server — Procédures détaillées de test

**Document complémentaire à `TrainPilot_PLAN_DE_TESTS.md`**  
**Référence : branche `main`, 2 septembre 2026**

Ce document répond à la question : **« Que dois-je lancer exactement, que dois-je observer, et quand puis-je cocher la case ? »**

---

# 0. Règle générale

Il existe trois types de tests dans le plan :

1. **AUTO** : la preuve est un test Go existant.  
   Il ne faut pas reproduire manuellement ce que le test vérifie déjà ; il faut lancer le package ou la suite et conserver le résultat.

2. **SIM** : le test peut être exécuté contre `dccd` avec `station.driver=simulator`.  
   On utilise soit `dcc-api-conformance`, soit `dccctl`, soit l'API `/test/v1/simulator/...`.

3. **MATERIEL** : une commande logicielle ne suffit pas.  
   Il faut observer le train, l'aiguillage, la z21, le 10819 ou DCC-EX et consigner `PASS/FAIL/BLOCKED/NOT_TESTED`.

Pour chaque test, ce document donne :

```text
Commande canonique
Commande ciblée éventuelle
Action
Résultat attendu
Preuve à conserver
```

---

# 1. Préparer un serveur Simulator de recette

Utiliser une instance jetable.

## 1.1 Vérifier la configuration

Le `config.json` de développement du dépôt utilise le simulateur. Vérifier au minimum :

```json
{
  "station": {
    "driver": "simulator"
  }
}
```

Pour piloter le banc depuis HTTP :

```json
"testAPI": true
```

## 1.2 Démarrer le serveur

```bash
go run ./cmd/dccd serve --config config.json
```

ou avec les binaires de release :

```bash
./bin/dccd serve --config config.json
```

## 1.3 Créer les utilisateurs de recette

Si la base est vide :

```bash
printf '%s\n' 'correct-horse-1' |
  go run ./cmd/dccd user bootstrap \
    --socket /tmp/dccd-admin.sock \
    --username alice \
    --display-name 'Alice' \
    --role driver \
    --password-stdin
```

Deuxième utilisateur :

```bash
printf '%s\n' 'correct-horse-2' |
  go run ./cmd/dccd user add \
    --socket /tmp/dccd-admin.sock \
    --username bob \
    --display-name 'Bob' \
    --role driver \
    --password-stdin
```

Administrateur :

```bash
printf '%s\n' 'correct-horse-admin' |
  go run ./cmd/dccd user add \
    --socket /tmp/dccd-admin.sock \
    --username admin \
    --display-name 'Admin' \
    --role administrator \
    --password-stdin
```

Dispatcher si nécessaire :

```bash
printf '%s\n' 'correct-horse-dispatch' |
  go run ./cmd/dccd user add \
    --socket /tmp/dccd-admin.sock \
    --username dispatcher \
    --display-name 'Dispatcher' \
    --role dispatcher \
    --password-stdin
```

Vérifier :

```bash
go run ./cmd/dccd user list --socket /tmp/dccd-admin.sock
```

---

# 2. Gate logiciel

## 2.1 Tous les tests

### Commande

```bash
go test ./...
```

### Attendu

```text
exit code = 0
aucun FAIL
```

### Cocher

Toutes les cases `AUTO` couvertes par la suite peuvent rester considérées comme valides **pour le commit testé**.

### Preuve

```bash
go test ./... | tee /tmp/trainpilot-go-test.log
```

Conserver :

```text
commit Git
date
OS
fichier log
```

---

## 2.2 Sans CGO

```bash
CGO_ENABLED=0 go test ./...
```

Attendu : `exit 0`.

Cette commande vérifie que SQLite et les binaires restent compatibles avec le mode de build réellement distribué.

---

## 2.3 Race detector

```bash
go test -race ./...
```

Attendu :

```text
aucun "WARNING: DATA RACE"
exit 0
```

Ne pas considérer un test de concurrence comme validé si la commande fonctionnelle passe mais `-race` échoue.

---

## 2.4 Vet

```bash
go vet ./...
```

Attendu : aucune sortie d'erreur.

---

## 2.5 Couverture

```bash
go test -coverprofile=coverage.out ./...
go tool cover -func=coverage.out
```

Pour une vue HTML :

```bash
go tool cover -html=coverage.out -o coverage.html
```

Il n'existe pas actuellement de seuil absolu de recette à atteindre. Vérifier surtout qu'une modification ne fait pas disparaître la couverture d'un chemin de sécurité.

---

## 2.6 GoReleaser

```bash
goreleaser check
goreleaser release --snapshot --clean --skip=publish
```

Puis :

```bash
find dist -type f | sort
```

Vérifier la présence des trois binaires dans les artefacts :

```text
dccd
dccctl
dcc-api-conformance
```

---

# 3. Conformité API

# 3.1 Conformité passive — détail exact

Les cinq lignes du plan :

```text
Inventaire des routes cohérent avec OpenAPI
Méthodes HTTP correctes
Codes d'erreur publics stables
Erreurs internes masquées
Aucun endpoint HTTP public de création d'utilisateur
```

ne correspondent **pas** à cinq commandes manuelles indépendantes.

Il y a trois niveaux de vérification.

---

## 3.1.1 Inventaire des routes = serveur = OpenAPI

### Test automatique exact

```bash
go test ./cmd/dcc-api-conformance \
  -run '^TestPublicEndpointInventoryMatchesServerAndOpenAPI$' \
  -v
```

### Ce que ce test compare

Il extrait :

1. les routes publiques enregistrées dans :

```text
internal/api/server.go
```

2. l'inventaire de :

```text
cmd/dcc-api-conformance/inventory.go
```

3. les routes documentées dans :

```text
api/openapi.yaml
```

Il normalise les paramètres `{id}` en `{}` puis compare les trois listes.

### Attendu

```text
--- PASS: TestPublicEndpointInventoryMatchesServerAndOpenAPI
PASS
```

### Quand cocher

Cocher :

```text
Inventaire des routes cohérent avec OpenAPI
Méthodes HTTP correctes
```

si ce test est vert.

**Pourquoi les méthodes sont couvertes :** l'inventaire contient le couple `METHOD + PATH`, par exemple :

```text
GET    /api/v1/locomotives
POST   /api/v1/locomotives
PUT    /api/v1/locomotives/{}
DELETE /api/v1/locomotives/{}
```

Le test compare donc aussi les verbes HTTP.

---

## 3.1.2 Afficher l'inventaire lisible

Cette commande est un outil de diagnostic/revue :

```bash
go run ./cmd/dcc-api-conformance --list-endpoints
```

ou :

```bash
./bin/dcc-api-conformance --list-endpoints
```

Exemples de classes affichées :

```text
passive
active
configuration
websocket
```

Elle ne contacte pas le serveur.

Utilité :

- vérifier visuellement ce qui fait partie de l'API publique ;
- voir quels endpoints sont destructifs/actifs ;
- préparer une revue de changement API.

Conserver éventuellement :

```bash
go run ./cmd/dcc-api-conformance --list-endpoints \
  | tee /tmp/trainpilot-endpoints.txt
```

---

## 3.1.3 Conformité passive contre un serveur actif

### Commande

```bash
go run ./cmd/dcc-api-conformance \
  --server http://127.0.0.1:8080 \
  --user1 alice --pass1 correct-horse-1 \
  --user2 bob --pass2 correct-horse-2
```

### Ce que la commande vérifie réellement

Parmi les checks exécutés :

```text
system information exposes compatible API versions
health endpoint is reachable
protected endpoint rejects missing token
valid user can authenticate
second user can authenticate
refresh rotates the token pair
rotated access token is immediately rejected
authenticated client reads its identity
authenticated client lists locomotives
authenticated client lists blocks
authenticated client lists turnouts
authenticated client lists routes
authenticated client reads station status
authenticated client reads track-power status
unknown locomotive returns a structured not-found error
public API does not expose user creation
authenticated client exports rolling stock
non-administrator cannot import rolling stock
authenticated client exports layout
logout revokes the current session
logged-out access token is rejected
```

### Attendu

Uniquement :

```text
PASS ...
SKIP ...     # pour les familles opt-in non demandées
...
Result: N passed, 0 failed
```

Le process doit retourner :

```text
exit code 0
```

Conserver :

```bash
go run ./cmd/dcc-api-conformance ... \
  | tee /tmp/trainpilot-conformance-passive.log
```

---

## 3.1.4 Vérifier manuellement « endpoint utilisateur absent »

Ce point est déjà dans `dcc-api-conformance`, mais on peut le diagnostiquer manuellement.

### Login

```bash
curl -sS http://127.0.0.1:8080/api/v1/auth/login \
  -H 'Content-Type: application/json' \
  -d '{
    "username":"alice",
    "password":"correct-horse-1",
    "clientId":"manual-api-test",
    "clientName":"manual-api-test",
    "platform":"cli"
  }'
```

Copier `accessToken` :

```bash
export TRAINPILOT_TOKEN='...'
```

### Tentative interdite

```bash
curl -i -X POST http://127.0.0.1:8080/api/v1/users \
  -H "Authorization: Bearer $TRAINPILOT_TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{"username":"forbidden"}'
```

### Attendu

```text
HTTP/1.1 404 Not Found
```

**Pas 401, pas 403, pas 201.**

L'objectif est de prouver que la route n'existe pas, même pour un utilisateur authentifié.

---

## 3.1.5 Codes d'erreur structurés

### Cas simple : ressource inconnue

```bash
curl -i \
  http://127.0.0.1:8080/api/v1/locomotives/conformance-unknown-locomotive \
  -H "Authorization: Bearer $TRAINPILOT_TOKEN"
```

### Attendu

```text
HTTP 404
Content-Type: application/problem+json
```

Le body doit contenir notamment :

```json
{
  "status": 404,
  "category": "not_found",
  "code": "locomotive_lookup_failed"
}
```

Le détail peut évoluer, mais `status`, `category` et `code` sont des éléments contractuels.

---

## 3.1.6 Erreurs internes masquées

Il n'est **pas recommandé de provoquer artificiellement une panne SQLite sur une instance normale** juste pour ce test.

### Preuve canonique

```bash
go test ./internal/api
```

et, globalement :

```bash
go test ./...
```

Les tests API vérifient la traduction des erreurs et l'absence de fuite des détails internes.

### Vérification manuelle raisonnable

Pour les erreurs accessibles normalement :

- aucun SQL ;
- aucun chemin local ;
- aucune stack trace Go ;
- aucun secret ;
- aucun message interne du driver brut inutile.

Si un `500` est observé pendant une autre campagne, conserver la réponse et vérifier qu'elle respecte :

```text
application/problem+json
category=internal
```

sans révéler la cause interne.

---

# 3.2 Conformité active Simulator

## Précondition

Serveur jetable :

```text
station.driver=simulator
testAPI=true
```

Au moins une locomotive doit exister.

### Commande

```bash
go run ./cmd/dcc-api-conformance \
  --server http://127.0.0.1:8080 \
  --user1 alice --pass1 correct-horse-1 \
  --user2 bob --pass2 correct-horse-2 \
  --admin admin --admin-pass correct-horse-admin \
  --allow-active-commands \
  --allow-configuration-mutations
```

### Attendu

`0 failed`.

### Ce qui est réellement commandé

La suite active vérifie notamment :

- power on ;
- acquisition ;
- seconde acquisition refusée ;
- throttle ;
- fonctions ;
- emergency stop ;
- inhibition après E-stop ;
- power on pour lever l'interlock ;
- throttle zéro ;
- release ;
- CRUD temporaire ;
- import/export temporaire.

### Ne jamais utiliser

Sur une vraie centrale :

```text
--allow-active-commands
--allow-configuration-mutations
```

sans décision explicite.

---

# 3.3 Conformité aiguillages

## Précondition

Simulator jetable avec au moins un turnout correctement défini.

### Commande

```bash
go run ./cmd/dcc-api-conformance \
  --server http://127.0.0.1:8080 \
  --user1 alice --pass1 correct-horse-1 \
  --user2 bob --pass2 correct-horse-2 \
  --admin admin --admin-pass correct-horse-admin \
  --check-turnouts
```

### Attendu

Parmi les lignes :

```text
PASS ... turnout ...
PASS turnout state confirms the commanded position
```

Aucun `FAIL`.

### Ce qui doit être vérifié

- toutes les définitions retournées sont valides ;
- une position déclarée est commandée ;
- l'état relu confirme cette position ;
- une position invalide est rejetée avec `invalid_turnout_position`;
- si un triple existe : ses trois positions sont parcourues.

### Test automatique de la suite elle-même

```bash
go test ./cmd/dcc-api-conformance \
  -run '^TestConformanceRunnerAgainstSimulator$' \
  -v
```

---

# 3.4 Expiration de session

## Précondition

Créer une configuration jetable :

```json
"security": {
  "accessTokenTTL": "2s",
  "refreshTokenTTL": "5s"
}
```

Redémarrer `dccd`.

### Commande

```bash
go run ./cmd/dcc-api-conformance \
  --server http://127.0.0.1:8080 \
  --user1 alice --pass1 correct-horse-1 \
  --user2 bob --pass2 correct-horse-2 \
  --check-session-expiration \
  --session-expiration-max-wait 15s
```

### Attendu exactement

Les contrôles suivants doivent être `PASS` :

```text
access token is accepted before expiration
expired access token is rejected
refresh token remains valid after access-token expiration
refreshed access token is accepted
expired refresh token is rejected
```

### Test isolé sans serveur externe

```bash
go test ./cmd/dcc-api-conformance \
  -run 'TestSessionExpirationChecksAgainstSimulator|TestSessionExpirationWait|TestWaitUntilHonorsCancelledContext' \
  -v
```

---

# 4. Authentification, rôles, utilisateurs

## 4.1 Test automatique canonique

```bash
go test ./internal/auth ./internal/service ./internal/api ./internal/admin
```

Puis :

```bash
go test -race ./internal/service ./internal/api
```

Ces packages contiennent la preuve principale.

## 4.2 Test externe

La conformité passive couvre login, refresh, logout et révocation :

```bash
go run ./cmd/dcc-api-conformance \
  --server http://127.0.0.1:8080 \
  --user1 alice --pass1 correct-horse-1 \
  --user2 bob --pass2 correct-horse-2
```

## 4.3 Administration locale

Lister :

```bash
go run ./cmd/dccd user list \
  --socket /tmp/dccd-admin.sock
```

Ajouter un utilisateur temporaire :

```bash
printf '%s\n' 'temporary-pass-123' |
  go run ./cmd/dccd user add \
    --socket /tmp/dccd-admin.sock \
    --username testuser \
    --display-name 'Test User' \
    --role viewer \
    --password-stdin
```

Désactiver :

```bash
go run ./cmd/dccd user disable \
  --socket /tmp/dccd-admin.sock \
  --username testuser
```

Vérifier ensuite qu'un login est refusé.

---

# 5. Leases et conduite multi-utilisateur

## 5.1 Preuve automatique

```bash
go test ./internal/service ./internal/api
go test -race ./internal/service ./internal/api
```

## 5.2 Preuve externe recommandée

La suite active vérifie le conflit de lease avec deux sessions :

```bash
go run ./cmd/dcc-api-conformance \
  --server http://127.0.0.1:8080 \
  --user1 alice --pass1 correct-horse-1 \
  --user2 bob --pass2 correct-horse-2 \
  --allow-active-commands
```

Utiliser uniquement Simulator ou banc choisi.

## 5.3 Diagnostic manuel avec dccctl

Lister les locomotives :

```bash
DCC_PASSWORD='correct-horse-1' \
go run ./cmd/dccctl \
  --server http://127.0.0.1:8080 \
  --username alice \
  --password-env DCC_PASSWORD \
  locomotives
```

Acquérir :

```bash
DCC_PASSWORD='correct-horse-1' \
go run ./cmd/dccctl \
  --server http://127.0.0.1:8080 \
  --username alice \
  --password-env DCC_PASSWORD \
  acquire <locomotive-id>
```

Depuis une autre session/state-file, tenter la même acquisition avec Bob.

Attendu : conflit, pas de second propriétaire.

---

# 6. Conduite, fonctions, power et E-stop

## 6.1 Simulator ou banc réel

Acquisition :

```bash
dccctl --server http://127.0.0.1:8080 \
  --username alice \
  acquire <locomotive-id>
```

Throttle :

```bash
dccctl --server http://127.0.0.1:8080 \
  --username alice \
  throttle <locomotive-id> 10 forward
```

Arrêt :

```bash
dccctl --server http://127.0.0.1:8080 \
  --username alice \
  throttle <locomotive-id> 0 forward
```

Fonction F0 :

```bash
dccctl --server http://127.0.0.1:8080 \
  --username alice \
  function <locomotive-id> 0 true
```

Power :

```bash
dccctl --server http://127.0.0.1:8080 \
  --username alice \
  power status

dccctl --server http://127.0.0.1:8080 \
  --username alice \
  power off

dccctl --server http://127.0.0.1:8080 \
  --username alice \
  power on
```

E-stop :

```bash
dccctl --server http://127.0.0.1:8080 \
  --username alice \
  emergency-stop
```

## 6.2 Séquence sécurité à vérifier

1. power on ;
2. throttle positif ;
3. emergency-stop ;
4. tenter throttle positif ;
5. tenter fonction positive ;
6. envoyer power on ;
7. retenter throttle.

Attendu :

```text
étape 4 → 409 emergency_stop_active
étape 5 → refus sécurité
étape 6 → succès
étape 7 → succès
```

Sur matériel : train immobilisé dès l'E-stop.

---

# 7. WebSocket

## 7.1 Obtenir un token

```bash
curl -sS http://127.0.0.1:8080/api/v1/auth/login \
  -H 'Content-Type: application/json' \
  -d '{
    "username":"alice",
    "password":"correct-horse-1",
    "clientId":"websocket-test",
    "clientName":"websocket-test",
    "platform":"cli"
  }'
```

```bash
export TRAINPILOT_TOKEN='...'
```

## 7.2 Ouvrir le WebSocket

Avec `websocat` :

```bash
websocat \
  -H="Authorization: Bearer $TRAINPILOT_TOKEN" \
  ws://127.0.0.1:8080/api/v1/events
```

### Attendu immédiatement

Premier message :

```text
system.snapshot
```

avec une séquence courante.

## 7.3 Générer un événement

Dans un autre terminal :

```bash
dccctl --server http://127.0.0.1:8080 \
  --username alice \
  power off
```

Observer l'événement dans `websocat`.

## 7.4 Preuve automatique

```bash
go test ./internal/api
go test -race ./internal/api
```

Les cas difficiles — gaps, snapshot-request, client lent, token expiré, session révoquée — sont principalement des tests automatiques. Ils ne doivent pas être reproduits manuellement à chaque recette.

---

# 8. Simulateur

## 8.1 Preuve complète

```bash
go test ./internal/station/simulator/... ./internal/api
```

Les scénarios de référence sont obligatoires dans :

```bash
go test ./...
```

## 8.2 Piloter manuellement un scénario

Login puis :

```bash
export TRAINPILOT_TOKEN='...'
```

Charger :

```bash
curl -sS -X POST \
  http://127.0.0.1:8080/test/v1/simulator/scenarios \
  -H "Authorization: Bearer $TRAINPILOT_TOKEN" \
  -H 'Content-Type: application/json' \
  --data-binary @tests/simulator/scenarios/station-offline-recovery.json
```

Démarrer :

```bash
curl -sS -X POST \
  http://127.0.0.1:8080/test/v1/simulator/scenarios/start \
  -H "Authorization: Bearer $TRAINPILOT_TOKEN"
```

Avancer :

```bash
curl -sS -X POST \
  http://127.0.0.1:8080/test/v1/simulator/scenarios/advance \
  -H "Authorization: Bearer $TRAINPILOT_TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{"duration":"2s"}'
```

Observer :

```bash
dccctl --server http://127.0.0.1:8080 \
  --username alice \
  power status
```

---

# 9. Import/export et migrations

## 9.1 Tests ciblés officiels

```bash
go test ./internal/model ./internal/store ./internal/transfer
```

Cette commande couvre notamment :

- modèle simple/triple/TJD/TJS ;
- anciennes tables ;
- migration rejouée ;
- layout ancien ;
- round-trip archive.

## 9.2 Export manuel

Avec `dccctl`, utiliser les sous-commandes disponibles dans la version courante :

```bash
dccctl --help
```

et :

```bash
dccctl <commande-export> --help
```

Le nom exact des sous-commandes doit être pris depuis `dccctl --help` de la release testée afin de ne pas recopier une syntaxe obsolète.

Alternative stable HTTP :

```bash
curl -sS \
  http://127.0.0.1:8080/api/v1/layout/export \
  -H "Authorization: Bearer $TRAINPILOT_TOKEN" \
  -o /tmp/layout.zip
```

Vérifier :

```bash
unzip -l /tmp/layout.zip
```

---

# 10. Aiguillages — tests logiciels ciblés

## 10.1 Modèle / store / transfert

```bash
go test ./internal/model ./internal/store ./internal/transfer
```

## 10.2 Contrôleur métier

```bash
go test ./internal/service -run RailwayService
```

Puis concurrence :

```bash
go test -race ./internal/service
```

Cela couvre :

- confirmation immédiate ;
- absence de confirmation ;
- retour incohérent ;
- chemins sûrs triple ;
- quatre positions TJD ;
- panne partielle ;
- timeout ;
- stale confirmation ;
- changement externe ;
- sérialisation.

## 10.3 API et WebSocket

```bash
go test ./internal/api -run Turnout
```

## 10.4 CLI

```bash
go test ./cmd/dccctl -run Turnout
```

## 10.5 Conformance bout-en-bout

```bash
go run ./cmd/dcc-api-conformance \
  --server http://127.0.0.1:8080 \
  --user1 alice --pass1 correct-horse-1 \
  --user2 bob --pass2 correct-horse-2 \
  --admin admin --admin-pass correct-horse-admin \
  --check-turnouts
```

---

# 11. Aiguillages — diagnostic manuel Simulator

Lister :

```bash
DCC_PASSWORD='correct-horse-dispatch' \
dccctl \
  --server http://127.0.0.1:8080 \
  --username dispatcher \
  --password-env DCC_PASSWORD \
  turnouts
```

Voir les positions :

```bash
dccctl \
  --server http://127.0.0.1:8080 \
  --username dispatcher \
  turnout T3 --positions
```

Commander :

```bash
dccctl \
  --server http://127.0.0.1:8080 \
  --username dispatcher \
  turnout T3 left
```

Puis :

```bash
dccctl ... turnout T3 straight
dccctl ... turnout T3 right
```

### Triple

Vérifier avec :

```bash
dccctl ... turnouts
```

que seules les positions configurées sont exposées.

Une quatrième combinaison physique peut être injectée via l'API Simulator pour tester `invalid/unknown`, mais elle ne doit pas être commandable comme position logique.

---

# 12. z21 — logiciel

## 12.1 Toute la suite z21

```bash
go test ./internal/station/z21
```

Race si pertinent :

```bash
go test -race ./internal/station/z21
```

## 12.2 Ce que cette commande doit couvrir

- throttle/fonctions/power ;
- health ;
- R-BUS parser ;
- adresse accessoire linéaire ↔ FAdr ;
- `LAN_X_SET_TURNOUT`;
- activate/deactivate ;
- `Q=1`;
- GET turnout info ;
- 4 états ZZ ;
- requêtes simultanées ;
- broadcast ;
- pulse ;
- annulation avec désactivation de sécurité ;
- refus offline.

---

# 13. z21 réelle — traction

Utiliser les commandes déjà validées.

Exemple :

```bash
./bin/dccctl \
  --server http://127.0.0.1:8080 \
  --username ldandoy \
  --password-env DCC_PASSWORD \
  acquire <loco-id>
```

Puis :

```bash
./bin/dccctl ... throttle <loco-id> 10 forward
./bin/dccctl ... throttle <loco-id> 0 forward
./bin/dccctl ... throttle <loco-id> 10 reverse
./bin/dccctl ... function <loco-id> 0 true
./bin/dccctl ... emergency-stop
./bin/dccctl ... power status
```

### Preuve

Noter :

```text
commande
réponse CLI
observation physique
résultat
```

---

# 14. z21 intermittente

Il n'existe pas une commande TrainPilot unique qui « crée » une intermittence UDP.

Il faut perturber le réseau au niveau OS/firewall/switch ou physiquement.

## 14.1 Ce que TrainPilot doit observer

Pendant la perturbation :

```bash
dccctl --server http://127.0.0.1:8080 \
  --username ldandoy \
  power status
```

Puis régulièrement répéter.

Vérifier :

```text
online
→ degraded
→ éventuellement offline
→ online
```

## 14.2 Test de non-rejeu

Quand offline :

```bash
dccctl ... throttle <loco-id> 50 forward
```

Attendu : refus.

Après reconnexion :

```text
ne rien envoyer pendant 20s
```

Le train reste arrêté.

Puis :

```bash
dccctl ... throttle <loco-id> 10 forward
```

Le train repart uniquement à cette nouvelle commande.

Même principe pour :

```bash
dccctl ... turnout <id> <position>
```

---

# 15. R-BUS / Roco 10819

Il n'existe pas de commande à envoyer au 10819 pour créer une occupation : c'est une observation physique.

## 15.1 Observer les blocs

Dans un terminal :

```bash
dccctl \
  --server http://127.0.0.1:8080 \
  --username ldandoy \
  blocks
```

Si `blocks` n'existe pas dans la CLI de la release, utiliser l'API :

```bash
curl -sS \
  http://127.0.0.1:8080/api/v1/blocks \
  -H "Authorization: Bearer $TRAINPILOT_TOKEN"
```

## 15.2 Observer les événements

```bash
websocat \
  -H="Authorization: Bearer $TRAINPILOT_TOKEN" \
  ws://127.0.0.1:8080/api/v1/events
```

## 15.3 Procédure pour une entrée

1. zone vide ;
2. lire `/api/v1/blocks`;
3. poser locomotive/charge ;
4. observer WebSocket ;
5. relire `/api/v1/blocks`;
6. retirer ;
7. observer nouvelle transition.

Attendu :

```text
false → true → false
```

sur le bon `blockId`.

## 15.4 Passage A→B

Faire rouler lentement :

```text
A
A+B
B
```

Conserver la séquence WebSocket.

Ce fichier sera directement exploitable lors du futur développement de la localisation.

---

# 16. Aiguillages z21 — campagne matérielle

La commande de référence est le script du dépôt.

## 16.1 Aide

```bash
scripts/test-turnouts.sh --help
```

## 16.2 Dry-run obligatoire

```bash
scripts/test-turnouts.sh \
  --server http://127.0.0.1:8080 \
  --username dispatcher \
  --password-env DCC_PASSWORD \
  --turnout T1 \
  --positions straight,diverging \
  --cycles 20 \
  --dry-run
```

Attendu :

- aucune commande réelle ;
- liste claire des commandes prévues ;
- checkpoints manuels affichés.

## 16.3 Test réel simple

```bash
scripts/test-turnouts.sh \
  --server http://127.0.0.1:8080 \
  --username dispatcher \
  --password-env DCC_PASSWORD \
  --turnout T1 \
  --positions straight,diverging \
  --cycles 20 \
  --delay 0.5 \
  --external-check \
  --offline-check \
  --log /tmp/trainpilot-z21-turnouts.log \
  --acknowledge-hardware-risk
```

Le script :

- liste le turnout ;
- affiche ses positions ;
- demande confirmation de sécurité ;
- commande chaque position ;
- demande confirmation visuelle ;
- réalise l'endurance ;
- guide le changement externe ;
- guide la coupure/reconnexion ;
- vérifie qu'une commande offline est refusée ;
- impose l'observation de non-rejeu.

## 16.4 Triple

```bash
scripts/test-turnouts.sh \
  --server http://127.0.0.1:8080 \
  --username dispatcher \
  --password-env DCC_PASSWORD \
  --turnout T3 \
  --positions left,straight,right,straight,left,right,left \
  --cycles 1 \
  --acknowledge-hardware-risk
```

Attendu : aucune combinaison interdite maintenue.

## 16.5 Endurance 500 commandes simples

Deux positions = deux commandes par cycle :

```bash
scripts/test-turnouts.sh \
  ... \
  --positions straight,diverging \
  --cycles 250 \
  --acknowledge-hardware-risk
```

---

# 17. DCC-EX — logiciel

## 17.1 Package

```bash
go test ./internal/station/dccex
```

Puis :

```bash
go test -race ./internal/station/dccex
```

Attendu :

- connexion initiale ;
- perte socket ;
- reconnect ;
- feedback après reconnect ;
- no replay ;
- `<a LINEAR 0|1>`;
- concurrence ;
- état `assumed`.

## 17.2 Intégration service

La suite globale :

```bash
go test ./internal/service ./tests/integration/... ./...
```

Si un chemin n'existe pas dans la release courante, `go test ./...` reste la commande canonique.

---

# 18. DCC-EX réel

## 18.1 Status

```bash
dccctl --server http://127.0.0.1:8080 \
  --username <user> \
  power status
```

## 18.2 Locomotive

```bash
dccctl ... acquire <loco-id>
dccctl ... throttle <loco-id> 10 forward
dccctl ... throttle <loco-id> 0 forward
dccctl ... function <loco-id> 0 true
dccctl ... emergency-stop
```

## 18.3 Aiguillage

```bash
dccctl ... turnouts
dccctl ... turnout <id> --positions
dccctl ... turnout <id> <position>
```

Vérifier physiquement la sortie.

L'état logique DCC-EX doit rester :

```text
assumed
```

si aucune confirmation physique n'existe.

## 18.4 Coupure transport

1. couper TCP/réseau ;
2. observer `power status`;
3. tenter throttle ;
4. tenter turnout ;
5. remettre le transport ;
6. attendre sans commande ;
7. vérifier aucun rejeu ;
8. envoyer une nouvelle commande explicite.

---

# 19. Itinéraires MVP

## Preuve automatique

```bash
go test ./internal/service
```

Les tests couvrent notamment occupation/conflits et activation offline.

## Diagnostic manuel Simulator

Lister :

```bash
curl -sS \
  http://127.0.0.1:8080/api/v1/routes \
  -H "Authorization: Bearer $TRAINPILOT_TOKEN"
```

Réserver :

```bash
curl -i -X POST \
  http://127.0.0.1:8080/api/v1/routes/<route-id>/reserve \
  -H "Authorization: Bearer $TRAINPILOT_TOKEN"
```

Activer :

```bash
curl -i -X POST \
  http://127.0.0.1:8080/api/v1/routes/<route-id>/activate \
  -H "Authorization: Bearer $TRAINPILOT_TOKEN"
```

Release :

```bash
curl -i -X POST \
  http://127.0.0.1:8080/api/v1/routes/<route-id>/release \
  -H "Authorization: Bearer $TRAINPILOT_TOKEN"
```

Ne pas considérer ce test comme validation d'un interlocking complet.

---

# 20. Sauvegarde / restauration SQLite

Ce test est manuel car la politique de sauvegarde opérationnelle reste à formaliser.

## Procédure sûre simple

1. arrêter `dccd` proprement ;
2. identifier le fichier SQLite ;
3. copier le fichier vers un répertoire de sauvegarde ;
4. redémarrer ;
5. restaurer la copie sur une instance de test, pas directement sur le serveur de référence ;
6. lancer la conformité passive.

Exemple :

```bash
cp /chemin/trainpilot.db /tmp/trainpilot-backup.db
```

Après restauration sur instance de test :

```bash
go run ./cmd/dcc-api-conformance \
  --server http://127.0.0.1:8080 \
  --user1 alice --pass1 correct-horse-1 \
  --user2 bob --pass2 correct-horse-2
```

Si la base est sauvegardée **à chaud**, ne pas utiliser une simple copie de fichier sans avoir défini la méthode WAL/backup appropriée.

---

# 21. Robustesse longue durée

Il n'existe pas encore un unique binaire benchmark couvrant toute cette recette.

## Minimum pratique

Pendant une session longue :

```bash
ps -o pid,rss,vsz,etime,command -p "$(pgrep -n dccd)"
```

Goroutines si un endpoint pprof est ajouté un jour : non disponible par défaut, ne pas l'inventer.

Sous Linux :

```bash
top -p "$(pgrep -n dccd)"
```

ou :

```bash
pidstat -p "$(pgrep -n dccd)" 5
```

En parallèle :

- clients WebSocket ;
- commandes simulator ;
- feedbacks ;
- turnouts ;
- heartbeats.

Attendu : pas de croissance continue mémoire/goroutines observable, pas de blocage.

---

# 22. Comment cocher les lignes du plan

## Exemple complet pour le plan 3.1

### « Inventaire des routes cohérent avec OpenAPI »

Lancer :

```bash
go test ./cmd/dcc-api-conformance \
  -run '^TestPublicEndpointInventoryMatchesServerAndOpenAPI$' \
  -v
```

PASS → case cochée.

### « Méthodes HTTP correctes »

Même test : il compare `METHOD + PATH`.

PASS → case cochée.

### « Codes d'erreur publics stables »

Lancer :

```bash
go run ./cmd/dcc-api-conformance \
  --server http://127.0.0.1:8080 \
  --user1 alice --pass1 correct-horse-1 \
  --user2 bob --pass2 correct-horse-2
```

Vérifier `0 failed`, en particulier :

```text
protected endpoint rejects missing token
rotated access token is immediately rejected
unknown locomotive returns a structured not-found error
non-administrator cannot import rolling stock
logged-out access token is rejected
```

PASS → case cochée.

### « Erreurs internes masquées »

Lancer :

```bash
go test ./internal/api
```

et la suite complète :

```bash
go test ./...
```

Aucun test manuel destructif supplémentaire n'est requis.

PASS → case cochée.

### « Aucun endpoint HTTP public de création d'utilisateur »

Le test externe le fait déjà.

Pour preuve manuelle :

```bash
curl -i -X POST http://127.0.0.1:8080/api/v1/users \
  -H "Authorization: Bearer $TRAINPILOT_TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{"username":"forbidden"}'
```

Attendu :

```text
404 Not Found
```

PASS → case cochée.

---

# 23. Tableau rapide « où trouver la commande ? »

| Domaine | Commande principale |
|---|---|
| Gate complète | `go test ./...` |
| Sans CGO | `CGO_ENABLED=0 go test ./...` |
| Races | `go test -race ./...` |
| Routes/OpenAPI | `go test ./cmd/dcc-api-conformance -run TestPublicEndpointInventoryMatchesServerAndOpenAPI -v` |
| Inventaire API | `go run ./cmd/dcc-api-conformance --list-endpoints` |
| Conformité passive | `go run ./cmd/dcc-api-conformance --server ... --user1 ... --user2 ...` |
| Conformité active | ajouter `--allow-active-commands --allow-configuration-mutations` |
| Aiguillages conformance | ajouter `--check-turnouts --admin ... --admin-pass ...` |
| Expiration session | ajouter `--check-session-expiration` |
| Modèle aiguillages | `go test ./internal/model ./internal/store ./internal/transfer` |
| Contrôleur aiguillages | `go test ./internal/service -run RailwayService` |
| API aiguillages | `go test ./internal/api -run Turnout` |
| CLI aiguillages | `go test ./cmd/dccctl -run Turnout` |
| z21 driver | `go test ./internal/station/z21` |
| DCC-EX driver | `go test ./internal/station/dccex` |
| Simulateur | `go test ./internal/station/simulator/... ./internal/api` |
| Campagne aiguillages réelle | `scripts/test-turnouts.sh ...` |
| WebSocket manuel | `websocat -H="Authorization: Bearer ..." ws://.../api/v1/events` |
| Blocs / R-BUS | `GET /api/v1/blocks` + WebSocket |
| Routes MVP | `GET /api/v1/routes`, POST reserve/activate/release |

---

# 24. Preuve à conserver après chaque campagne

Pour les tests logiciels :

```text
commit
OS
commande
exit code
log
```

Exemple :

```bash
git rev-parse HEAD
uname -a
go test ./... | tee /tmp/go-test.log
echo $?
```

Pour le matériel :

```text
commit
release
centrale + firmware
décodeur
adresse
configuration
action physique
résultat observé
log TrainPilot
capture réseau si utile
PASS/FAIL/BLOCKED/NOT_TESTED
```

---

# 25. Sources dans le dépôt

Les commandes de ce document sont principalement issues de :

```text
README.md
docs/TESTING.md
docs/hardware-tests/turnouts/README.md
cmd/dcc-api-conformance/
scripts/test-turnouts.sh
api/openapi.yaml
```

Pour un test CLI, toujours vérifier également :

```bash
dccctl --help
dccctl <commande> --help
```

de la **même release que le serveur**, afin d'éviter d'utiliser une syntaxe ancienne.
