# Lot A — Rétrosignalisation R-BUS

## Caractérisation du 17 novembre 2026

Résultats rapportés par l'utilisateur sur son banc z21/10819.
Ces essais n'ont pas été exécutés par l'agent. Les captures ne remplacent pas
une trace protocolaire ; les versions de firmware n'ont pas été communiquées.

- Coupure Ethernet, centrale maintenue sous tension, puis reconnexion :
  la centrale revient `ONLINE`, mais les cantons restent `unknown`.
  Captures du 17 novembre à 19:05:43, 19:05:59 et 19:06:19.
- Coupure de l'alimentation de voie puis rétablissement : le plan conserve
  une occupation obsolète. Un canton affiché vert devrait être occupé selon
  la présence réelle du train rapportée par l'utilisateur.
  Captures du 17 novembre à 19:11:02, 19:11:34 et 19:11:53.

## RBUS-001 — Correction et recette

### Retour de banc du 19 novembre 2026

L'utilisateur rapporte un nouvel essai : locomotive détectée sur le canton 2,
coupure Ethernet avec la centrale allumée, attente du statut dégradé/perdu, puis
reconnexion. Le client macOS récupère finalement les occupations, avec un délai.
Les logs montrent cependant des répétitions d'occupation à environ deux secondes
d'intervalle et des rejets de générations devenues obsolètes. Ce retour confirme
une récupération observée, avec une anomalie encore présente ; ce n'est pas une
recette complète sans défaut. Les numéros exacts des firmwares restent à relever.

Le diagnostic logiciel reproduit une invalidation à tort quand l'état système
arrive sans la réponse secondaire X. Le correctif accepte cet état système frais
immédiatement, conserve les barrières de génération et distingue les trames
rejetées dans `station.feedback.discarded`. Les requêtes concurrentes et les
changements de génération puissance sont testés. Ce correctif supplémentaire
reste **à retester physiquement sur le nouveau binaire**.

Pendant ce réessai, laisser la liaison rétablie pendant au moins dix secondes.
Une occupation inchangée peut être annoncée une fois dans la nouvelle génération,
puis les logs de transition doivent rester silencieux. Les rejets transitoires
doivent indiquer les générations reçues/courantes ; ils ne doivent pas former
une nouvelle boucle de resynchronisation. Refaire aussi la recette OFF/ON DCC
ci-dessous. Aucun résultat physique de ce nouveau correctif n'est présumé.

### Correction et protocole de recette initial

Correction logicielle : invalidation dès `degraded`, génération de feedback,
renouvellement du socket UDP et des abonnements, nouvelle lecture des groupes
0 et 1, puis relance des seuls groupes manquants. Les mappings wildcard sont
invalidés avec ceux de la centrale. Les autres fournisseurs restent indépendants.

Tests automatisés : perte de réponse aux requêtes de statut, plusieurs cycles
de reprise, changement de port UDP, retour d'une ancienne génération, réponse
partielle et récupération REST/WebSocket sans notification spontanée.

Recette physique après correction : **à effectuer sur le nouveau binaire**.

1. Noter les versions du serveur, de la z21 et du 10819, ainsi que les mappings.
2. Placer un train sur un détecteur et attendre une occupation correcte.
3. Débrancher Ethernet sans couper la centrale. Vérifier les occupations
   dépendantes à `unknown` dès la détection de `degraded`.
4. Déplacer manuellement le train vers une autre zone pendant la coupure.
5. Rebrancher Ethernet sans déplacer à nouveau le train. Vérifier les nouveaux
   états d'occupation dans REST et dans le client graphique.
6. Répéter plusieurs cycles et conserver les logs de feedback et de statut.

Une réponse partielle ne prouve pas la synchronisation des zones absentes.
L'état d'un fournisseur indépendant peut toujours maintenir un canton occupé.

## RBUS-002 — Traitement groupé

Correction logicielle : chaque trame de 80 entrées est traitée en un lot. Les
entrées non configurées sont ignorées ; les mappings explicites précèdent les
wildcards. Tous les blocs sont recalculés avant publication et les logs de
transition partagent l'ensemble final des adresses occupées.

Tests automatisés : transfert entre deux détecteurs du même canton sans
libération intermédiaire, priorité des mappings, 77 entrées non configurées,
trames identiques, lot vide, séquences monotones, états mis à jour avant le
premier événement, backpressure et contrôle REST/WebSocket du transfert réel.
Les tests des pilotes individuels DCC-EX et simulateur restent applicables.

La simultanéité entre deux groupes distincts n'est pas garantie. Les événements
publics restent individuels, même si l'état serveur est recalculé en un lot.

## RBUS-003 — Alimentation de voie

Correction logicielle : déclaration persistante `trackPowerDependent`, défaut
conservateur pour les détecteurs de courant et les mappings de centrale/wildcard,
invalidation sur OFF, puissance inconnue ou défaut électrique, puis acquisition
fraîche au retour ON. Une génération d'alimentation écarte les retours en attente.
Les sources indépendantes conservent leurs observations. L'arrêt d'urgence seul
n'est pas assimilé à une coupure électrique.

Tests automatisés : états de puissance ON/OFF/inconnue, courts-circuits, perte
d'alimentation, observations pendant OFF, maintien des capteurs indépendants,
mappings wildcard, retours d'une ancienne génération, réponse partielle et
nouvelle position du train après rétablissement, vérifiée dans REST/WebSocket.
Une commande ON acceptée mais non confirmée par l'état observé de la centrale
ne rétablit pas la validité des détecteurs.
La migration SQLite et les archives de circuit version 8 sont également testées.
Le pilote DCC-EX interroge les états des capteurs avec `<Q>` après renouvellement
de son flux TCP, sans rejouer de commande de locomotive ou d'accessoire.

Recette physique après correction : **à effectuer sur le nouveau binaire**.

1. Confirmer les versions du serveur et des firmwares, le câblage d'alimentation
   du 10819 et la déclaration des fournisseurs dépendants/indépendants.
2. Train immobile détecté : passer VOIE OFF, vérifier l'état `unknown` de ses
   détecteurs dépendants, puis VOIE ON sans déplacer le train.
3. Vérifier le retour de l'occupation réelle sans mouvement supplémentaire.
4. Refaire l'essai en déplaçant manuellement le train hors tension vers une
   autre zone ; vérifier les deux zones après remise sous tension.
5. Vérifier qu'un fournisseur indépendant conserve sa validité et ses états.
6. Conserver les traces de puissance, de feedback et les réponses REST/WS.

Ces corrections ne constituent pas encore une recette physique réussie. Les
résultats matériels rapportés plus haut caractérisent la version antérieure.

## Validation logicielle

Chaque ticket a été vérifié avant son commit dédié.

| Ticket | Tests standard | Tests avec `-race` | Autres contrôles |
| --- | --- | --- | --- |
| RBUS-001 | 1 079 réussis | 1 079 réussis | `go vet ./...`, `git diff --check` |
| RBUS-002 | 1 082 réussis | 1 082 réussis | `go vet ./...`, `git diff --check` |
| RBUS-003 | 1 096 réussis | 1 096 réussis | `go vet ./...`, `git diff --check` |

Les contrats YAML et leurs références locales ont également été validés.
