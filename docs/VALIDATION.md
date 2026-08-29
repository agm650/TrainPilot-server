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

Sur macOS, les sockets Unix ont une longueur de chemin limitée. Si `TestUserAdministrationOverUnixSocket` échoue avec `bind: invalid argument` dans un chemin temporaire long, relancer les tests avec :

```bash
TMPDIR=/tmp go test ./...
```

La CI exécute formatage, tests, détecteur de concurrence et `go vet` sur Linux et macOS. Un job Linux supplémentaire construit une release snapshot GoReleaser pour valider les trois binaires et le contenu des archives.
