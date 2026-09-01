# DCC Control Server

Socle serveur pour piloter un réseau ferroviaire numérique DCC depuis plusieurs clients natifs. Le dépôt ne contient volontairement aucun client graphique macOS, iOS ou Linux.

## État de cette version

Cette version constitue un **MVP fonctionnel et testable**, destiné à figer l’architecture et le contrat des futurs clients.

Fonctions incluses :

- serveur HTTP JSON en Go ;
- contrat OpenAPI et contrat d’événements AsyncAPI ;
- WebSocket sans dépendance Go externe, avec séquence monotone, snapshot initial et resynchronisation à la demande ;
- base SQLite via `modernc.org/sqlite`, sans CGO ;
- utilisateurs, rôles, sessions, access tokens et refresh tokens révocables ;
- administration des utilisateurs uniquement par socket Unix local ;
- réservation exclusive d’une locomotive par session ;
- heartbeat de réservation ;
- arrêt à vitesse zéro avant libération d’une réservation expirée ;
- locomotives, cantons, aiguillages et itinéraires de démonstration ;
- rétrosignalisation normalisée et mapping capteur → canton ;
- pilote de centrale simulée ;
- pilote DCC-EX TCP pour alimentation, arrêt, vitesse, fonctions, accessoires et remontées de capteurs, avec suivi de santé et reconnexion automatique ;
- pilote Z21 UDP pour alimentation, arrêt, vitesse, fonctions, accessoires binaires et parsing R-BUS ;
- import/export versionné du parc et du circuit dans des archives ZIP natives ;
- outil de diagnostic et de transfert `dccctl` ;
- outil de conformité `dcc-api-conformance` ;
- tests unitaires, de concurrence, de protocoles et d’intégration.

Limites assumées du MVP :

- l’édition graphique complète du réseau n’est pas encore implémentée ; les archives couvrent les locomotives, cantons, aiguillages, itinéraires et mappings de rétrosignalisation, sans ressources graphiques pour le moment ;
- le décodage R-BUS doit être validé sur une z21 blanche réelle et les modules choisis ;
- les commandes et retours d'accessoires z21 sont couverts par un faux serveur UDP, mais leur adressage et leur temporisation doivent encore être validés sur le banc réel ;
- le pilote DCC-EX fournit les commandes et retours de base ainsi que la reconnexion automatique après une première connexion réussie, mais sa couverture protocolaire et sa validation sur matériel réel restent à compléter ;
- la confirmation physique de l’arrêt d’une locomotive n’est pas disponible sur toutes les centrales : une temporisation de sécurité est utilisée ;
- le serveur ne pilote qu’une centrale par processus ;
- la programmation des CV n’est pas incluse ;
- le format de mot de passe actuel utilise PBKDF2-HMAC-SHA256 avec 600 000 itérations. Le code isole cette fonction afin de pouvoir migrer vers Argon2id avec une stratégie de rehash progressive.

## Arborescence

```text
api/                         contrats OpenAPI et AsyncAPI
cmd/dccd/                    serveur et administration locale
cmd/dccctl/                  client CLI de diagnostic
cmd/dcc-api-conformance/     tests de conformité contre un serveur actif
internal/api/                API HTTP et WebSocket
internal/admin/              serveur/client du socket Unix d’administration
internal/auth/               mots de passe et tokens opaques
internal/service/            règles métier
internal/station/            abstraction et pilotes de centrales
internal/store/              persistance métier
internal/transfer/           archives versionnées et validation d’import
internal/sqlite/             SQLite via database/sql et modernc.org/sqlite
internal/websocket/          implémentation RFC 6455 minimale
internal/*_test.go           tests unitaires
tests/contract/              validation des scénarios contractuels versionnés
tests/integration/           tests d’intégration
tests/simulator/scenarios/   scénarios déterministes du banc virtuel
contract-tests/              scénarios métier lisibles par plusieurs clients
deploy/                      exemple systemd et configuration Linux
```

## Prérequis

### Linux et macOS

- Go 1.26 ou supérieur ;
- GoReleaser 2.17 ou supérieur pour produire les binaires et archives.

