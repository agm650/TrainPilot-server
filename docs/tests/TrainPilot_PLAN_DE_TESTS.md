# TrainPilot-server — Plan de tests et de validation

**État de référence : branche `main`, revue le 2 septembre 2026**

Dépôt : https://github.com/agm650/TrainPilot-server

## 1. Objet

Cette check-list regroupe les validations à exécuter compte tenu de l'état actuel du serveur. Elle distingue les tests automatiques, les tests réalisables avec le simulateur et les campagnes nécessitant du matériel réel.

### Légende

- `[ ]` test à exécuter ou conserver dans la recette.
- `[x]` comportement déjà observé manuellement lors des essais précédents, mais à conserver en non-régression.
- `AUTO` couvert par la suite Go/CI.
- `SIM` réalisable sans matériel.
- `Z21`, `DCCEX`, `10819` nécessitent le matériel correspondant.
- `MANUAL` nécessite une observation humaine.
- Résultat matériel : `PASS`, `FAIL`, `BLOCKED`, `NOT_TESTED`.

Les fonctionnalités encore non développées — topologie complète, localisation des trains, itinéraires sécurisés complets et signalisation — sont hors recette fonctionnelle pour le moment.

---

# 2. Gate logiciel obligatoire

À exécuter avant toute release et après toute correction issue d'une campagne matérielle.

- [ ] `AUTO` `go mod download`
- [ ] `AUTO` `test -z "$(gofmt -l .)"`
- [ ] `AUTO` `go test ./...`
- [ ] `AUTO` `CGO_ENABLED=0 go test ./...`
- [ ] `AUTO` `go test -race ./...`
- [ ] `AUTO` `go vet ./...`
- [ ] `AUTO` `go test -coverprofile=coverage.out ./...`
- [ ] `AUTO` `goreleaser check`
- [ ] `AUTO` `goreleaser release --snapshot --clean --skip=publish`

Attendu : aucune erreur, aucune data race, les trois binaires `dccd`, `dccctl`, `dcc-api-conformance` présents dans les livrables et aucune régression manifeste de couverture.

---

# 3. Conformité API

## 3.1 Conformité passive

```bash
go run ./cmd/dcc-api-conformance   --server http://127.0.0.1:8080   --user1 alice --pass1 correct-horse-1   --user2 bob --pass2 correct-horse-2
```

- [ ] `AUTO/SIM` Inventaire des routes cohérent avec OpenAPI.
- [ ] `AUTO/SIM` Méthodes HTTP correctes.
- [ ] `AUTO/SIM` Codes d'erreur publics stables.
- [ ] `AUTO/SIM` Erreurs internes masquées.
- [ ] `AUTO/SIM` Aucun endpoint HTTP public de création d'utilisateur.

## 3.2 Conformité active sur une instance jetable Simulator

```bash
go run ./cmd/dcc-api-conformance   --server http://127.0.0.1:8080   --user1 alice --pass1 correct-horse-1   --user2 bob --pass2 correct-horse-2   --admin admin --admin-pass correct-horse-admin   --allow-active-commands   --allow-configuration-mutations
```

- [ ] `SIM` Conduite active.
- [ ] `SIM` Mutations de configuration.
- [ ] `SIM` Import/export temporaire.
- [ ] `SIM` Nettoyage correct.

Ne jamais utiliser ces options implicitement sur un réseau réel.

## 3.3 Conformité aiguillages

```bash
go run ./cmd/dcc-api-conformance   --server http://127.0.0.1:8080   --user1 alice --pass1 correct-horse-1   --user2 bob --pass2 correct-horse-2   --admin admin --admin-pass correct-horse-admin   --check-turnouts
```

- [ ] `SIM` Définitions valides.
- [ ] `SIM` Commande d'une position déclarée.
- [ ] `SIM` Confirmation de position.
- [ ] `SIM` Position inexistante → `invalid_turnout_position`.
- [ ] `SIM` Triple : trois positions parcourues.
- [ ] `SIM` La combinaison interdite n'est jamais exposée comme position logique.

## 3.4 Expiration naturelle des sessions

Configuration de test :

```json
{
  "security": {
    "accessTokenTTL": "2s",
    "refreshTokenTTL": "5s"
  }
}
```

Commande :

```bash
go run ./cmd/dcc-api-conformance   --server http://127.0.0.1:8080   --user1 alice --pass1 correct-horse-1   --user2 bob --pass2 correct-horse-2   --check-session-expiration   --session-expiration-max-wait 15s
```

- [ ] `SIM` Access token valide avant expiration.
- [ ] `SIM` Access token refusé après expiration.
- [ ] `SIM` Refresh encore valide après expiration de l'access token.
- [ ] `SIM` Nouveau token accepté après refresh.
- [ ] `SIM` Refresh expiré refusé.

---

# 4. Authentification, rôles et administration

## 4.1 Sessions

- [ ] `AUTO/SIM` Login valide.
- [ ] `AUTO/SIM` Mauvais mot de passe refusé.
- [ ] `AUTO/SIM` Refresh fait tourner access + refresh token.
- [ ] `AUTO/SIM` Anciens tokens immédiatement invalides.
- [ ] `AUTO/SIM` Logout révoque la session.

## 4.2 Rôles

### Viewer
- [ ] `AUTO/SIM` Peut consulter.
- [ ] `AUTO/SIM` Ne peut pas conduire.
- [ ] `AUTO/SIM` Ne peut pas commander un aiguillage.

### Driver
- [ ] `AUTO/SIM` Peut acquérir/conduire une locomotive.
- [ ] `AUTO/SIM` Peut exporter.
- [ ] `AUTO/SIM` Import refusé si le contrat l'interdit.

