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
- pilote DCC-EX TCP pour alimentation, arrêt, vitesse, fonctions, accessoires et remontées de capteurs ;
- pilote Z21 UDP initial pour alimentation, arrêt, vitesse, fonctions et parsing R-BUS ;
- import/export versionné du parc et du circuit dans des archives ZIP natives ;
- outil de diagnostic et de transfert `dccctl` ;
- outil de conformité `dcc-api-conformance` ;
- tests unitaires, de concurrence, de protocoles et d’intégration.

Limites assumées du MVP :

- l’édition graphique complète du réseau n’est pas encore implémentée ; les archives couvrent les locomotives, cantons, aiguillages, itinéraires et mappings de rétrosignalisation, sans ressources graphiques pour le moment ;
- le décodage R-BUS doit être validé sur une z21 blanche réelle et les modules choisis ;
- le pilote DCC-EX fournit les commandes et retours de base, mais sa surveillance de disponibilité, sa reconnexion et sa validation sur matériel réel restent à compléter ;
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

## Démarrage rapide avec le simulateur

Le fichier `config.json` versionné est une configuration de développement utilisant le simulateur et une écoute locale.

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

La connectivité Z21 vaut `online` après toute réponse UDP valide, `degraded`
après la première erreur ou expiration de délai, puis `offline` si aucune
réponse valide ne revient pendant 10 secondes. Les interrogations de statut
continuent en permanence, y compris hors ligne. Les commandes actives sont
refusées en état `offline` avec HTTP 503 et le code `station_offline`, mais
restent autorisées en état `degraded`.

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

Les exports sont des archives ZIP versionnées contenant un `manifest.json` et un document JSON. Les imports utilisent le mode `merge` par défaut ; `--replace` remplace la bibliothèque correspondante après validation.

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
4. Une commande de conduite valide renouvelle le lease ; après 10 minutes d'inactivité, il passe d’abord à `stopping`, une vitesse nulle est envoyée, puis il devient `released` après le délai de sécurité.
5. Les commandes de sécurité préemptent les commandes de conduite en attente et aucune reprise après arrêt d’urgence n’est implicite.
6. Les actions d’itinéraire sont refusées si un canton est occupé ou si un itinéraire incompatible est actif.
7. Les événements WebSocket possèdent une séquence monotone pendant la vie du processus.
8. Les comptes utilisateurs ne sont administrables que par le socket local du système d’exploitation.

À l’ouverture du WebSocket, le serveur envoie un événement `system.snapshot`
complet dont `sequence` est la séquence courante du bus. Il contient la
centrale, son état, les locomotives, les leases de la session connectée, les
cantons, les aiguillages et les itinéraires. Le client ignore tout événement
de séquence inférieure ou égale au snapshot. S'il détecte ensuite un trou, il
envoie :

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

La fermeture d'un WebSocket ne libère pas immédiatement les leases : une
brève coupure réseau ne doit pas provoquer une perte de contrôle. Ils restent
valides jusqu'à une libération explicite ou leur expiration par absence de
heartbeat, qui déclenche l'arrêt contrôlé habituel.

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
  "transport": "tcp"
}
```

### z21/Z21 sur UDP

```json
"station": {
  "driver": "z21",
  "address": "192.168.0.111",
  "port": 21105
}
```

Le pilote Z21 est volontairement conservateur : alimentation, arrêt, conduite et fonctions sont présents ; les accessoires et certains retours doivent être complétés après validation sur le matériel réel.

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
2. Ajouter la surveillance de disponibilité et la reconnexion au pilote DCC-EX.
3. Étendre le parc au-delà des locomotives et compléter l’édition du plan de réseau.
4. Ajouter les tests WebSocket restants pour doublons, événements anciens, snapshot concurrent et client lent.
5. Étendre les archives aux ressources graphiques, images et futures migrations de format.
6. Ajouter les signaux, les conflits d’itinéraires explicites et la libération progressive.
7. Ajouter les tests matériels exécutés sur un banc dédié.
8. Développer ensuite les clients Swift et Linux contre le simulateur et les contrats fournis.