La persistance utilise `modernc.org/sqlite`, un pilote `database/sql` sans CGO. Aucun compilateur C ni paquet système SQLite n'est nécessaire. Les binaires peuvent donc être compilés nativement ou en cross-compilation avec `CGO_ENABLED=0`.

## Compiler et tester

```bash
go mod download
go test ./...
CGO_ENABLED=0 go test ./...
go test -race ./...
go vet ./...
goreleaser check
goreleaser release --snapshot --clean --skip=publish
```

GoReleaser produit dans `dist/` une archive par système cible. Chaque archive contient :

```text
bin/dccd
bin/dccctl
bin/dcc-api-conformance
README.md
config.json
api/
docs/
deploy/
```

Pour ne construire que les trois binaires de la plateforme courante :

```bash
goreleaser build --single-target --snapshot
```

## Développer sans centrale DCC

Le fichier `config.json` versionné est une configuration de développement utilisant le simulateur et une écoute locale.

Pour les tests Go, le simulateur fournit également une horloge injectable, un
snapshot profondément copié de son état et un reset déterministe qui conserve
son état de connexion. Les accessoires simulés distinguent la commande
`Desired` du retour `Reported` et peuvent confirmer immédiatement, après un
délai déterministe, ne pas confirmer ou produire un retour incohérent. Ces
fonctions d'introspection restent internes au banc de test et ne modifient pas
l'API publique `/api/v1/...`.

La télémétrie simulée démarre dans un état nominal stable : 25 °C, alimentation
à 18 000 mV, voie à 0 mV lorsqu'elle est coupée, courants nuls et aucun défaut.
Les courants, tensions, température, mode programmation, perte d'alimentation,
surchauffe et courts-circuits peuvent ensuite être injectés sans modèle
physique ni coupure automatique cachée.

La connectivité du simulateur peut être forcée à `online`, `degraded` ou
`offline`. Des règles typées par opération injectent un délai context-aware et
une erreur sur les N prochains appels ; `Remaining: 0` conserve la règle
jusqu'à `ClearFaults()` ou `Reset()`. Une opération encore retardée est annulée
si les faults sont effacés, si le simulateur est reset ou fermé, ou si sa
connectivité change. Aucune commande refusée n'est rejouée.

Les capteurs simulés sont identifiés par source, type et adresse. `SetFeedback`
met à jour leur état physique et garantit l'émission ou retourne une erreur de
saturation ; les répétitions sont conservées. `SetFeedbackState` simule
explicitement un changement physique dont le message est perdu, tandis que les
séquences et rebonds utilisent l'horloge injectée. L'ancien `InjectFeedback`
reste disponible en best-effort pour compatibilité.

Le package `internal/station/simulator/scenario` charge des scénarios JSON
versionnés et strictement validés avant leur démarrage. En mode manuel, un
`Runner` associé à `clock.Fake` avance sans sommeil réel et exécute toutes les
étapes arrivées à échéance, en conservant l'ordre du fichier lorsque plusieurs
actions partagent le même timestamp. Le mode `StartRealtime(ctx)` utilise le
temps réel pour les essais interactifs ; il est annulable et s'arrête également
si le simulateur est fermé ou reset depuis l'extérieur. Son snapshot de
contrôle expose le scénario chargé, l'état `loaded/running/completed/stopped/failed`,
le temps logique, la prochaine étape et l'erreur éventuelle.

Les scénarios de référence se trouvent dans `tests/simulator/scenarios/`. Le
format courant v2 utilise des durées Go (`500ms`, `5s`, `1m`). Le lecteur
accepte encore le format v1. Les actions disponibles sont :

- `station.connectivity`, `station.track_power`, `station.emergency_stop` et
  `station.electrical` ;
- `feedback.set` avec `emit: true|false`, et `feedback.emit` ;
- `accessory.report` et `accessory.behavior` ;
- `fault.operation`, `fault.clear` et `simulator.reset`.