### Dispatcher / Administrator
- [ ] `AUTO/SIM` Peut commander les aiguillages.
- [ ] `AUTO/SIM` Peut effectuer les mutations autorisées.

## 4.3 Socket Unix d'administration

- [ ] `AUTO` Bootstrap seulement sur table utilisateur vide.
- [ ] `AUTO` Création utilisateur.
- [ ] `AUTO` Liste utilisateurs.
- [ ] `AUTO` Désactivation utilisateur.
- [ ] `AUTO` Révocation des sessions lors de la désactivation.
- [ ] `AUTO` Permissions du socket correctes.

Sous macOS, si nécessaire :

```bash
TMPDIR=/tmp go test ./...
```

---

# 5. Leases et multi-utilisateurs

## 5.1 Acquisition et heartbeat

- [ ] `AUTO/SIM` Locomotive libre acquise.
- [ ] `AUTO/SIM` Lease rattaché à la bonne session.
- [ ] `AUTO/SIM` Heartbeat renouvelle le lease.
- [ ] `AUTO/SIM` Heartbeat à/après expiration refusé.
- [ ] `AUTO/SIM` Commande tardive ne ressuscite pas un lease expiré.

## 5.2 Conflit deux sessions

- [ ] `AUTO/SIM` Session A acquiert la locomotive.
- [ ] `AUTO/SIM` Session B tente de l'acquérir.
- [ ] `AUTO/SIM` Session B est refusée.
- [ ] `AUTO/SIM` Il existe exactement un propriétaire.

Ce scénario doit utiliser deux sessions distinctes.

## 5.3 Arrêt contrôlé à expiration

- [ ] `AUTO/SIM` Vitesse zéro avant libération.
- [ ] `AUTO/SIM` État `stopping` visible.
- [ ] `AUTO/SIM` Pas d'acquisition par un tiers pendant `stopping`.
- [ ] `AUTO/SIM` Libération après la séquence d'arrêt.

## 5.4 Takeover même utilisateur

- [ ] `AUTO/SIM` Reprise depuis une autre session du même utilisateur.
- [ ] `AUTO/SIM` Lease conservé.
- [ ] `AUTO/SIM` Locomotive arrêtée avant takeover.
- [ ] `AUTO/SIM` Direction non inversée.
- [ ] `AUTO/SIM` Fonctions conservées.
- [ ] `AUTO/SIM` Ancienne session perd ses droits.
- [ ] `AUTO/SIM` Événement propagé à tous les observateurs.

---

# 6. Conduite et sécurité de traction

## 6.1 z21 déjà observé

- [x] `Z21/MANUAL` Marche avant faible.
- [x] `Z21/MANUAL` Marche arrière faible.
- [x] `Z21/MANUAL` Vitesse zéro.
- [x] `Z21/MANUAL` Vitesse élevée.
- [x] `Z21/MANUAL` F0.
- [x] `Z21/MANUAL` Arrêt d'urgence.

À rejouer après modification importante du driver z21 ou du service de conduite.

## 6.2 Fonctions et bornes

- [ ] `Z21/MANUAL` Tester au moins une fonction > F0 si disponible.
- [ ] `AUTO/SIM` Fonction hors capacité refusée avant le driver.
- [ ] `AUTO/SIM` Numéro hors plage refusé.
- [ ] `AUTO/SIM` Fonction refusée lorsque station offline.

## 6.3 Safety priority

- [ ] `AUTO/SIM` Emergency stop préempte les commandes ordinaires.
- [ ] `AUTO/SIM` Power off préempte les commandes ordinaires.
- [ ] `AUTO/SIM` Vitesse zéro préempte les commandes ordinaires.
- [ ] `AUTO/SIM` Reprise après E-stop nécessite `power on` explicite.
- [ ] `Z21/MANUAL` STOP depuis MultiMaus/Z21 propagé vers API/WebSocket.

---

# 7. WebSocket

## 7.1 Snapshot et séquences

- [ ] `AUTO/SIM` Snapshot initial complet.
- [ ] `AUTO/SIM` Séquence du snapshot = séquence courante.
- [ ] `AUTO/SIM` Séquences monotones.
- [ ] `AUTO/SIM` Événements anciens/dupliqués filtrés.
- [ ] `AUTO/SIM` Événement publié pendant snapshot non perdu.
- [ ] `AUTO/SIM` Trou de séquence → resynchronisation par snapshot.

## 7.2 Reconnexion

- [ ] `AUTO/SIM` Déconnexion/reconnexion.
- [ ] `AUTO/SIM` Nouveau snapshot cohérent.
- [ ] `AUTO/SIM` Pas de double événement.
- [ ] `AUTO/SIM` Déconnexion WebSocket ne libère pas le lease.

## 7.3 Sécurité de session

- [ ] `AUTO/SIM` Token expiré ferme la connexion.
- [ ] `AUTO/SIM` Session révoquée ferme la connexion.
- [ ] `AUTO/SIM` Client trop lent déconnecté.
- [ ] `AUTO/SIM` Un client lent ne bloque pas le bus.

---

# 8. Simulateur déterministe

Le lot SIM-001 à SIM-008 est logiciellement terminé. Ces invariants doivent rester verts.

## 8.1 État

- [ ] `AUTO` Snapshot profondément copié.
- [ ] `AUTO` Horloge fake injectable.
- [ ] `AUTO` Reset déterministe.
- [ ] `AUTO` Lectures/commandes concurrentes sans race.

## 8.2 Connectivité et faults

