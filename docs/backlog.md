# Backlog restant — TrainPilot-server

Dernière mise à jour : 31 août 2026.

Ce document ne contient que les travaux restant à réaliser. Les fonctionnalités
terminées et leur historique restent consignés dans `DCC_BACKLOG.md`. Avant de
commencer une tâche, vérifier le code, les tests, les contrats et la branche
courante : une tâche peut demander une consolidation plutôt qu'une nouvelle
implémentation.

Priorités :

- **P0** : sécurité, cohérence ou fiabilité du socle ;
- **P1** : nécessaire pour compléter le MVP serveur ;
- **P2** : extension structurante après stabilisation ;
- **P3** : amélioration à plus long terme ;
- **Différé** : volontairement conservé hors du développement actuel.

## Prochain lot recommandé sans matériel

Ordre suggéré parmi les tâches P0 détaillées ci-dessous :

1. vérifier que le scénario de conflit de réservation reste exécuté avec deux sessions distinctes.

## P0 — Fiabilité du socle

### Centrales

- [ ] Vérifier les reconnexions répétées et les réponses z21 intermittentes. **En attente d'une z21 disponible ; conserver la tâche ouverte.**

### Conformité

- [ ] Vérifier que le scénario de conflit de réservation reste exécuté avec deux sessions distinctes.

## P1 — Compléter le MVP serveur

### Matériel roulant

- [ ] Finaliser le modèle de décodeur et la correspondance entre fonctions logiques et numéros DCC.
- [ ] Définir les champs nécessaires pour les wagons et voitures.
- [ ] Ne proposer des fonctions sur un matériel remorqué que lorsqu'un décodeur le justifie.
- [ ] Étudier un format d'échange limité avec CVProgrammer sans coupler les deux applications.

### Simulateur

Le lot SIM-001 à SIM-008 est terminé : socle déterministe, télémétrie, faults,
rétrosignalisation, moteur JSON, API de contrôle externe et douze scénarios de
référence obligatoires dans `go test ./...`. La suite couvre notamment la panne
sans rejeu, les événements WebSocket, les feedbacks multiples/rebond/perte et
les bases de confirmation d'accessoire. Le guide client exhaustif est livré
avec chaque archive du serveur. Aucun ticket du lot simulateur ne reste ouvert
dans ce backlog.

### Rétrosignalisation et cantons

- [ ] Tester un redémarrage du serveur lorsque des cantons sont déjà occupés sur le réseau réel.
- [ ] Valider sur le petit réseau les trois sections rouges extérieures et les deux sections rouges intérieures.

### Accessoires

- [x] Refondre le modèle des aiguillages simples et composés avec endpoints, positions explicites, migration SQLite et archives compatibles (`AIG-001`).
- [x] Typer l'interface station pour les sorties d'accessoires binaires et leurs retours qualifiés (`AIG-002`).
- [x] Adapter le simulateur aux endpoints binaires, retours qualifiés, appareils composés, faults ciblés et scénarios de référence (`AIG-003`).
- [x] Implémenter les commandes et retours d'état binaires z21, avec impulsion configurable, corrélation et broadcasts (`AIG-004`).
- [ ] Aligner DCC-EX sur l'adresse canonique et la sémantique binaire.
- [ ] Sérialiser les transitions multi-endpoints et gérer les erreurs partielles.
- [ ] Valider sur z21 réelle l'adressage des accessoires, la durée d'impulsion et la différence entre état de fonction rapporté et position physique.
- [ ] Gérer les délais, échecs et incohérences entre position demandée et position confirmée.
- [ ] Étendre REST, WebSocket, CLI et conformité au modèle composé.
- [ ] Préparer les sorties nécessaires au pilotage futur des signaux.

## P2 — Itinéraires et conduite sécurisée

- [ ] Réserver atomiquement les cantons nécessaires à un itinéraire.
- [ ] Confirmer physiquement les aiguillages et prévoir un rollback après succès partiel.
- [ ] Définir et implémenter la libération progressive ou totale d'un itinéraire.
- [ ] Ajouter un mode de conduite assistée avant l'automatisation complète.
- [ ] Définir les règles de repli en cas de perte de détection ou de centrale.