La suite SIM-008 couvre douze situations : conduite nominale, arrêt
d'urgence, récupération `degraded` et `offline`, court-circuit électrique,
feedback simple, multiple, avec rebond ou événement perdu, puis confirmation
d'accessoire réussie, absente ou incohérente. Les scénarios critiques passent
par l'API HTTP et le WebSocket réels dans `go test ./...`; l'avance reste
entièrement logique et n'attend jamais 10 ou 30 secondes réelles.

Les scénarios AIG-003 ajoutent les endpoints binaires simples, les trois
vecteurs valides et le vecteur interdit d'un triple, les quatre vecteurs d'une
TJD, une panne ciblée sur un endpoint et une confirmation retardée obsolète.

Exemple d'exécution manuelle dans un test :

```go
clk := clock.NewFake(start)
sim := simulator.NewWithClock(clk)
_ = sim.Connect(ctx)
definition, _ := scenario.LoadFile("tests/simulator/scenarios/feedback-a-to-b.json")
runner, _ := scenario.New(definition, sim, clk)
_ = runner.Start(ctx)
_ = runner.Advance(ctx, 3*time.Second)
```

Le moteur ne dépend ni des services métier, ni de SQLite, ni des handlers HTTP.
Il simule uniquement le monde extérieur observé par TrainPilot.

Lorsque `testAPI=true` et que `station.driver` vaut `simulator`, ce banc peut
aussi être piloté depuis un autre processus sous `/test/v1/simulator/...` :
snapshot, reset, connectivité, télémétrie, feedback, accessoires, faults et
avance manuelle des scénarios. Ces routes exigent une authentification et sont
totalement absentes avec un pilote matériel ou lorsque `testAPI=false`. Leur
contrat séparé est documenté dans
[`docs/SIMULATOR_TEST_API.md`](docs/SIMULATOR_TEST_API.md) ; elles ne sont pas
ajoutées à l'OpenAPI public de production.

Le guide complet destiné aux développeurs de clients, avec les flux HTTP,
WebSocket, leases, scénarios, erreurs et diagrammes PlantUML, est disponible
dans [`docs/CLIENT_SIMULATOR_GUIDE.md`](docs/CLIENT_SIMULATOR_GUIDE.md). Il est
inclus dans chaque archive de livraison.

```bash
go run ./cmd/dccd serve --config config.json
```

Dans un autre terminal, créer les utilisateurs pendant que le serveur fonctionne :

```bash
printf '%s\n' 'correct-horse-1' |
  go run ./cmd/dccd user bootstrap \
    --socket /tmp/dccd-admin.sock \
    --username alice \
    --display-name 'Alice' \
    --role driver \
    --password-stdin

printf '%s\n' 'correct-horse-2' |
  go run ./cmd/dccd user add \
    --socket /tmp/dccd-admin.sock \
    --username bob \
    --display-name 'Bob' \
    --role driver \
    --password-stdin
```

Le mode `bootstrap` ne fonctionne que si la table des utilisateurs est vide.

Après un login, copier l'`accessToken` retourné puis charger et avancer un
scénario depuis un autre terminal :

```bash
curl -sS http://127.0.0.1:8080/api/v1/auth/login \
  -H 'Content-Type: application/json' \
  -d '{"username":"alice","password":"correct-horse-1","clientId":"simulator-console-1","clientName":"simulator-console","platform":"cli"}'

export TRAINPILOT_TOKEN='<accessToken retourné>'

curl -sS -X POST http://127.0.0.1:8080/test/v1/simulator/scenarios \
  -H "Authorization: Bearer $TRAINPILOT_TOKEN" \
  -H 'Content-Type: application/json' \
  --data-binary @tests/simulator/scenarios/station-offline-recovery.json

curl -sS -X POST http://127.0.0.1:8080/test/v1/simulator/scenarios/start \
  -H "Authorization: Bearer $TRAINPILOT_TOKEN"

curl -sS -X POST http://127.0.0.1:8080/test/v1/simulator/scenarios/advance \
  -H "Authorization: Bearer $TRAINPILOT_TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{"duration":"2s"}'
```