- [ ] `SIM` `online → degraded → online`.
- [ ] `SIM` `online → degraded → offline → online`.
- [ ] `SIM` Commandes refusées offline.
- [ ] `SIM` Aucun rejeu après retour online.
- [ ] `SIM` Erreur unique injectée.
- [ ] `SIM` N erreurs exactement.
- [ ] `SIM` Délai annulable par contexte.
- [ ] `SIM` Opération échouée ne modifie pas l'état.

## 8.3 Télémétrie

- [ ] `SIM` Courant principal/filtré.
- [ ] `SIM` Température.
- [ ] `SIM` Tension alimentation/voie.
- [ ] `SIM` High temperature.
- [ ] `SIM` Power lost.
- [ ] `SIM` Courts-circuits externe/interne.

## 8.4 Feedback

- [ ] `SIM` Occupation/libération simple.
- [ ] `SIM` Deux cantons simultanés.
- [ ] `SIM` Répétition.
- [ ] `SIM` Rebond.
- [ ] `SIM` Perte volontaire d'événement.
- [ ] `SIM` Saturation signalée.

## 8.5 12 scénarios de référence

- [ ] `nominal-driving.json`
- [ ] `emergency-stop.json`
- [ ] `station-degraded-recovery.json`
- [ ] `station-offline-recovery.json`
- [ ] `electrical-short-circuit.json`
- [ ] `feedback-single-block.json`
- [ ] `feedback-multiple-blocks.json`
- [ ] `feedback-bounce.json`
- [ ] `feedback-event-loss.json`
- [ ] `accessory-confirmation-success.json`
- [ ] `accessory-confirmation-timeout-base.json`
- [ ] `accessory-wrong-confirmation.json`


---

# 9. Import / export / migrations

## 9.1 Parc et layout

- [ ] `AUTO/SIM` Export locomotives.
- [ ] `AUTO/SIM` Réimport sans perte.
- [ ] `AUTO/SIM` Export cantons/mappings.
- [ ] `AUTO/SIM` Export aiguillage simple.
- [ ] `AUTO/SIM` Export triple.
- [ ] `AUTO/SIM` Export TJD.
- [ ] `AUTO/SIM` Export itinéraires existants.
- [ ] `AUTO/SIM` Round-trip déterministe.
- [ ] `AUTO/SIM` Import invalide rejeté sans modification partielle.
- [ ] `AUTO/SIM` Références cassées rejetées.

## 9.2 Compatibilité

- [ ] `AUTO` Import layout v1.
- [ ] `AUTO` Import layout v2.
- [ ] `AUTO` Export au format courant v3.
- [ ] `AUTO` Ancien `dccAddress + straight/diverging` migré en simple.
- [ ] `AUTO` Migration SQLite idempotente.
- [ ] `AUTO` États runtime des aiguillages non restaurés depuis archive.

---

# 10. Aiguillages — validation logicielle

## 10.1 Modèle

- [ ] `AUTO` Simple valide.
- [ ] `AUTO` Triple valide.
- [ ] `AUTO` TJD valide.
- [ ] `AUTO` TJS valide.
- [ ] `AUTO` Custom valide.
- [ ] `AUTO` Endpoint inconnu rejeté.
- [ ] `AUTO` Adresse invalide rejetée.
- [ ] `AUTO` Vecteurs dupliqués rejetés.
- [ ] `AUTO` Inversion endpoint correcte.
- [ ] `AUTO` Combinaison non déclarée → `invalid/unknown`.

## 10.2 Triple

- [ ] `AUTO/SIM` `left`.
- [ ] `AUTO/SIM` `straight`.
- [ ] `AUTO/SIM` `right`.
- [ ] `AUTO/SIM` 4e combinaison jamais commandable.
- [ ] `AUTO/SIM` 4e combinaison imposée extérieurement → `invalid/unknown`.
- [ ] `AUTO/SIM` `left → right` via chemin sûr.
- [ ] `AUTO/SIM` `right → left` via chemin sûr.
- [ ] `AUTO/SIM` Aucun vecteur interdit maintenu pendant transition.

## 10.3 TJD

- [ ] `AUTO/SIM` 4 positions représentables.
- [ ] `AUTO/SIM` 12 transitions ordonnées testées.
- [ ] `AUTO/SIM` Séquence déterministe.

## 10.4 Confirmation / erreurs

- [ ] `AUTO/SIM` Confirmation immédiate.
- [ ] `AUTO/SIM` Confirmation différée.
- [ ] `AUTO/SIM` Absence de confirmation.
- [ ] `AUTO/SIM` Confirmation incohérente.
- [ ] `AUTO/SIM` Confirmation obsolète ignorée.
- [ ] `AUTO/SIM` `desiredPosition != reportedPosition` supporté.
- [ ] `AUTO/SIM` Changement externe ne renvoie pas automatiquement l'ancienne consigne.
- [ ] `AUTO/SIM` Timeout conserve desired, termine pending et marque `commandStatus=timeout`.
- [ ] `AUTO/SIM` Échec partiel ne produit aucun faux succès.
- [ ] `AUTO/SIM` Aucun rollback aveugle.

## 10.5 Concurrence

- [ ] `AUTO` Commandes même turnout sérialisées.
- [ ] `AUTO` Turnouts indépendants parallèles.
- [ ] `AUTO` 20 turnouts / 100 commandes concurrentes.
- [ ] `AUTO` Changements externes parallèles.
- [ ] `AUTO` Aucune race.

---

# 11. Aiguillages — REST / WebSocket / CLI

## 11.1 REST

