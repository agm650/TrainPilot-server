# Validation du dépôt

Environnements de référence : Linux et macOS, Go 1.26 ou supérieur, pilote SQLite pur Go `modernc.org/sqlite`. Les livrables distribués sont construits avec `CGO_ENABLED=0`.

## Contrôles de référence

```bash
go mod download
test -z "$(gofmt -l .)"
go test ./...
CGO_ENABLED=0 go test ./...
go test -race ./...
go vet ./...
goreleaser check
goreleaser release --snapshot --clean --skip=publish
```

Le détecteur de concurrence Go nécessite CGO, contrairement aux binaires de distribution. Il est donc exécuté séparément de la validation `CGO_ENABLED=0`.

La validation de sécurité de la conduite inclut les tests de concurrence de
`internal/service` : ils doivent démontrer qu'une commande de sécurité en
attente préempte les nouveaux ordres de traction sans qu'ils atteignent le
pilote, et qu'une reprise exige une action explicite.

La validation contractuelle inclut également :

- la parité entre les routes publiques, OpenAPI et l'inventaire de
  `dcc-api-conformance` ;
- la conformité passive et active exécutée en test contre le simulateur ;
- la rotation/révocation des jetons et l'expiration WebSocket ;
- le snapshot complet, la resynchronisation après trou de séquence et la
  reconnexion ;
- le filtrage des séquences anciennes ou dupliquées, la livraison des événements
  concurrents avec un snapshot, la conservation de l'événement le plus récent
  lors d'un overflow et la fermeture sur expiration d'écriture ;
- les catégories/codes d'erreur stables et le masquage des erreurs internes ;
- les bornes de fonctions propres aux capacités du simulateur, de z21 et de
  DCC-EX.

La validation d'occupation couvre le démarrage en `unknown`, l'agrégation
multi-sources conservative, l'expiration des sources requises, les séquences
hors ordre, les refresh sans événement métier, et la cohérence REST/WebSocket/
snapshot. Les tests de route prouvent que `unknown` et `occupied` bloquent
avant toute commande d'aiguillage, tandis que `free` seul poursuit les autres
validations. Le diagnostic par source et les métriques utilisent des labels
bornés.

Les commandes de conformité actives et les mutations de configuration ne sont
jamais lancées implicitement contre une centrale réelle : elles exigent les
options explicites documentées dans `docs/TESTING.md`.

La validation automatisée du feedback vérifie la migration des mappings, la
traduction active/inactive, l'invalidation sur `offline` et l'absence de faux
`free` au retour `online`. Elle utilise le simulateur et les faux pilotes. Elle
ne remplace pas les essais physiques du Roco 10819 : chaque entrée, les états
simultanés, le redémarrage et la coupure/reprise z21 restent à confirmer sur le
réseau réel.

Sur macOS, les sockets Unix ont une longueur de chemin limitée. Si `TestUserAdministrationOverUnixSocket` échoue avec `bind: invalid argument` dans un chemin temporaire long, relancer les tests avec :

```bash
TMPDIR=/tmp go test ./...
```

La CI exécute formatage, tests, détecteur de concurrence et `go vet` sur Linux
et macOS. Un job Ubuntu exécute aussi un smoke benchmark fonctionnel sur le
simulateur. Un autre job Linux construit une release snapshot GoReleaser pour
valider les quatre binaires et le contenu des archives.

Les tests Go vérifient structurellement les profils, rapports, dashboards JSON
et règles YAML. Ils ne prouvent pas un import Grafana, une validation
`promtool`, un soak réel de 6 ou 24 heures, ni un résultat matériel.