L'état public est observable avec `dccctl ... power status`. Pour voir le
snapshot puis les événements ordonnés, un client WebSocket tel que `websocat`
peut ouvrir `ws://127.0.0.1:8080/api/v1/events` avec l'en-tête
`Authorization: Bearer <accessToken>`.

Lister les utilisateurs :

```bash
go run ./cmd/dccd user list --socket /tmp/dccd-admin.sock
```

Désactiver un utilisateur et révoquer ses sessions :

```bash
go run ./cmd/dccd user disable --socket /tmp/dccd-admin.sock --username bob
```

Aucune route `/api/v1/users` n’est exposée aux clients. Même un utilisateur ayant le rôle applicatif `administrator` ne peut pas créer de compte à distance.

## Tester l’API

```bash
DCC_PASSWORD='correct-horse-1' \
go run ./cmd/dccctl \
  --server http://127.0.0.1:8080 \
  --username alice \
  --password-env DCC_PASSWORD \
  locomotives
```

Lancer la suite de conformité :

```bash
go run ./cmd/dcc-api-conformance \
  --server http://127.0.0.1:8080 \
  --user1 alice --pass1 correct-horse-1 \
  --user2 bob   --pass2 correct-horse-2
```

Sans option destructive, cette suite vérifie l'état de santé, les versions de
contrat, l'authentification, la rotation et la révocation des jetons, les
lectures publiques authentifiées, les erreurs structurées et les exports.
L'inventaire complet est disponible avec :

```bash
go run ./cmd/dcc-api-conformance --list-endpoints
```

Les commandes de voie ne sont exécutées qu'avec
`--allow-active-commands`. Les mutations temporaires de configuration exigent
en plus `--allow-configuration-mutations`, `--admin` et `--admin-pass` ; elles
doivent être réservées à une instance jetable utilisant le simulateur.

Sur une telle instance, les deux familles opt-in peuvent être combinées :

```bash
go run ./cmd/dcc-api-conformance \
  --server http://127.0.0.1:8080 \
  --user1 alice --pass1 correct-horse-1 \
  --user2 bob --pass2 correct-horse-2 \
  --admin admin --admin-pass correct-horse-admin \
  --allow-active-commands \
  --allow-configuration-mutations
```

Ces options restent désactivées par défaut et ne doivent jamais viser une
centrale réelle sans décision explicite de l'opérateur.

L'expiration naturelle des access tokens et refresh tokens est vérifiée
uniquement avec `--check-session-expiration`. Utilisez une instance de test
configurée avec des TTL courtes, par exemple :

```json
"security": {
  "accessTokenTTL": "2s",
  "refreshTokenTTL": "5s"
}
```

Puis lancez :

```bash
go run ./cmd/dcc-api-conformance \
  --server http://127.0.0.1:8080 \
  --user1 alice --pass1 correct-horse-1 \
  --user2 bob   --pass2 correct-horse-2 \
  --check-session-expiration \
  --session-expiration-max-wait 15s
```

Le maximum vaut `15s` par défaut et empêche une attente accidentelle avec les
TTL de production. Le scénario utilise deux sessions dédiées : il vérifie
d'abord qu'un access token expiré est refusé tandis que son refresh token reste
valide, puis qu'un refresh token naturellement expiré ne peut plus produire de
nouvelle paire. Sans l'option, ces contrôles sont ignorés sans ralentir la suite
standard.

La suite active vérifie notamment :

- l’authentification des deux utilisateurs ;
- la lecture du parc ;
- la réservation exclusive ;
- le refus de la seconde réservation ;
- l’envoi d’une commande de vitesse par le propriétaire ;
- la libération contrôlée ;
- l’absence d’administration distante des utilisateurs ;
- l’export du parc ;
- le refus d’un import par un rôle non administrateur.

## Gestion du matériel roulant

Le CRUD minimal des locomotives est disponible via l'API. La lecture est accessible à tout utilisateur authentifié ; la création, la modification et la suppression nécessitent le rôle `administrator`. Une locomotive possédant un lease actif ne peut pas être modifiée. Une locomotive référencée par l'historique des leases ne peut pas être supprimée.

