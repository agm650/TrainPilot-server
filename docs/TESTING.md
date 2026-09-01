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
- les commandes DCC-EX sont encodées correctement et un faux serveur TCP couvre la connexion initiale, la perte du socket, les transitions `online/degraded/offline`, la reconnexion avant ou après `offline`, plusieurs cycles et l'arrêt pendant une reconnexion ;
- les accessoires DCC-EX utilisent exactement `<a linear 0|1>`, publient uniquement un état `assumed` après succès et ne publient rien après une erreur d'écriture ;
- cent commandes accessoires DCC-EX concurrentes restent des trames complètes, tandis qu'une commande refusée pendant une panne n'est jamais rejouée après reconnexion ;
- un test d'intégration `RailwayService -> DCC-EX TCP` vérifie qu'un turnout simple à l'adresse 44 produit `<a 44 1>` puis atteint sa position logique grâce au retour `assumed` ;
- les commandes DCC-EX présentées sans socket sont refusées sans mise en file ni rejeu, tandis que les retours de capteurs reprennent sur le même canal après reconnexion ;
- les snapshots du simulateur sont profondément copiés, son horloge est injectable, son reset conserve la connexion et ses lectures restent sûres face aux commandes concurrentes ;
- les accessoires simulés distinguent état demandé et confirmé, couvrent les confirmations immédiates, différées, absentes ou incohérentes et ignorent toute confirmation différée devenue obsolète ;
- la télémétrie simulée possède un état nominal déterministe, expose tous les champs électriques de `station.Status` et combine les défauts sans effet implicite sur la puissance ;
- le simulateur permet les transitions `online/degraded/offline`, refuse toute commande active hors ligne, conserve un `LastSeen` cohérent et injecte sans rejeu des délais annulables ou un nombre exact d'erreurs par opération ;
- les capteurs simulés mémorisent leur état physique indépendamment des événements, reproduisent répétitions, rebonds et pertes, signalent la saturation et alimentent simultanément deux cantons via `RailwayService` ;
- les scénarios JSON v2, avec lecture v1 compatible, sont validés intégralement avant exécution, conservent l'ordre des étapes simultanées et sont reproductibles avec `clock.Fake` sans attente réelle ;
- un scénario expose son cycle de vie et son erreur, s'arrête sur une étape impossible et son mode temps réel est annulé par le contexte, `Close()` ou un reset externe du simulateur ;
- l'API de test du simulateur disparaît avec `testAPI=false` ou un pilote matériel, exige une authentification et expose snapshot, reset, connectivité, télémétrie, feedback, accessoires, faults et scénarios sans polluer l'API publique ;
- le feedback injecté par HTTP traverse le mapping de `RailwayService` jusqu'au WebSocket, tandis qu'une connectivité `offline` injectée produit le refus métier `503 station_offline` sans rejeu au retour `online` ;
- les douze scénarios de référence du simulateur exercent les réponses HTTP, les snapshots et événements WebSocket, l'arrêt d'urgence, le court-circuit, les feedbacks multiples/rebond/perte et les confirmations d'accessoire en temps logique ;
- le bus attribue des séquences monotones, expose sa séquence courante et ne bloque pas sur un abonné lent ;
- le WebSocket fournit un snapshot complet, permet la resynchronisation après un trou de séquence, supporte la reconnexion et ferme la connexion à l'expiration du jeton ou à la révocation de la session ;
- les événements anciens ou dupliqués sont filtrés, et un événement publié pendant un snapshot est transmis ensuite sans perte ;
- un client WebSocket trop lent est déconnecté lorsque sa file déborde ou que l'écriture expire ;
- une déconnexion WebSocket ne libère pas le lease, qui reste soumis à son heartbeat et à son expiration normale ;
- le refresh fait tourner les deux jetons, invalide immédiatement les anciens et le logout révoque la session ;
- la conformité opt-in distingue un access token expiré avec refresh encore valide d'un refresh token naturellement expiré ;
- le snapshot conserve les leases complets de la seule session authentifiée et expose séparément l'occupation `mine`, `same_user_other_session` ou `other` de toutes les locomotives contrôlées ;
- une locomotive libre est absente de `locomotiveControlStates`, et les événements d'acquisition, d'arrêt contrôlé et de libération sont diffusés aux autres sessions WebSocket ;
- le takeover est limité aux sessions d'un même utilisateur, conserve le lease, arrête la locomotive sans inverser son dernier sens, laisse les fonctions inchangées et invalide immédiatement les droits de l'ancienne session ;
- la barrière de takeover préempte les commandes ordinaires déjà en attente et son événement est reçu par l'ancien propriétaire, le nouveau et les autres utilisateurs ;
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