- [ ] `AUTO/SIM` `GET /api/v1/turnouts`.
- [ ] `AUTO/SIM` Positions valides exposées.
- [ ] `AUTO/SIM` `PUT {"position":"..."}`.
- [ ] `AUTO/SIM` `204` après confirmation finale selon le contrat actuel.
- [ ] `AUTO/SIM` Invalide → `400 invalid_turnout_position`.
- [ ] `AUTO/SIM` Busy → `409 turnout_busy`.
- [ ] `AUTO/SIM` Échec → `409 turnout_transition_failed`.
- [ ] `AUTO/SIM` Timeout → `409 turnout_confirmation_timeout`.
- [ ] `AUTO/SIM` Offline → `503 station_offline`.
- [ ] `AUTO/SIM` Driver unsupported → `409 station_unsupported`.
- [ ] `AUTO/SIM` Ancien `state` accepté uniquement pour simple.
- [ ] `AUTO/SIM` `state + position` refusé.

## 11.2 WebSocket

- [ ] `AUTO/SIM` `turnout.commanded`.
- [ ] `AUTO/SIM` `turnout.state.changed`.
- [ ] `AUTO/SIM` `turnout.command.failed`.
- [ ] `AUTO/SIM` desired/reported/pending/quality/status présents.
- [ ] `AUTO/SIM` Snapshot pendant commande montre `pending=true`.

## 11.3 CLI

- [ ] `AUTO/SIM` `dccctl turnouts`.
- [ ] `AUTO/SIM` `dccctl turnout T3 --positions`.
- [ ] `AUTO/SIM` `dccctl turnout T3 right`.
- [ ] `AUTO/SIM` Position invalide affiche les choix valides.

---

# 12. Pilote z21 — logiciel

## 12.1 Locomotive / puissance / état

- [ ] `AUTO` Paquets puissance/statut.
- [ ] `AUTO` Décodage état centrale.
- [ ] `AUTO` Throttle.
- [ ] `AUTO` Fonctions.
- [ ] `AUTO` Télémétrie supportée.

## 12.2 Santé

- [ ] `AUTO` Réponse valide → online.
- [ ] `AUTO` Première erreur → degraded.
- [ ] `AUTO` `offlineAfter` → offline.
- [ ] `AUTO` Nouvelle réponse → online.
- [ ] `AUTO` Erreurs répétées ne repoussent pas `failureSince`.
- [ ] `AUTO` Commandes actives refusées offline.

## 12.3 Accessoires

- [ ] `AUTO` Adresse linéaire `1..2040`.
- [ ] `AUTO` Conversion FAdr correcte.
- [ ] `AUTO` position1/position2 correctement encodées.
- [ ] `AUTO` Activation A=1 et désactivation A=0.
- [ ] `AUTO` Politique Q cohérente.
- [ ] `AUTO` Désactivation de sécurité malgré annulation pendant pulse.
- [ ] `AUTO` GET info corrélé par adresse.
- [ ] `AUTO` Réponses concurrentes correctement routées.
- [ ] `AUTO` Broadcast externe parsé.
- [ ] `AUTO` États z21 inconnus/invalides non transformés en position valide.

---

# 13. z21 réelle — traction et puissance

Comportements déjà observés, à conserver en non-régression :

- [x] `Z21/MANUAL` Connexion centrale.
- [x] `Z21/MANUAL` Acquire locomotive.
- [x] `Z21/MANUAL` Faible vitesse avant.
- [x] `Z21/MANUAL` Faible vitesse arrière.
- [x] `Z21/MANUAL` Zéro.
- [x] `Z21/MANUAL` Vitesse élevée.
- [x] `Z21/MANUAL` F0.
- [x] `Z21/MANUAL` Emergency stop.
- [x] `Z21/MANUAL` Coupure/reconnexion : aucune reprise spontanée.
- [x] `Z21/MANUAL` Nouvelle commande explicite nécessaire après reconnexion.

À rejouer après les dernières modifications :

- [ ] `Z21/MANUAL` Power off/on.
- [ ] `Z21/MANUAL` `power status`.
- [ ] `Z21/MANUAL` F1 ou autre fonction > F0 si disponible.
- [ ] `Z21/MANUAL` STOP depuis MultiMaus/Z21 → API/WebSocket.

---

# 14. z21 réelle — intermittence réseau

C'est un test encore réellement ouvert.

- [ ] `Z21/MANUAL` Perte UDP courte < `offlineAfter`.
- [ ] `Z21/MANUAL` Passage degraded puis retour online.
- [ ] `Z21/MANUAL` Plusieurs pertes courtes successives.
- [ ] `Z21/MANUAL` Perte > `offlineAfter`.
- [ ] `Z21/MANUAL` Passage offline.
- [ ] `Z21/MANUAL` Throttle refusé offline.
- [ ] `Z21/MANUAL` Fonction refusée offline.
- [ ] `Z21/MANUAL` Aiguillage refusé offline.
- [ ] `Z21/MANUAL` Aucun rejeu au retour.
- [ ] `Z21/MANUAL` Nouvelle commande explicite fonctionne.
- [ ] `Z21/MANUAL` Répéter au moins 5 cycles complets.
- [ ] `Z21/MANUAL` API/WebSocket reflètent online/degraded/offline/online.
- [ ] `Z21/MANUAL` Pas de blocage ni croissance anormale des goroutines.

---

# 15. Roco 10819 / R-BUS — validation matérielle

## 15.1 Mapping

Pour chaque entrée utilisée, relever :

```text
entrée physique | adresse z21-rbus | block_id | zone
```

- [ ] `10819/MANUAL` Mapping complet documenté.
- [ ] `10819/MANUAL` Pas de collision/mauvais canton.

## 15.2 Occupation simple

Pour chaque zone :

- [ ] `10819/MANUAL` Libre → `occupied=false`.
- [ ] `10819/MANUAL` Charge/loco → `occupied=true`.
- [ ] `10819/MANUAL` Retrait → `occupied=false`.
- [ ] `10819/MANUAL` Event `block.occupancy.changed`.
- [ ] `10819/MANUAL` Snapshot cohérent.

