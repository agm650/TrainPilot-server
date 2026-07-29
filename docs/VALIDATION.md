# Validation effectuée sur le dépôt généré

Environnement de validation : Linux x86-64, Go 1.23.2, CGO activé et SQLite système.

Commandes exécutées avec succès :

```bash
gofmt -w .
go test ./...
go test -race ./...
go vet ./...
make build
```

Validation documentaire :

- parsing YAML réussi pour `api/openapi.yaml` ;
- parsing YAML réussi pour `api/asyncapi.yaml` ;
- chargement de tous les scénarios JSON contractuels ;
- aller-retour réussi pour les archives de parc et de circuit ;
- vérification qu’un circuit invalide ne modifie pas la base.

Validation de bout en bout :

1. démarrage de `dccd` avec la centrale simulée et une base SQLite neuve ;
2. création d’Alice par `dccd user bootstrap` sur le socket Unix ;
3. création de Bob et d’un administrateur par `dccd user add` ;
4. exécution de `dcc-api-conformance` contre le processus actif ;
5. export du parc et du circuit avec `dccctl` ;
6. réimport du circuit par le compte administrateur ;
7. inspection du contenu ZIP et de ses manifestes.

Résultat de conformité :

```text
PASS  valid user can authenticate
PASS  second user can authenticate
PASS  authenticated client lists locomotives
PASS  free locomotive can be reserved
PASS  second user cannot reserve same locomotive
PASS  lease owner can change speed
PASS  lease owner can release control
PASS  public API does not expose user creation
PASS  authenticated client can export rolling stock
PASS  non-administrator cannot import rolling stock

Result: 10 passed, 0 failed
```

Les pilotes physiques DCC-EX et Z21 n’ont pas été testés sur du matériel réel dans cet environnement. Leurs tests actuels portent sur l’encodage protocolaire, le transport simulé et le parsing de base. La commande d’accessoires Z21 reste à compléter après validation sur une z21/Z21 réelle.
