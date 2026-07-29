# Validation effectuée sur le dépôt généré

+Environnement de validation de référence : Linux x86-64, Go 1.23 ou supérieur, `CGO_ENABLED=0`, pilote SQLite `modernc.org/sqlite`.

+Commandes attendues :

```bash
go mod download
gofmt -w .
go test ./...
go test -race ./...
go vet ./...
make build
```