## P2 — Signalisation SNCF

- [ ] Séparer le calcul d'aspect logique de l'activation électrique des feux.
- [ ] Définir les aspects minimaux : carré, indication rouge adaptée, avertissement orange et voie libre verte.
- [ ] Associer signaux, cantons, aiguillages et itinéraires.
- [ ] Définir l'état sûr des signaux en cas de perte du serveur ou de la centrale.
- [ ] Valider un montage minimal avec z21, Roco 10819 et Lectix LEC000043.
- [ ] Documenter le câblage et les limites électriques avant les essais matériels.

## P2 — Parité DCC-EX

- [ ] Définir et maintenir une matrice de capacités z21/DCC-EX.
- [ ] Exécuter pour DCC-EX les mêmes tests contractuels que pour les capacités communes de z21.
- [ ] Documenter les différences de pilotes sans les propager dans l'API publique.

## P2 — Import, export et sauvegarde

- [ ] Ajouter une prévisualisation des imports et définir la stratégie de résolution des conflits.
- [ ] Ajouter des tests de compatibilité ascendante des formats d'archives.

## P2 — Exploitation et livraison

- [ ] Définir le répertoire et la politique de conservation des journaux.
- [ ] Documenter et tester la sauvegarde et la restauration de SQLite.
- [ ] Injecter les informations de version, commit et date dans `dccd`, `dccctl` et `dcc-api-conformance`.
- [ ] Définir une politique de rotation des journaux.

## Différé — Clients natifs

Le développement du client macOS n'est pas engagé pour le moment. Ces tâches
restent volontairement dans le backlog sans bloquer les travaux serveur.

### Client macOS

- [ ] Implémenter le login, le refresh, la révocation et la reconnexion WebSocket.
- [ ] Implémenter la bibliothèque de locomotives et décodeurs.
- [ ] Gérer acquisition, heartbeat, libération, expiration et conflits de lease.
- [ ] Fournir un throttle sûr fondé sur l'état confirmé par le serveur.
- [ ] Afficher puissance, arrêt d'urgence et connectivité `online/degraded/offline`.
- [ ] Désactiver clairement les commandes impossibles lorsque la centrale est hors ligne.
- [ ] Traiter les séquences WebSocket et demander un snapshot en cas de trou.
- [ ] Afficher les cantons et leur occupation.
- [ ] Préparer l'éditeur de réseau.
- [ ] Tester le décodage de toutes les réponses et de tous les événements utilisés.

### Autres clients

- [ ] **P3** Développer un client Linux natif après stabilisation du client macOS.

## P3 — Extensions

- [ ] Automatiser complètement la circulation après stabilisation des itinéraires et de la sécurité.
- [ ] Ajouter historique et métriques d'exploitation.
- [ ] Supporter de nouvelles centrales derrière l'abstraction existante.
- [ ] Fournir des outils graphiques de diagnostic des événements et séquences.
- [ ] Étudier la conversion ou l'import de plans AnyRail/Raily dans un projet séparé si nécessaire.

## Campagnes matérielles à conserver

Ces campagnes ne doivent jamais être exécutées automatiquement. Elles exigent
une activation explicite, un opérateur présent et un moyen physique de couper
l'alimentation.

- [ ] Couper puis rétablir l'alimentation de la z21 pendant qu'une locomotive roule.
- [ ] Déconnecter le réseau UDP avec retour avant puis après le délai `offline`.
- [ ] Appuyer sur STOP depuis la MultiMaus et vérifier la propagation API/WebSocket.
- [ ] Simuler un court-circuit selon une procédure contrôlée.
- [ ] Tester l'occupation successive et simultanée des cantons du réseau d'essai.
- [ ] Changer un aiguillage avec et sans confirmation de position.
- [ ] Perdre le WebSocket pendant des changements d'état puis vérifier la resynchronisation.
- [ ] Redémarrer le serveur sans redémarrer la centrale.
- [ ] Redémarrer la centrale sans redémarrer le serveur.

Chaque campagne doit consigner la version du serveur, la configuration, le
matériel utilisé, le résultat attendu, le résultat observé et les journaux
pertinents.