Créer rapidement une locomotive à adresse DCC courte pour un test matériel :

```bash
DCC_ADMIN_PASSWORD='correct-horse-admin' go run ./cmd/dccctl \
  --server http://127.0.0.1:8080 --username admin \
  --password-env DCC_ADMIN_PASSWORD \
  locomotive-add 'Loco test z21' 3 short 128
```

Lister puis afficher une locomotive :

```bash
DCC_PASSWORD='correct-horse-1' go run ./cmd/dccctl \
  --server http://127.0.0.1:8080 --username alice \
  --password-env DCC_PASSWORD locomotives

DCC_PASSWORD='correct-horse-1' go run ./cmd/dccctl \
  --server http://127.0.0.1:8080 --username alice \
  --password-env DCC_PASSWORD locomotive-show <locomotive-id>
```

Les routes correspondantes sont :

```text
GET    /api/v1/locomotives
POST   /api/v1/locomotives
GET    /api/v1/locomotives/{id}
PUT    /api/v1/locomotives/{id}
DELETE /api/v1/locomotives/{id}
```

Pour les premiers tests z21, une adresse courte (par exemple `3`) est recommandée afin d'isoler la validation de la conduite et de la rétrosignalisation des particularités des adresses DCC longues.

### Contrôle avec `dccctl`

`dccctl` conserve sa session et les leases acquis dans le répertoire de
configuration de l'utilisateur (par exemple `~/.config/dccctl/state.json`
sous Linux), avec des permissions `0600`. Le chemin peut être remplacé avec
`--state-file`. Le mot de passe n'est pas enregistré. Après le premier login,
les commandes suivantes réutilisent la même session et renouvellent
automatiquement ses tokens si nécessaire.

Le lease est retrouvé automatiquement à partir du serveur, de l'utilisateur
et de la locomotive :

```bash
DCC_PASSWORD='correct-horse-1' go run ./cmd/dccctl \
  --server http://127.0.0.1:8080 --username alice \
  --password-env DCC_PASSWORD acquire loco-bb26001

go run ./cmd/dccctl \
  --server http://127.0.0.1:8080 --username alice \
  throttle loco-bb26001 40 forward

go run ./cmd/dccctl \
  --server http://127.0.0.1:8080 --username alice \
  function loco-bb26001 0 true

go run ./cmd/dccctl \
  --server http://127.0.0.1:8080 --username alice \
  release loco-bb26001
```

Les commandes globales de voie ne nécessitent pas de lease :

```bash
dccctl --server http://127.0.0.1:8080 --username alice power off
dccctl --server http://127.0.0.1:8080 --username alice power on
dccctl --server http://127.0.0.1:8080 --username alice power status
dccctl --server http://127.0.0.1:8080 --username alice emergency-stop
```

`power status` interroge la centrale et affiche l'alimentation, l'arrêt
d'urgence, les courts-circuits, le mode programmation ainsi que les mesures de
courant, tension et température disponibles. Avec une Z21, ces données viennent
de `LAN_X_GET_STATUS` et `LAN_SYSTEMSTATE_GETDATA`. Pour un pilote ne proposant
pas encore de lecture d'état, l'alimentation vaut `unknown` jusqu'au premier
ordre `power on` ou `power off` réussi.

La connectivité d'une centrale vaut `online` après une preuve de communication
valide et `degraded` dès la première erreur. Le paramètre optionnel
`station.offlineAfter`, au format de durée Go (`ms`, `s`, `m`, etc.), définit
le temps maximal passé dans cet état ; sa valeur par défaut est `10s`. Le délai
démarre à la première erreur de communication. Une réponse valide reçue avant
son expiration remet immédiatement la centrale `online` ; sinon elle devient
`offline` une fois le délai écoulé. Avec Z21, les interrogations de statut
continuent en permanence, y compris hors ligne. Avec DCC-EX TCP, la perte
confirmée du socket refuse immédiatement les commandes, même pendant le délai
`degraded`, et déclenche la reconnexion. Dans tous les cas, une commande refusée
n'est ni mise en file ni rejouée après le retour de la centrale. Une commande
refusée pour indisponibilité produit HTTP 503 et le code `station_offline`.

