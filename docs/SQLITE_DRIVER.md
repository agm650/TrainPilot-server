# Pilote SQLite sans CGO

Le serveur utilise `modernc.org/sqlite` comme pilote `database/sql`.

## Motivations

- aucun compilateur C requis ;
- aucune dépendance à `libsqlite3` sur la machine cible ;
- cross-compilation Linux/macOS simplifiée ;
- conservation du format de fichier SQLite standard ;
- API de persistance fondée sur la bibliothèque standard `database/sql`.

## Versions retenues

```text
modernc.org/sqlite v1.37.0
modernc.org/libc   v1.62.1
```

La version de `modernc.org/libc` est volontairement déclarée directement dans
`go.mod`. Le projet `modernc.org/sqlite` recommande d'utiliser exactement la
version de `modernc.org/libc` déclarée dans son propre `go.mod`.

Lors d'une mise à niveau du pilote, mettre les deux versions à jour ensemble,
puis exécuter :

```bash
go mod tidy
go test ./...
go test -race ./...
```

## Gestion des connexions

`internal/sqlite.Open` limite `database/sql` à une seule connexion ouverte et
inactive. Ce choix :

- conserve la sérialisation de l'ancienne implémentation ;
- garantit qu'une base `:memory:` reste unique ;
- maintient les PRAGMA de connexion (`foreign_keys` et `busy_timeout`) ;
- convient à la charge attendue d'un serveur pilotant une seule centrale.

Une évolution vers plusieurs connexions nécessiterait d'appliquer les PRAGMA à
chaque nouvelle connexion et de revoir les tests de concurrence.

## Transactions

Les imports utilisent toujours `BEGIN IMMEDIATE`, au moyen d'une connexion
`database/sql` dédiée. Cela permet de réserver l'écriture dès le début de la
transaction et de conserver l'atomicité des imports.

## Compatibilité des bases existantes

Le changement concerne le pilote Go, pas le format du fichier. Une base créée
par la précédente implémentation SQLite reste une base SQLite standard. Avant
une migration en production, conserver néanmoins une copie du fichier `.db` et
de ses éventuels fichiers `-wal` et `-shm` après un arrêt propre du serveur.