Le workflow CI exécute `go test ./...` et `go test -race ./...` sur Linux et
macOS. Sur Linux, il rejoue aussi toute la suite avec `CGO_ENABLED=0`; les tests
de référence du simulateur appartiennent au package `internal/api` et sont donc
obligatoires dans chaque `go test ./...`.

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

Sur une instance jetable configurée avec `station.driver=simulator`, les tests
de conduite et les mutations de configuration peuvent être activés ensemble :

```bash
go run ./cmd/dcc-api-conformance \
  --server http://127.0.0.1:8080 \
  --user1 alice --pass1 correct-horse-1 \
  --user2 bob --pass2 correct-horse-2 \
  --admin admin --admin-pass correct-horse-admin \
  --allow-active-commands \
  --allow-configuration-mutations
```

Ces options restent inactives par défaut et ne doivent pas être utilisées sur
un réseau ferroviaire réel sans activation volontaire.

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

## Aiguillages simples et composés

Les tests de `internal/model` couvrent les appareils simples, triples, TJD,
TJS et personnalisés. Ils vérifient la validation, les vecteurs inconnus et
l'inversion des endpoints.

Les tests de `internal/store` créent aussi une ancienne table `turnouts`, puis
exécutent deux fois la migration. Les tests de `internal/transfer` importent une
archive de circuit version 1 et vérifient un round-trip déterministe en version
2.

```bash
go test ./internal/model ./internal/store ./internal/transfer
```

Le modèle et les exemples sont décrits dans [`TURNOUTS.md`](TURNOUTS.md).

## Scénarios déterministes du simulateur

Le parcours complet destiné aux développeurs de clients est documenté dans
[`CLIENT_SIMULATOR_GUIDE.md`](CLIENT_SIMULATOR_GUIDE.md). Le présent document
se concentre sur les validations du dépôt serveur.

Les fichiers sous `tests/simulator/scenarios/` décrivent le monde extérieur vu
par la centrale simulée. Ils sont distincts des scénarios contractuels HTTP de
`contract-tests/scenarios/` : le moteur n'appelle aucun service métier et ne
modifie aucune base SQLite.

Un fichier contient obligatoirement `version`, `name`, `initial` et `steps`.
La version courante est `2`, les timestamps `at` sont relatifs au démarrage et
utilisent `time.ParseDuration`. Les étapes doivent être triées ; deux étapes au
même instant restent exécutées dans leur ordre JSON. Le parsing rejette avant
démarrage les champs inconnus, actions inconnues, durées invalides, adresses
négatives et champs requis absents.

Le mode manuel associe le simulateur et le runner à la même `clock.Fake`, puis
utilise `Start(ctx)` et `Advance(ctx, durée)`. Une avance peut franchir plusieurs
étapes sans `time.Sleep`. Pour un client interactif, `StartRealtime(ctx)` suit
le temps réel et `Done()` permet d'attendre sa terminaison. `Stop()`, l'annulation
du contexte, `Simulator.Close()` et un reset externe empêchent toute action
future. Une étape `simulator.reset` appartenant au scénario, elle, réinitialise
le banc puis laisse le scénario se poursuivre.

Les douze scénarios de référence obligatoires sont :

- `nominal-driving.json` et `emergency-stop.json` ;
- `station-degraded-recovery.json`, `station-offline-recovery.json` et
  `electrical-short-circuit.json` ;
- `feedback-single-block.json`, `feedback-multiple-blocks.json`,
  `feedback-bounce.json` et `feedback-event-loss.json` ;
- `accessory-confirmation-success.json`,
  `accessory-confirmation-timeout-base.json` et
  `accessory-wrong-confirmation.json`.

`TestReferenceSimulatorScenarios` valide chaque document puis exerce les
scénarios critiques par l'API HTTP/WebSocket d'un serveur `httptest` avec
SQLite et authentification réelles. Il vérifie notamment que la commande
refusée pendant `offline` ne réapparaît pas au retour `online`, que deux cantons
restent occupés simultanément, qu'un feedback perdu ne modifie pas l'état connu
du service et que les trois bases de confirmation d'accessoire restent
observables. Les anciens scénarios complémentaires `feedback-a-to-b.json` et
`accessory-electrical-fault.json` restent disponibles pour les essais ciblés.
Le lecteur accepte aussi les scénarios v1 et convertit leurs états
`straight/diverging` en `position1/position2`.

Les scénarios AIG-003 complètent cette base avec `accessory-simple`,
`accessory-triple`, `accessory-triple-invalid`, `accessory-tjd`,
`accessory-partial-failure` et `accessory-stale-confirmation`. Les tests du
service vérifient la résolution d'un triple, l'état physique interdit, les
quatre positions d'une TJD et une panne ciblée sur le second endpoint.

