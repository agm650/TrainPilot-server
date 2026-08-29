# Modèle de sécurité

- Les mots de passe ne sont jamais stockés en clair.
- Les access tokens et refresh tokens sont aléatoires et seuls leurs condensats SHA-256 sont persistés.
- Les refresh tokens sont tournés à chaque renouvellement.
- La désactivation d’un utilisateur révoque toutes ses sessions.
- Un WebSocket ferme à l'expiration de son jeton d'ouverture ou après la révocation de sa session.
- L’administration des utilisateurs n’est pas routée sur le serveur HTTP public.
- Le socket Unix utilise les permissions du système d’exploitation comme frontière d’administration.
- Les commandes de conduite vérifient le propriétaire du lease et la session.
- Les corps HTTP sont limités à 1 Mio et les champs JSON inconnus sont refusés sur les commandes sensibles.
- Les messages WebSocket sont limités à 1 Mio.
- Les erreurs publiques utilisent des catégories et codes stables ; les détails d'erreur interne sont masqués.

## Durcissements avant production

- utiliser TLS sur le LAN ;
- limiter les tentatives de connexion ;
- ajouter un journal d’audit dans tous les services ;
- ajouter une politique de complexité et de rotation adaptée au contexte ;
- migrer vers Argon2id si une dépendance cryptographique auditée est acceptée ;
- valider les certificats et permissions de fichiers au démarrage ;
- ajouter des sauvegardes et tests de restauration SQLite ;
- exécuter le service sous un utilisateur non privilégié.