## 15.3 Occupations successives

Faire circuler :

```text
A → A+B → B → B+C → C
```

- [ ] `10819/MANUAL` Ordre cohérent.
- [ ] `10819/MANUAL` Pas de perte d'occupation.
- [ ] `10819/MANUAL` Pas de libération prématurée.

## 15.4 Occupations simultanées

- [ ] `10819/MANUAL` Deux zones simultanées.
- [ ] `10819/MANUAL` Trois zones si faisable.
- [ ] `10819/MANUAL` États indépendants.

## 15.5 Réseau d'essai prévu

- [ ] `10819/MANUAL` Rouge extérieur 1.
- [ ] `10819/MANUAL` Rouge extérieur 2.
- [ ] `10819/MANUAL` Rouge extérieur 3.
- [ ] `10819/MANUAL` Rouge intérieur 1.
- [ ] `10819/MANUAL` Rouge intérieur 2.

## 15.6 Redémarrage avec canton déjà occupé

1. laisser une locomotive sur une zone ;
2. arrêter `dccd` ;
3. conserver z21 + 10819 actifs ;
4. redémarrer `dccd`.

- [ ] `10819/MANUAL` État occupé finalement récupéré.
- [ ] `10819/MANUAL` Snapshot converge vers la réalité.
- [ ] `10819/MANUAL` Délai de convergence relevé.
- [ ] `10819/MANUAL` Pas d'état libre incorrect permanent.

Si l'état ne remonte qu'au changement suivant, classer le test `FAIL` ou limitation à corriger.

---

# 16. Aiguillages z21 — campagne matérielle AIG-009

Référence dépôt :

```text
docs/hardware-tests/turnouts/README.md
```

## 16.1 Préparation

Créer une fiche datée depuis `z21-TEMPLATE.md` et renseigner :

- [ ] commit/version TrainPilot ;
- [ ] OS/machine ;
- [ ] z21 + firmware ;
- [ ] décodeur accessoire ;
- [ ] actionneur/charge ;
- [ ] adresse constructeur ;
- [ ] adresse linéaire TrainPilot ;
- [ ] mapping position1/position2 ;
- [ ] `offlineAfter` ;
- [ ] `accessoryPulse` ;
- [ ] `confirmationTimeout`.

Faire d'abord :

```bash
scripts/test-turnouts.sh \
  --server http://127.0.0.1:8080 \
  --username dispatcher \
  --password-env DCC_PASSWORD \
  --turnout T1 \
  --positions straight,diverging \
  --cycles 20 \
  --dry-run
```

- [ ] `Z21` Dry-run vérifié.

## 16.2 Simple

- [ ] `Z21/MANUAL` position1 → une seule activation correcte.
- [ ] `Z21/MANUAL` position2 → autre sortie.
- [ ] `Z21/MANUAL` 20 allers-retours.
- [ ] `Z21/MANUAL` Pas de commande manquante/doublée.
- [ ] `Z21/MANUAL` Retour = qualité `station`.
- [ ] `Z21/MANUAL` Pas d'échauffement anormal.

## 16.3 Adressage

Tester :

- [ ] `Z21/MANUAL` adresse 1.
- [ ] `Z21/MANUAL` adresse 4.
- [ ] `Z21/MANUAL` adresse 5.
- [ ] `Z21/MANUAL` adresse 8.
- [ ] `Z21/MANUAL` adresse 9.

Noter :

```text
affichage constructeur | adresse TrainPilot | sortie réelle
```

## 16.4 Pulse

Pour `50ms`, `100ms`, `150ms` :

- [ ] `Z21/MANUAL` A=1.
- [ ] `Z21/MANUAL` A=0.
- [ ] `Z21/MANUAL` Mouvement complet.
- [ ] `Z21/MANUAL` Pas d'alimentation persistante.
- [ ] `Z21/MANUAL` Annulation pendant pulse → A=0 malgré tout.
- [ ] `Z21/MANUAL` Pas d'échauffement.

## 16.5 Changement externe

- [ ] `Z21/MANUAL` TrainPilot position1.
- [ ] `Z21/MANUAL` Z21 App/WLANmaus position2.
- [ ] `Z21/MANUAL` `reportedPosition` évolue.
- [ ] `Z21/MANUAL` `desiredPosition` reste inchangé.
- [ ] `Z21/MANUAL` Aucune ancienne commande renvoyée.
- [ ] `Z21/MANUAL` quality=`station`.

## 16.6 Offline/reconnect

- [ ] `Z21/MANUAL` Position A.
- [ ] `Z21/MANUAL` Coupure centrale/transport.
- [ ] `Z21/MANUAL` Attente offline.
- [ ] `Z21/MANUAL` Demande B refusée.
- [ ] `Z21/MANUAL` Retour centrale.
- [ ] `Z21/MANUAL` 20 s sans commande → aucun mouvement.
- [ ] `Z21/MANUAL` Nouvelle B → exactement un mouvement.

## 16.7 Triple

Avec triple réel ou deux charges :

- [ ] `Z21/MANUAL` left.
- [ ] `Z21/MANUAL` straight.
- [ ] `Z21/MANUAL` right.
- [ ] `Z21/MANUAL` left→right chemin sûr.
- [ ] `Z21/MANUAL` right→left chemin sûr.
- [ ] `Z21/MANUAL` Combinaison interdite jamais maintenue/commandée.
- [ ] `Z21/MANUAL` État interdit imposé extérieurement → invalid/unknown.

