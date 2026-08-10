# DCC Control Server

Socle serveur pour piloter un réseau ferroviaire numérique DCC depuis plusieurs clients natifs. Le dépôt ne contient volontairement aucun client graphique macOS, iOS ou Linux.

## État de cette version

Cette version constitue un **MVP fonctionnel et testable**, destiné à figer l’architecture et le contrat des futurs clients.

Fonctions incluses :

- serveur HTTP JSON en Go ;
- contrat OpenAPI et contrat d’événements AsyncAPI ;
- WebSocket sans dépendance Go externe ;
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
- le pilote Z21 doit encore recevoir des tests matériels et la commande des accessoires ;
- le décodage R-BUS doit être validé sur une z21 blanche réelle et les modules choisis ;
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
tests/integration/           tests d’intégration
scripts/                     outils de maintenance du dépôt
deploy/                     exemple systemd et configuration Linux
```

## Prérequis

### Linux et macOS

- Go 1.23 ou supérieur ;
- GoReleaser 2.17 ou supérieur pour produire les binaires et archives.

La persistance utilise `modernc.org/sqlite`, un pilote `database/sql` sans CGO. Aucun compilateur C ni paquet système SQLite n'est nécessaire. Les binaires peuvent donc être compilés nativement ou en cross-compilation avec `CGO_ENABLED=0`.

## Compiler et tester

```bash
go mod download
go test ./...
go test -race ./...
goreleaser check
goreleaser release --snapshot --clean --skip=publish
```

GoReleaser produit dans `dist/` une archive par système cible. Chaque archive contient :

```text
bin/dccd
bin/dccctl
bin/dcc-api-conformance
README.md
config.example.json
api/
docs/
deploy/
```

Pour ne construire que les trois binaires de la plateforme courante :

```bash
goreleaser build --single-target --snapshot
```

Le chemin de module fourni est volontairement générique. Avant de publier le dépôt :

```bash
./scripts/rename-module.sh github.com/votre-organisation/dcc-control-server
```

## Démarrage rapide avec le simulateur

```bash
cp config.example.json config.json
goreleaser build --single-target --snapshot

# GoReleaser place les artefacts dans dist/. Pour un lancement de développement
# sans rechercher leur chemin, go run reste le plus simple :
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

Cette suite vérifie notamment :

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

Règles structurantes :

1. Le serveur est la source de vérité.
2. Une commande de conduite nécessite une session valide et un lease actif appartenant à cette session.
3. Une locomotive ne peut avoir qu’un lease vivant (`active` ou `stopping`).
4. Un lease expiré passe d’abord à `stopping` ; une vitesse nulle est envoyée ; il ne devient `released` qu’après le délai de sécurité.
5. Les actions d’itinéraire sont refusées si un canton est occupé ou si un itinéraire incompatible est actif.
6. Les événements WebSocket possèdent une séquence monotone pendant la vie du processus.
7. Les comptes utilisateurs ne sont administrables que par le socket local du système d’exploitation.

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
2. Ajouter le CRUD complet du parc et du plan de réseau.
3. Étendre les archives aux ressources graphiques, images et futures migrations de format.
4. Ajouter les signaux, les conflits d’itinéraires explicites et la libération progressive.
5. Ajouter un instantané/replay des événements après reconnexion.
6. Ajouter les tests matériels exécutés sur un banc dédié.
7. Développer ensuite les clients Swift et Linux contre le simulateur et les contrats fournis.