Les commandes de sécurité sont arbitrées avant les commandes ordinaires : un
arrêt d'urgence, une coupure de puissance ou un `throttle` à vitesse zéro déjà
en attente passe avant les nouveaux ordres de traction ou de fonctions. Après
un arrêt d'urgence, les vitesses positives et les fonctions restent inhibées
jusqu'à la réussite d'un ordre explicite `power on`. Elles sont également
refusées lorsque la puissance est coupée ou encore inconnue. L'API retourne
alors HTTP 409 avec un code stable parmi `emergency_stop_active`,
`track_power_off`, `track_power_unknown` et `safety_command_preempted`.

Une commande `throttle` ou de fonction valide repousse l'expiration du lease
de 10 minutes. Sans activité ni heartbeat pendant ce délai, le serveur lance
l'arrêt contrôlé puis libère le lease. `throttle` n'acquiert jamais
implicitement une locomotive : `acquire` reste obligatoire.

## Import et export

Les exports sont des archives ZIP version 2 contenant un `manifest.json` et un document JSON. Les archives version 1 restent importables. Les imports utilisent le mode `merge` par défaut ; `--replace` remplace la bibliothèque correspondante après validation.

```bash
# Export accessible à tout utilisateur authentifié
DCC_PASSWORD='correct-horse-1' go run ./cmd/dccctl \
  --server http://127.0.0.1:8080 --username alice \
  --password-env DCC_PASSWORD \
  export-rolling-stock rolling-stock.dcclib

DCC_PASSWORD='correct-horse-1' go run ./cmd/dccctl \
  --server http://127.0.0.1:8080 --username alice \
  --password-env DCC_PASSWORD \
  export-layout layout.dcclayout

# Import réservé au rôle applicatif administrator
DCC_ADMIN_PASSWORD='correct-horse-admin' go run ./cmd/dccctl \
  --server http://127.0.0.1:8080 --username admin \
  --password-env DCC_ADMIN_PASSWORD \
  import-layout layout.dcclayout --replace
```

La taille totale d’une archive est limitée à 25 Mio et chaque entrée à 10 Mio. Les chemins suspects, versions inconnues, références cassées et identifiants dupliqués sont rejetés avant modification de la base.

## Contrat des futurs clients

Le contrat HTTP est dans [`api/openapi.yaml`](api/openapi.yaml). Le contrat WebSocket est dans [`api/asyncapi.yaml`](api/asyncapi.yaml). Le format des archives est détaillé dans [`docs/ARCHIVE_FORMAT.md`](docs/ARCHIVE_FORMAT.md).
La politique de version, de dépréciation et de migration est décrite dans
[`docs/API_COMPATIBILITY.md`](docs/API_COMPATIBILITY.md).

Règles structurantes :

1. Le serveur est la source de vérité.
2. Une commande de conduite nécessite une session valide et un lease actif appartenant à cette session.
3. Une locomotive ne peut avoir qu’un lease vivant (`active` ou `stopping`).
4. Une reprise entre deux sessions du même utilisateur est possible uniquement par l'endpoint explicite `POST /api/v1/control-leases/{leaseId}/takeover`. Elle arrête la locomotive à zéro avant de transférer atomiquement le même lease ; l'acquisition standard ne réalise jamais ce transfert.
5. Une commande de conduite valide renouvelle le lease ; après 10 minutes d'inactivité, il passe d’abord à `stopping`, une vitesse nulle est envoyée, puis il devient `released` après le délai de sécurité.
6. Les commandes de sécurité préemptent les commandes de conduite en attente et aucune reprise après arrêt d’urgence n’est implicite.
7. Les actions d’itinéraire sont refusées si un canton est occupé ou si un itinéraire incompatible est actif.
8. Les événements WebSocket possèdent une séquence monotone pendant la vie du processus.
9. Les comptes utilisateurs ne sont administrables que par le socket local du système d’exploitation.