Avec LEDs/relais seulement : la séquence électrique peut être `PASS`, mais le triple mécanique reste `NOT_TESTED`.

## 16.8 TJD

- [ ] `Z21/MANUAL` Table construite depuis le câblage réel.
- [ ] `Z21/MANUAL` 4 positions.
- [ ] `Z21/MANUAL` 12 transitions ordonnées.
- [ ] `Z21/MANUAL` Aucun mapping générique d'une autre marque utilisé sans validation.

Sans TJD réelle : `NOT_TESTED`.

## 16.9 Échec partiel

- [ ] `Z21/MANUAL` Débrancher un endpoint sur charge sûre.
- [ ] `Z21/MANUAL` Erreur/timeout visible.
- [ ] `Z21/MANUAL` Aucun faux `physical`.
- [ ] `Z21/MANUAL` État intermédiaire visible.
- [ ] `Z21/MANUAL` Pas de rollback aveugle.

## 16.10 Endurance

- [ ] `Z21/MANUAL` 500 commandes simples.
- [ ] `Z21/MANUAL` ≥100 transitions triple si matériel sûr.
- [ ] `Z21/MANUAL` TJD 100 transitions uniquement si matériel adapté.
- [ ] `Z21/MANUAL` Aucune fuite mémoire/goroutine notable.
- [ ] `Z21/MANUAL` Aucune commande perdue/doublée.


---

# 17. Pilote DCC-EX — validation logicielle

## 17.1 Connexion et santé

- [ ] `AUTO` Connexion TCP initiale.
- [ ] `AUTO` Perte du socket détectée.
- [ ] `AUTO` `online → degraded`.
- [ ] `AUTO` `degraded → offline`.
- [ ] `AUTO` Reconnexion avant offline.
- [ ] `AUTO` Reconnexion après offline.
- [ ] `AUTO` Plusieurs cycles.
- [ ] `AUTO` `Close()` pendant reconnexion.
- [ ] `AUTO` Aucune race/goroutine leak.

## 17.2 Commandes

- [ ] `AUTO` Power.
- [ ] `AUTO` Emergency stop.
- [ ] `AUTO` Throttle.
- [ ] `AUTO` Fonctions.
- [ ] `AUTO` Accessoires.

## 17.3 Accessoires

- [ ] `AUTO` Adresse linéaire.
- [ ] `AUTO` `position1 → <a LINEAR 0>`.
- [ ] `AUTO` `position2 → <a LINEAR 1>`.
- [ ] `AUTO` 100 commandes concurrentes, trames intactes.
- [ ] `AUTO` Succès write → qualité `assumed`.
- [ ] `AUTO` Échec write → aucun faux report.
- [ ] `AUTO` Pas de mise en queue/rejeu après panne.

---

# 18. DCC-EX réelle — campagne matérielle

À réaliser lorsqu'une centrale DCC-EX réelle est disponible.

## 18.1 Centrale et locomotive

- [ ] `DCCEX/MANUAL` Connexion initiale.
- [ ] `DCCEX/MANUAL` État online.
- [ ] `DCCEX/MANUAL` Power status cohérent.
- [ ] `DCCEX/MANUAL` Acquire locomotive.
- [ ] `DCCEX/MANUAL` Faible vitesse avant.
- [ ] `DCCEX/MANUAL` Zéro.
- [ ] `DCCEX/MANUAL` Reverse.
- [ ] `DCCEX/MANUAL` F0.
- [ ] `DCCEX/MANUAL` Emergency stop.

## 18.2 Coupure TCP

- [ ] `DCCEX/MANUAL` Déconnecter le transport.
- [ ] `DCCEX/MANUAL` Passage degraded.
- [ ] `DCCEX/MANUAL` Passage offline si délai dépassé.
- [ ] `DCCEX/MANUAL` Throttle refusé.
- [ ] `DCCEX/MANUAL` Accessoire refusé.
- [ ] `DCCEX/MANUAL` Reconnexion automatique.
- [ ] `DCCEX/MANUAL` Feedback reprend.
- [ ] `DCCEX/MANUAL` Aucune ancienne vitesse/fonction/accessoire rejouée.

## 18.3 Aiguillages DCC-EX

Tester les adresses `1`, `4`, `5`, `8`, `9` et au moins une adresse réellement utilisée.

- [ ] `DCCEX/MANUAL` `<a LINEAR 0>` actionne position1.
- [ ] `DCCEX/MANUAL` `<a LINEAR 1>` actionne position2.
- [ ] `DCCEX/MANUAL` Mapping d'adresse documenté.
- [ ] `DCCEX/MANUAL` État TrainPilot = `assumed`, pas `physical`.
- [ ] `DCCEX/MANUAL` Changements rapides sans corruption.
- [ ] `DCCEX/MANUAL` Tester si un changement externe est observable.
- [ ] `DCCEX/MANUAL` Si aucun retour fiable, documenter explicitement la limite.

---

# 19. Itinéraires existants — validation du MVP actuel

La sécurisation complète des itinéraires reste un futur chantier. Les capacités présentes doivent néanmoins rester couvertes.

- [ ] `AUTO/SIM` Itinéraire libre réservable.
- [ ] `AUTO/SIM` Canton occupé → réservation refusée.
- [ ] `AUTO/SIM` Conflit explicite → réservation refusée.
- [ ] `AUTO/SIM` Activation avec station offline → échec.
- [ ] `AUTO/SIM` Échec d'un aiguillage → pas de faux succès global.

Ne pas interpréter ces tests comme validation d'un interlocking complet. Restent à développer/tester ultérieurement :

```text
réservation atomique
confirmation physique obligatoire selon politique
rollback sûr
libération progressive
conduite assistée
règles de repli
```

