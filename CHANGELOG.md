# Changelog

All notable changes to this project will be documented in this file.

## Unreleased

### Fixed

- Les snapshots WebSocket utilisent la séquence courante du bus au lieu d’une valeur constante.
- Les heartbeats WebSocket client ne consomment plus de séquence d’événement serveur.
- Les arrêts d'urgence, coupures de puissance et vitesses nulles préemptent désormais les commandes de traction ou de fonctions en attente.
- Les commandes de conduite sont explicitement inhibées tant que l'arrêt d'urgence reste actif ou que la puissance est coupée ou inconnue.
- Un heartbeat de lease envoyé à son instant d'expiration ne peut plus réactiver une réservation expirée.
- Les jetons révoqués sont distingués des jetons réellement expirés sans exposer de détail interne.
- Les événements WebSocket anciens ou dupliqués sont filtrés sans perdre ceux publiés pendant un snapshot.
- Les clients WebSocket trop lents sont déconnectés sur débordement de leur file ou expiration d'écriture.

### Added

- Contrat station typé pour les accessoires DCC binaires, validation de la plage linéaire portable et provider générique de retours qualifiés.
- Commandes d'accessoires z21 `LAN_X_SET_TURNOUT`, impulsion configurable et désactivation sûre, lecture corrélée `LAN_X_GET_TURNOUT_INFO` et broadcasts d'état sans position inventée.
- Accessoires DCC-EX alignés sur `<a linear 0|1>`, avec validation de la plage portable, retours `assumed`, tests TCP concurrents et absence de rejeu après reconnexion.
- Modèle d'aiguillage simple ou composé avec endpoints binaires, positions logiques explicites, inversion, résolution physique, migration SQLite et archives version 2 compatibles avec la version 1.
- Authentification persistée avec access tokens et refresh tokens révocables.
- Administration des utilisateurs par socket Unix local uniquement.
- CRUD des locomotives avec validation des adresses DCC et contrôle par rôle.
- Réservations exclusives avec heartbeat, expiration et arrêt contrôlé avant libération.
- Gestion de la puissance, de l’arrêt d’urgence, de la vitesse et des fonctions.
- Cantons, aiguillages, itinéraires et mapping des retours de rétrosignalisation.
- Pilotes simulateur, Z21 UDP et DCC-EX TCP derrière une abstraction commune.
- État Z21 `online`, `degraded` et `offline`, avec refus des commandes actives hors ligne.
- Import et export versionnés du parc et du circuit dans des archives ZIP.
- Outils `dccctl` et `dcc-api-conformance`.
- Contrats OpenAPI et AsyncAPI ainsi que scénarios contractuels réutilisables par les clients.
- Snapshot WebSocket à la connexion et resynchronisation via `client.snapshot_request`.
- Construction macOS/Linux sans CGO et archives GoReleaser v2.
- Exemple de service systemd et configuration Linux.
- Snapshot WebSocket complet et filtré par session, tests de trou de séquence, resynchronisation, reconnexion et expiration du jeton.
- Catégories et codes d'erreur stables pour l'authentification, l'autorisation, la validation, la sécurité et l'indisponibilité de la centrale.
- Inventaire vérifié des endpoints publics et scénarios de conformité passifs, actifs et de mutation de configuration.
- Politique de compatibilité et de migration séparée pour HTTP et WebSocket.
- État public `locomotiveControlStates` dans les snapshots pour distinguer les locomotives libres, contrôlées par la session, par une autre session du même utilisateur ou par un autre utilisateur.
- Endpoint explicite de takeover entre sessions d'un même utilisateur, avec arrêt à zéro, transfert atomique du lease et événement `locomotive.control.transferred`.
- Socle déterministe du simulateur avec état explicite, horloge injectable, snapshot profondément copié, introspection des accessoires et reset sans déconnexion.
- Simulation déterministe des accessoires avec états demandé et confirmé, confirmations immédiates ou différées, absence de confirmation et retours incohérents.
- Simulation binaire `position1`/`position2` des endpoints, événements de position qualifiés, rapports externes, faults ciblés par adresse et scénarios simple, triple, TJD, panne partielle et confirmation obsolète.
- Télémétrie électrique injectable du simulateur couvrant courants, tensions, température, mode programmation, perte d'alimentation, surchauffe et courts-circuits.
- Injection déterministe de connectivité `online/degraded/offline`, délais context-aware et erreurs limitées par type d'opération dans le simulateur.
- Rétrosignalisation simulée avec état physique observable, répétitions, rebonds déterministes, pertes volontaires, saturation explicite et intégration multi-cantons.
- Moteur de scénarios JSON v1 strict et déterministe, avec avance manuelle sans sommeil réel, exécution temps réel annulable, état de contrôle observable et scénarios de référence versionnés.
- API de test authentifiée du simulateur pour snapshots, reset, connectivité, télémétrie, feedback, accessoires, faults et scénarios, entièrement absente avec les pilotes matériels ou lorsque `testAPI=false`.
- Douze scénarios de référence du simulateur exécutés en temps logique par la suite d'intégration HTTP/WebSocket et la CI, avec non-rejeu hors ligne, télémétrie, feedbacks et confirmations d'accessoire.
- Publication générique des changements d'état injectés par le simulateur via `station.StatusEventProvider`.
- Guide exhaustif du banc simulateur pour le développement de clients, avec exemples HTTP/WebSocket, scénarios et diagrammes PlantUML, inclus dans les archives de livraison.
- Contrôleur métier d'aiguillages avec transitions multi-endpoints sûres, confirmation qualifiée, timeout configurable, sérialisation par appareil, gestion des erreurs partielles et changements externes.

### Changed

- Les drivers reçoivent désormais `position1` ou `position2` via `SetBasicAccessory`, sans chaînes géométriques `straight/diverging`.
- Le contrat OpenAPI passe à `1.6.0` et AsyncAPI à `1.8.0`. Les aiguillages exposent leur statut de rapport, leur qualité et leur statut de commande ; les événements `turnout.commanded` et `turnout.command.failed` complètent `turnout.state.changed`.
- SQLite utilise le pilote pur Go `modernc.org/sqlite`.
- Une commande de traction ou de fonction valide renouvelle désormais le lease de conduite.
- Le snapshot WebSocket inclut les capacités et l’état courant de la centrale.
- Le contrat OpenAPI passe à la version 1.4.0 et documente l'endpoint et les codes d'erreur stables du takeover.
- Le contrat AsyncAPI passe à la version 1.6.0 et décrit tous les payloads d'événements, la resynchronisation complète, l'ownership dans `system.snapshot` et `locomotive.control.transferred`.
- Les capacités de centrale exposent désormais `maxFunctionNumber` ; `functions` reste le nombre de fonctions pour compatibilité.
- Une connexion WebSocket expire avec le jeton d'accès utilisé à son ouverture et ferme après révocation de session ; sa fermeture ne libère pas automatiquement les leases.

## v0.0.1

### Changed

- Initial version
- Z21 interface mostly working. RBUS still not integrated
- Train driving is working through command line
