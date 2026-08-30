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
- Télémétrie électrique injectable du simulateur couvrant courants, tensions, température, mode programmation, perte d'alimentation, surchauffe et courts-circuits.

### Changed

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
