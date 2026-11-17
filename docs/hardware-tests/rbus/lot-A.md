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

## RBUS-003

Travaux à traiter après le commit RBUS-002.