À l’ouverture du WebSocket, le serveur envoie un événement `system.snapshot`
complet dont `sequence` est la séquence courante du bus. Il contient la
centrale, son état, les locomotives, les leases complets de la session
connectée, l'état public d'occupation de toutes les locomotives contrôlées, les
cantons, les aiguillages et les itinéraires. `controlLeases` reste privé à la
session ; `locomotiveControlStates` permet de distinguer `mine`,
`same_user_other_session` et `other` sans exposer les identifiants des leases
des autres sessions. L'absence d'une locomotive dans ce second tableau signifie
qu'elle est libre. Le client ignore tout événement
de séquence inférieure ou égale au snapshot ; le serveur filtre également ces
événements anciens ou dupliqués. S'il détecte ensuite un trou, il envoie :

```json
{
  "type": "client.snapshot_request",
  "lastSequence": 42
}
```

Le serveur répond par un nouveau `system.snapshot`. `lastSequence` est
informatif dans la version actuelle : le serveur renvoie toujours l’état
courant complet. Les messages `client.heartbeat` n'étendent ni le jeton
d'accès ni un lease et ne consomment pas de numéro de séquence. Le WebSocket
est fermé à l'expiration du jeton utilisé lors de son ouverture ; après un
refresh, le client ouvre donc une nouvelle connexion avec le nouveau jeton.
Un logout ou une révocation de session ferme également la connexion. Aucun
replay des événements intermédiaires n’est conservé actuellement.

Un takeover réussi publie `locomotive.control.transferred` à toutes les
sessions connectées. L'ancienne session doit immédiatement abandonner ses
heartbeats et commandes ; la nouvelle reçoit le même `leaseId` avec une
nouvelle échéance. Le serveur ne conserve ni ne restaure la vitesse précédente.

Chaque connexion possède une file de 64 événements. Si elle déborde, ou si une
écriture WebSocket dépasse 5 secondes, le serveur ferme la connexion afin de
ne pas laisser le client poursuivre avec un état incomplet. Le client se
reconnecte alors et repart du nouveau snapshot complet.

La fermeture d'un WebSocket ne libère pas immédiatement les leases : une
brève coupure réseau ne doit pas provoquer une perte de contrôle. Ils restent
valides jusqu'à une libération explicite ou leur expiration par absence de
heartbeat, qui déclenche l'arrêt contrôlé habituel.

## Aiguillages et appareils composés

Le modèle distingue désormais l'appareil logique de ses sorties DCC binaires.
Un aiguillage possède un `kind`, des endpoints et des positions logiques
définies par des vecteurs `position1`/`position2`.

L'abstraction de centrale utilise `SetBasicAccessory` avec une adresse linéaire
portable `1..2040` et une position typée. Elle ne transmet plus
`straight/diverging` aux drivers. Un provider facultatif distingue les retours
de centrale, les états supposés et les futurs capteurs physiques.

Il représente un aiguillage simple, triple, une TJD, une TJS ou un appareil
personnalisé. Une combinaison physique non déclarée reste inconnue. Elle ne
devient jamais une position commandable.

Le simulateur applique ces vecteurs séquentiellement et publie un
`AccessoryStateEvent` de qualité `physical` par confirmation. Il permet aussi
d'injecter un rapport `station`, `assumed` ou `physical`. Une combinaison non
déclarée laisse `reportedPosition` vide et conserve `pending=true`.

Les anciennes bases et archives à une adresse sont converties automatiquement
en aiguillages simples. Les champs historiques restent temporairement exposés
pour ces appareils. Le rollback après réussite partielle et les séquences de
transition sûres restent prévus dans un ticket ultérieur.

Le modèle, l'adressage linéaire et les exemples sont décrits dans
[`docs/TURNOUTS.md`](docs/TURNOUTS.md).

## Configuration des centrales

### Simulateur

```json
"station": {
  "driver": "simulator"
}
```

### DCC-EX sur TCP

```json
"station": {
  "driver": "dccex",
  "address": "192.168.1.50",
  "port": 2560,
  "transport": "tcp",
  "offlineAfter": "10s"
}
```

