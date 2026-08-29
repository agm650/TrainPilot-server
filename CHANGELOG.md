# Changelog

All notable changes to this project will be documented in this file.

## Unreleased

### Fixed

- Les snapshots WebSocket utilisent la séquence courante du bus au lieu d’une valeur constante.
- Les heartbeats WebSocket client ne consomment plus de séquence d’événement serveur.
- Les arrêts d'urgence, coupures de puissance et vitesses nulles préemptent désormais les commandes de traction ou de fonctions en attente.
- Les commandes de conduite sont explicitement inhibées tant que l'arrêt d'urgence reste actif ou que la puissance est coupée ou inconnue.

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

### Changed

- SQLite utilise le pilote pur Go `modernc.org/sqlite`.
- Une commande de traction ou de fonction valide renouvelle désormais le lease de conduite.
- Le snapshot WebSocket inclut les capacités et l’état courant de la centrale.
- Le contrat OpenAPI passe à la version 1.3.0 et documente les codes d'erreur de sécurité stables.

## v0.0.1

### Changed

- Initial version
- Z21 interface mostly working. RBUS still not integrated
- Train driving is working through command line