---

# 20. SQLite, redémarrage et sauvegarde

## 20.1 Persistance

- [ ] `AUTO/SIM` Locomotives présentes après redémarrage.
- [ ] `AUTO/SIM` Utilisateurs présents.
- [ ] `AUTO/SIM` Turnouts/configuration présents.
- [ ] `AUTO/SIM` Mappings feedback présents.
- [ ] `AUTO/SIM` Itinéraires présents.

## 20.2 États runtime

- [ ] `AUTO/SIM` `pending` turnout non restauré abusivement.
- [ ] `AUTO/SIM` Qualité/report runtime non restaurés depuis archive.
- [ ] `AUTO/SIM` Aucune ancienne commande rejouée au démarrage.

## 20.3 Sauvegarde / restauration réelle

Cette procédure reste à formaliser dans le backlog.

- [ ] `MANUAL` Arrêt propre de `dccd`.
- [ ] `MANUAL` Sauvegarde SQLite.
- [ ] `MANUAL` Restauration sur instance de test.
- [ ] `MANUAL` Conformité passive après restauration.
- [ ] `MANUAL` Vérification utilisateurs/parc/layout.
- [ ] `MANUAL` Méthode compatible WAL/SHM documentée.

---

# 21. Robustesse et endurance serveur

## 21.1 Longue durée Simulator

Faire tourner plusieurs heures avec WebSocket, heartbeats, throttle, feedbacks, aiguillages et snapshots.

- [ ] `SIM` Aucun crash.
- [ ] `SIM` Mémoire stable.
- [ ] `SIM` Goroutines stables.
- [ ] `SIM` Aucun lease bloqué.
- [ ] `SIM` Séquences WebSocket monotones.

## 21.2 Charge concurrente

- [ ] `AUTO/SIM` Plusieurs sessions.
- [ ] `AUTO/SIM` Plusieurs locomotives.
- [ ] `AUTO/SIM` Plusieurs aiguillages.
- [ ] `AUTO/SIM` Feedback simultané.
- [ ] `AUTO/SIM` Commande sécurité pendant commandes ordinaires.
- [ ] `AUTO/SIM` Takeover pendant charge.

## 21.3 Redémarrages

- [ ] `SIM` Redémarrage serveur.
- [ ] `Z21/MANUAL` Redémarrage serveur avec z21 déjà online.
- [ ] `DCCEX/MANUAL` Redémarrage avec DCC-EX déjà online.
- [ ] `10819/MANUAL` Redémarrage avec canton occupé.
- [ ] `Z21/MANUAL` Redémarrage avec aiguillage dans une position inconnue du runtime.
- [ ] `MANUAL` Aucun mouvement automatique au boot uniquement pour resynchroniser.

---

# 22. Configuration et sécurité d'exploitation

## 22.1 Valeurs invalides

- [ ] `AUTO` Driver inconnu.
- [ ] `AUTO` Adresse/port invalides.
- [ ] `AUTO` `offlineAfter = 0`.
- [ ] `AUTO` `offlineAfter < 0`.
- [ ] `AUTO` `offlineAfter` non parsable.
- [ ] `AUTO` `accessoryPulse <= 0`.
- [ ] `AUTO` `accessoryPulse` non parsable.
- [ ] `AUTO` `turnout.confirmationTimeout <= 0`.

Attendu : refus de démarrage avec erreur explicite.

## 22.2 API de test Simulator

- [ ] `AUTO` Présente uniquement avec `testAPI=true`.
- [ ] `AUTO` Absente avec `testAPI=false`.
- [ ] `AUTO` Absente avec z21 réelle.
- [ ] `AUTO` Absente avec DCC-EX réel.
- [ ] `AUTO` Authentification requise.
- [ ] `AUTO` Non incluse dans OpenAPI production.

## 22.3 Réseau

Si `dccd` est exposé sur le LAN :

- [ ] `MANUAL` TLS natif ou reverse proxy TLS.
- [ ] `MANUAL` Pas d'écoute HTTP non protégée non voulue.
- [ ] `MANUAL` Socket Unix admin correctement protégé.
- [ ] `MANUAL` Aucun token/password dans logs ou rapports.

---

# 23. Priorités de tests à exécuter maintenant

Compte tenu de l'avancement du serveur, l'ordre recommandé est :

## P1 — Gate logiciel et conformité

- [ ] Gate complète Go/CGO/race/vet/GoReleaser.
- [ ] Conformité passive.
- [ ] Conformité active sur Simulator.
- [ ] `--check-turnouts`.
- [ ] Expiration de session opt-in.

## P2 — Aiguillages z21 réels

- [ ] AIG-009 simple.
- [ ] Adressage `1/4/5/8/9`.
- [ ] Pulse `50/100/150ms`.
- [ ] Retour z21.
- [ ] Changement externe.
- [ ] Offline/reconnect/no replay.
- [ ] Triple réel ou deux charges.
- [ ] TJD si matériel disponible.
- [ ] Fiche z21 datée committée.

## P3 — z21 intermittente

- [ ] Perturbations UDP courtes.
- [ ] Retour avant offline.
- [ ] Passage offline.
- [ ] Au moins 5 cycles.
- [ ] Refus + absence de rejeu.

## P4 — R-BUS / Roco 10819

- [ ] Mapping des entrées.
- [ ] Occupation/libération.
- [ ] Occupations simultanées.
- [ ] `A → A+B → B`.
- [ ] Redémarrage avec canton occupé.
- [ ] Validation des 5 zones prévues.

## P5 — DCC-EX réel

Dès qu'une centrale est disponible :