Le démarrage exige que la première connexion TCP réussisse. Après une perte
ultérieure du socket, le pilote passe à `degraded`, tente automatiquement de se
reconnecter, puis devient `offline` après `offlineAfter` si DCC-EX ne revient
pas. Une reconnexion réussie le remet immédiatement `online` et les retours de
capteurs reprennent sur le canal existant. Les commandes présentées pendant la
panne sont refusées sans mise en file ni rejeu ; aucune vitesse, fonction ou
position d'accessoire antérieure n'est restaurée automatiquement.

Les accessoires utilisent la forme linéaire brute DCC-EX :

```text
position1 -> <a LINEAR_ADDRESS 0>
position2 -> <a LINEAR_ADDRESS 1>
```

La plage TrainPilot reste `1..2040`. Le pilote ne crée jamais de définition
persistante `<T>` dans la centrale : les IDs et les appareils composés restent
propriété de TrainPilot. Après une écriture TCP réussie, il publie un état de
qualité `assumed`. DCC-EX ne mémorise pas l'état des commandes brutes `<a>` ; ce
retour ne confirme donc ni la réception par le décodeur ni le mouvement des
lames. Aucun changement externe n'est déduit sans source de feedback fiable.
Un décalage par groupe de quatre peut exister avec l'affichage d'un autre
système ; la procédure de diagnostic est décrite dans `docs/TURNOUTS.md`.

### z21/Z21 sur UDP

```json
"station": {
  "driver": "z21",
  "address": "192.168.0.111",
  "port": 21105,
  "offlineAfter": "10s",
  "accessoryPulse": "100ms"
}
```

`offlineAfter`, commun à Z21 et DCC-EX, accepte la syntaxe de
`time.ParseDuration`, par exemple `500ms`, `5s`, `30s` ou `1m`. Une valeur
invalide, nulle ou négative empêche le démarrage du serveur.

`accessoryPulse` configure la durée d'activation d'une sortie binaire z21. Sa
valeur par défaut est `100ms`. Le format est celui des durées Go et une valeur
invalide, nulle ou négative empêche aussi le démarrage. La commande active la
sortie, attend cette durée, puis la désactive. La désactivation est tentée avec
un contexte de sécurité interne même si la requête cliente est annulée.

Le pilote accepte les adresses linéaires TrainPilot `1..2040`, les convertit en
`FAdr = adresse - 1`, interroge ensuite `LAN_X_GET_TURNOUT_INFO` et publie les
retours spontanés reçus. Les états z21 « pas encore commuté » et « invalide »
restent inconnus côté turnout : le serveur n'invente jamais une position. Une
confirmation de centrale n'est pas une preuve de mouvement mécanique des
lames. La validation sur z21 blanche réelle reste nécessaire.

## Rétrosignalisation

La table `feedback_mappings` associe une source physique à un canton logique :

```text
provider = dccex, address = 14  → block_id = gare-voie-1
provider = z21-rbus, address = 9 → block_id = pleine-voie
```

Les pilotes publient des événements génériques `FeedbackEvent`. Le service ferroviaire met à jour le canton et émet ensuite `block.occupancy.changed` sur WebSocket.

## Sécurité réseau

La configuration de démonstration écoute uniquement sur `127.0.0.1` et utilise HTTP. Pour une écoute sur le LAN, configurez `tlsCert` et `tlsKey`, ou placez le serveur derrière un reverse proxy TLS correctement configuré.

Le socket Unix d’administration doit être protégé par les permissions du système. La valeur décimale `432` dans le JSON correspond au mode octal `0660`.

## Étapes suivantes proposées

1. Valider les pilotes sur DCC-EX et z21 blanche réels.
2. Étendre le parc au-delà des locomotives et compléter l’édition du plan de réseau.
3. Étendre les archives aux ressources graphiques, images et futures migrations de format.
4. Ajouter les signaux, les conflits d’itinéraires explicites et la libération progressive.
5. Ajouter les tests matériels exécutés sur un banc dédié.
6. Développer ensuite les clients Swift et Linux contre le simulateur et les contrats fournis.