L'interface HTTP destinée aux tests externes est décrite dans
[`SIMULATOR_TEST_API.md`](SIMULATOR_TEST_API.md). Les tests API vérifient aussi
le routage absent en production, la validation JSON stricte, le snapshot, les
faults, l'avance manuelle et les lectures métier concurrentes sous le détecteur
de races.

Les tests du contrat accessoire vérifient aussi `position1`/`position2`, la
plage linéaire `1..2040`, les erreurs typées, le refus hors ligne et le provider
d'observations. Le simulateur utilise une file non bloquante de 64 événements.

Les tests z21 couvrent sans matériel :

- la conversion réversible adresse linéaire ↔ `FAdr`, avec les bornes et des
  valeurs de référence autour des groupes de quatre ;
- les octets exacts `LAN_X_SET_TURNOUT` pour activation et désactivation de
  `position1` et `position2`, avec `Q=1` ;
- `LAN_X_GET_TURNOUT_INFO` et les quatre valeurs `ZZ` ;
- deux interrogations simultanées dont les réponses arrivent dans l'ordre
  inverse ;
- un broadcast spontané, l'impulsion complète, l'annulation contextuelle avec
  désactivation de sécurité et le refus hors ligne sans datagramme ;
- la configuration des broadcast flags lors de `Connect()`.

## Couverture restant à ajouter

- parité contractuelle complète entre DCC-EX et z21 pour leurs capacités communes ;
- couverture protocolaire DCC-EX au-delà des commandes et retours actuellement pris en charge ;
- rétrosignalisation déjà présente au démarrage réel du serveur ;
- campagnes sur matériel réel.

## Test manuel facultatif DCC-EX

Ce contrôle n'est pas requis par les tests automatisés et ne doit être exécuté
que sur un banc explicitement choisi :

1. démarrer DCC-EX puis `dccd` avec le pilote `dccex` et un
   `station.offlineAfter` court mais adapté au banc ;
2. configurer un décodeur d'accessoires et relever son adresse ;
3. commander `position1`, puis `position2`, et vérifier les trames
   `<a LINEAR 0>` et `<a LINEAR 1>` ;
4. répéter de part et d'autre d'une frontière de groupe de quatre et noter la
   convention d'affichage du matériel ;
5. effectuer plusieurs changements rapides et vérifier l'absence de trame
   corrompue ;
6. vérifier que l'état serveur est `assumed` et non `physical` ;
7. comparer si possible avec un autre client et noter tout retour externe
   réellement observable, sans le supposer ;
8. vérifier que l'état annoncé est `online` et qu'un retour de capteur est reçu ;
9. couper le transport TCP après la connexion initiale et vérifier le passage à
   `degraded`, puis à `offline` si la coupure dépasse le délai ;
10. présenter une commande accessoire pendant la coupure et vérifier son refus ;
11. rétablir DCC-EX et vérifier le retour à `online` ainsi que la reprise des
   retours de capteurs ;
12. vérifier que la commande refusée n'est pas rejouée et qu'une nouvelle
   commande explicite est nécessaire.

Aucune ancienne vitesse, fonction ou position d'accessoire ne doit être
restaurée automatiquement après la reconnexion.

## Tests matériels à ajouter

Les tests matériels n’existent pas encore dans la branche. Lors de leur ajout, ils devront être protégés par un build tag, par exemple :

```bash
go test -tags=hardware ./tests/hardware/z21
```

Ils devront utiliser une locomotive et un accessoire réservés au banc de test, avec une alimentation et un arrêt d’urgence physiques accessibles.

### Procédure z21 accessoire facultative

Ce contrôle n'est pas requis par la CI. Il doit viser une sortie sans risque et
un aiguillage visible, avec arrêt d'urgence accessible :

1. configurer le pilote `z21`, `station.accessoryPulse: "100ms"` et une adresse
   linéaire connue ;
2. vérifier hors tension de traction la correspondance entre l'adresse
   TrainPilot et l'adresse affichée par le décodeur ;
3. commander `position1`, puis `position2`, et vérifier une seule impulsion par
   ordre, suivie de la désactivation ;
4. vérifier que le retour z21 annonce la bonne position de fonction et qu'un
   changement issu d'une autre commande est publié ;
5. annuler une requête pendant une impulsion et vérifier que la sortie est bien
   désactivée ;
6. déconnecter la centrale jusqu'à `offline`, présenter une commande et
   vérifier qu'aucun paquet n'est envoyé ni rejoué au retour `online` ;
7. noter explicitement si le retour de centrale correspond seulement à l'état
   électrique de la sortie ou à une vraie détection mécanique.

Répéter avec les adresses linéaires `1`, `4`, `5`, `8` et `9` permet de détecter
rapidement un décalage de convention par groupe de quatre.