- [ ] Conduite.
- [ ] Reconnexion TCP.
- [ ] Feedback.
- [ ] Aiguillages/adressage.
- [ ] No replay.
- [ ] Fiche matérielle datée.

---

# 24. Matrice de validation actuelle

| Domaine | Automatique | Simulator | z21 réel | DCC-EX réel | 10819 réel |
|---|---:|---:|---:|---:|---:|
| Auth/session | ✅ | ✅ | N/A | N/A | N/A |
| Leases | ✅ | ✅ | partiel | à valider | N/A |
| Throttle | ✅ | ✅ | ✅ observé | à valider | N/A |
| Fonctions | ✅ | ✅ | F0 ✅ | à valider | N/A |
| E-stop | ✅ | ✅ | ✅ observé | à valider | N/A |
| WebSocket | ✅ | ✅ | partiel | partiel | partiel |
| Simulateur | ✅ | ✅ | N/A | N/A | N/A |
| Santé z21 | ✅ fake | ✅ | coupure simple ✅ / intermittence ouverte | N/A | N/A |
| Santé DCC-EX | ✅ fake | N/A | N/A | à faire | N/A |
| Feedback générique | ✅ | ✅ | parser testé | fake testé | à faire |
| Aiguillage simple | ✅ | ✅ | à faire | à faire | N/A |
| Triple | ✅ | ✅ | à faire matériel | à faire si matériel | N/A |
| TJD/TJS | ✅ | ✅ | à faire si matériel | à faire si matériel | N/A |
| R-BUS | ✅ parser | ✅ feedback | à valider | N/A | à valider |
| Import/export | ✅ | ✅ | N/A | N/A | N/A |
| Itinéraire MVP | ✅ | ✅ | partiel | partiel | partiel |
| Topologie complète | pas encore | pas encore | N/A | N/A | N/A |
| Localisation train | pas encore | pas encore | N/A | N/A | futur |
| Signalisation | pas encore | pas encore | futur | futur | futur |

---

# 25. Critères avant de passer au chantier « topologie »

Le lot aiguillages/centrales peut être considéré suffisamment validé lorsque :

- [ ] Gate logicielle entièrement verte.
- [ ] `dcc-api-conformance --check-turnouts` vert.
- [ ] Aiguillage simple z21 réel validé.
- [ ] Adressage z21 validé autour des groupes de quatre.
- [ ] Pulse z21 validé.
- [ ] Retour d'état z21 et sa limite documentés.
- [ ] Offline/reconnect/no-replay validé avec un aiguillage.
- [ ] Triple au minimum validé électriquement avec deux endpoints.
- [ ] Au moins une fiche AIG-009 z21 datée est committée.
- [ ] Aucune régression traction/F0/E-stop.

Une TJD mécanique réelle peut rester `NOT_TESTED` si le matériel n'est pas disponible, puisque son modèle est déjà couvert automatiquement.

---

# 26. Critères avant de développer/valider la localisation

Avant d'utiliser le R-BUS comme source de position logique :

- [ ] Chaque entrée 10819 utilisée est mappée sans ambiguïté.
- [ ] Occupation/libération fiables.
- [ ] Plusieurs cantons simultanés fiables.
- [ ] Séquence `A → A+B → B` fidèle.
- [ ] Redémarrage avec zone occupée compris et validé.
- [ ] Rebonds/répétitions réels compris.
- [ ] WebSocket reflète les occupations correctement.

---

# 27. Fiche minimale de campagne matérielle

```markdown
# Campagne <nom>

Date:
Commit TrainPilot:
Version/release:
OS / machine:

## Matériel
Centrale:
Firmware:
Décodeur:
Module feedback:
Actionneur / locomotive:
Adresse(s):

## Configuration TrainPilot
station.driver:
station.offlineAfter:
station.accessoryPulse:
turnout.confirmationTimeout:

## Tests
| ID | Test | Attendu | Observé | Résultat |
|---|---|---|---|---|
| ... | ... | ... | ... | PASS/FAIL/BLOCKED/NOT_TESTED |

## Logs
- logs serveur:
- capture réseau:
- autres observations:

## Anomalies
...

## Conclusion
...
```

Ne jamais inclure de mot de passe, access token, refresh token ou clé privée.

---

# 28. Sources de référence du dépôt

À garder synchronisés avec cette recette :

- `README.md`
- `docs/TESTING.md`
- `docs/VALIDATION.md`
- `docs/TURNOUTS.md`
- `docs/backlog.md`
- `docs/hardware-tests/turnouts/README.md`
- `api/openapi.yaml`
- `api/asyncapi.yaml`

Liens :

- https://github.com/agm650/TrainPilot-server
- https://github.com/agm650/TrainPilot-server/blob/main/docs/TESTING.md
- https://github.com/agm650/TrainPilot-server/blob/main/docs/VALIDATION.md
- https://github.com/agm650/TrainPilot-server/blob/main/docs/TURNOUTS.md
- https://github.com/agm650/TrainPilot-server/tree/main/docs/hardware-tests/turnouts

---

# 29. Note sur le backlog

Lors de cette revue, `docs/backlog.md` daté du 2 septembre 2026 liste encore plusieurs éléments AIG-001 à AIG-010 comme ouverts, alors que le README, `docs/TESTING.md`, `docs/TURNOUTS.md` et les tests décrivent déjà une grande partie de ce support comme implémentée et couverte automatiquement.

Pour cette recette, l'ordre de confiance retenu est :

1. code et contrats présents dans `main`;
2. tests automatisés présents;
3. documentation fonctionnelle actuelle;
4. backlog.

Il serait utile de mettre à jour le backlog pour distinguer explicitement :

```text
implémenté + testé automatiquement
```

de :

```text
validation matérielle restante
```.
