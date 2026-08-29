# Stratégie de test

## Niveaux

- **Unitaires et composants** : fonctions de mot de passe, matrice de permissions, utilisateurs, leases, état de centrale, bus d’événements, feedback, itinéraires, archives, simulateur et codecs des pilotes.
- **Concurrence** : deux acquisitions simultanées doivent produire exactement un gagnant ; une commande de sécurité en attente doit préempter les nouveaux ordres de conduite.
- **Intégration** : serveur HTTP réel via `httptest`, SQLite réelle, centrale simulée et authentification réelle.
- **Scénarios contractuels** : les fichiers de `contract-tests/scenarios/` sont validés et décrivent les comportements partageables avec les clients natifs.
- **Conformité externe** : `dcc-api-conformance` s’exécute contre un processus actif.
- **Matériel** : à ajouter pour z21 blanche, Z21 noire et DCC-EX.

## Invariants couverts

- un seul lease vivant par locomotive ;
- un viewer ne peut pas conduire ;
- le propriétaire du lease peut commander la vitesse ;
- une commande valide renouvelle le lease et une commande tardive ne peut pas le réactiver ;
- un heartbeat juste avant l'expiration renouvelle le lease, tandis qu'un heartbeat à l'expiration ou après celle-ci est refusé ;
- une réservation expirée commande l’arrêt avant la libération ;
- la locomotive reste indisponible pendant l’état `stopping` ;
- une session étrangère ou un lease libéré ne peut pas commander la locomotive ;
- les commandes de traction et de fonctions sont refusées lorsque la centrale simulée est hors ligne ;
- les vitesses positives et les fonctions sont refusées lorsque la puissance est coupée ou inconnue, ou lorsque l'arrêt d'urgence est actif ;
- l'arrêt d'urgence, la coupure de puissance et la vitesse zéro passent avant les commandes ordinaires en attente, qui sont refusées sans atteindre le pilote ;
- la reprise après arrêt d'urgence nécessite un ordre `power on` explicite réussi ;
- les transitions `online`, `degraded`, `offline` et le retour à `online` sont couvertes au niveau du suivi de santé ;
- un capteur mappé modifie le canton correspondant ;
- un itinéraire occupé ou en conflit ne peut pas être réservé et une activation hors ligne échoue ;
- la création d’utilisateur n’existe pas dans l’API publique ;
- le socket Unix d’administration permet la création, la liste et la désactivation ;
- les paquets de puissance et de statut Z21 ont la forme attendue et les réponses d’état sont décodées ;
- une commande de vitesse DCC-EX est encodée correctement ;
- le bus attribue des séquences monotones, expose sa séquence courante et ne bloque pas sur un abonné lent ;
- le WebSocket fournit un snapshot complet, permet la resynchronisation après un trou de séquence, supporte la reconnexion et ferme la connexion à l'expiration du jeton ou à la révocation de la session ;
- les événements anciens ou dupliqués sont filtrés, et un événement publié pendant un snapshot est transmis ensuite sans perte ;
- un client WebSocket trop lent est déconnecté lorsque sa file déborde ou que l'écriture expire ;
- une déconnexion WebSocket ne libère pas le lease, qui reste soumis à son heartbeat et à son expiration normale ;
- le refresh fait tourner les deux jetons, invalide immédiatement les anciens et le logout révoque la session ;
- la conformité opt-in distingue un access token expiré avec refresh encore valide d'un refresh token naturellement expiré ;
- chaque problème HTTP possède une catégorie et un code stable, et les erreurs internes sont masquées ;
- les sens et numéros de fonctions sont validés avant le pilote selon ses capacités déclarées ;
- les archives parc/circuit passent un aller-retour sans perte ;
- un rôle driver peut exporter mais ne peut pas importer ;
- un import invalide ou contenant des références cassées est rejeté sans modification partielle.

## Commandes

```bash
go test ./...
CGO_ENABLED=0 go test ./...
go test -race ./...
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out
```

La conformité HTTP passive, sans commande de voie, s'exécute contre un serveur
déjà démarré avec :

```bash
go run ./cmd/dcc-api-conformance --server http://127.0.0.1:8080 \
  --user1 alice --pass1 correct-horse-1 \
  --user2 bob --pass2 correct-horse-2
```

`--list-endpoints` affiche l'inventaire vérifié avec OpenAPI et les routes du
serveur. Les scénarios qui commandent la centrale exigent
`--allow-active-commands`. Le CRUD temporaire et les imports exigent aussi
`--allow-configuration-mutations` et un compte administrateur. Ces deux modes
ne doivent être utilisés que sur une instance de test explicitement choisie.

L'expiration naturelle des sessions est volontairement absente du lancement
standard. Elle nécessite une instance de test avec des TTL courtes :

```json
"security": {
  "accessTokenTTL": "2s",
  "refreshTokenTTL": "5s"
}
```

```bash
go run ./cmd/dcc-api-conformance \
  --server http://127.0.0.1:8080 \
  --user1 alice --pass1 correct-horse-1 \
  --user2 bob --pass2 correct-horse-2 \
  --check-session-expiration \
  --session-expiration-max-wait 15s
```

Le scénario emploie une session dédiée à l'expiration de l'access token et une
autre à celle du refresh token. Il utilise les dates d'expiration retournées
par le serveur, ajoute une courte marge et attend avec un timer interruptible
par le contexte. `--session-expiration-max-wait`, égal à `15s` par défaut,
refuse immédiatement une expiration trop éloignée ; il faut alors raccourcir
les TTL du serveur de test ou augmenter explicitement cette limite.

Sur macOS, utiliser `TMPDIR=/tmp go test ./...` si le chemin temporaire par défaut rend le nom du socket Unix d’administration trop long.

## Couverture restant à ajouter

- surveillance de disponibilité, perte de connexion et reconnexion DCC-EX ;
- couverture DCC-EX des commandes autres que la vitesse et des retours de capteurs ;
- scénarios de rétrosignalisation simultanée, répétée et présente au démarrage ;
- campagnes sur matériel réel.

## Tests matériels à ajouter

Les tests matériels n’existent pas encore dans la branche. Lors de leur ajout, ils devront être protégés par un build tag, par exemple :

```bash
go test -tags=hardware ./tests/hardware/z21
```

Ils devront utiliser une locomotive et un accessoire réservés au banc de test, avec une alimentation et un arrêt d’urgence physiques accessibles.
