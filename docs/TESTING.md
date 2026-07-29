# Stratégie de test

## Niveaux

- **Unitaires** : fonctions de mot de passe, matrice de permissions, leases, feedback, archives, encodage Z21 et commandes DCC-EX.
- **Concurrence** : deux acquisitions simultanées doivent produire exactement un gagnant.
- **Intégration** : serveur HTTP réel via `httptest`, SQLite réelle, centrale simulée et authentification réelle.
- **Conformité externe** : `dcc-api-conformance` s’exécute contre un processus actif.
- **Matériel** : à ajouter pour z21 blanche, Z21 noire et DCC-EX.

## Invariants couverts

- un seul lease vivant par locomotive ;
- un viewer ne peut pas conduire ;
- le propriétaire du lease peut commander la vitesse ;
- une réservation expirée commande l’arrêt avant la libération ;
- la locomotive reste indisponible pendant l’état `stopping` ;
- un capteur mappé modifie le canton correspondant ;
- la création d’utilisateur n’existe pas dans l’API publique ;
- le socket Unix d’administration permet la création, la liste et la désactivation ;
- les paquets de mise sous/hors tension Z21 ont la forme attendue ;
- une commande de vitesse DCC-EX est encodée correctement ;
- les archives parc/circuit passent un aller-retour sans perte ;
- un rôle driver peut exporter mais ne peut pas importer ;
- un import invalide ou contenant des références cassées est rejeté sans modification partielle.

## Commandes

```bash
go test ./...
go test -race ./...
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out
```

## Tests matériels à ajouter

Les tests matériels doivent être protégés par un build tag, par exemple :

```bash
go test -tags=hardware ./tests/hardware/z21
```

Ils devront utiliser une locomotive et un accessoire réservés au banc de test, avec une alimentation et un arrêt d’urgence physiques accessibles.
