# Politique de compatibilité des API

TrainPilot versionne séparément le serveur, l'API HTTP et le protocole
d'événements. Les versions courantes sont exposées par
`GET /api/v1/system/info` :

- `serverVersion` identifie le binaire ;
- `apiVersion` et `minimumClientApiVersion` décrivent le contrat HTTP ;
- `eventApiVersion` et `minimumClientEventApiVersion` décrivent le contrat
  WebSocket.

Les versions HTTP et événements suivent `MAJEUR.MINEUR.CORRECTIF` :

- un correctif précise ou corrige le comportement sans modifier un message
  valide ;
- une version mineure peut ajouter des endpoints, champs optionnels, types
  d'événements ou codes d'erreur ;
- une version majeure est nécessaire pour retirer ou renommer un élément,
  changer son type ou modifier une sémantique existante de manière
  incompatible.

Le préfixe `/api/v1` représente la version majeure HTTP. Une rupture HTTP
nécessitera donc un nouveau préfixe, une période de coexistence documentée ou
un mécanisme de migration explicite.

## Négociation côté client

Avant d'ouvrir une session, un client lit `/api/v1/system/info`. Il doit
refuser proprement la connexion si la version majeure est inconnue ou si la
version de contrat qu'il implémente est antérieure au minimum annoncé par le
serveur. Il doit appliquer la même règle séparément au protocole d'événements.

Un client compatible avec une version mineure doit :

- ignorer les champs JSON inconnus ;
- accepter de nouveaux types ou codes en les présentant comme inconnus plutôt
  que comme un succès ;
- conserver un traitement de repli fondé sur `category` et le statut HTTP ;
- ne jamais supposer qu'une capacité matérielle est présente sans lire
  `station` dans les informations système ou le snapshot.

## Erreurs

Une réponse d'erreur publique utilise le schéma `Problem` et contient au
minimum `status` et `category`, avec un `code` stable lorsqu'une condition doit
être distinguée par le client. Les catégories sont
`authentication`, `authorization`, `validation`, `not_found`, `conflict`,
`safety`, `station_unavailable` et `internal`.

Un code existant ne change pas de sens dans une même version majeure. De
nouveaux codes peuvent être ajoutés dans une version mineure. Les détails
d'une erreur interne ne sont jamais contractuels et ne doivent pas être
affichés comme une instruction opérateur.

## Événements et resynchronisation

La séquence WebSocket est monotone pendant la vie du processus serveur, mais
n'est pas persistée entre deux démarrages. Après connexion, reconnexion ou trou
de séquence, le client prend `system.snapshot` comme nouvelle base et ignore
les événements dont la séquence est inférieure ou égale à celle du snapshot.
Il envoie `client.snapshot_request` lorsqu'il détecte une perte. Le serveur ne
garantit actuellement aucun replay. Il filtre aussi les séquences anciennes ou
dupliquées. Si la file de 64 événements d'une connexion déborde ou si une
écriture dépasse 5 secondes, il ferme le WebSocket ; le client doit se
reconnecter et repartir du snapshot complet.

Les ajouts de champs optionnels et de nouveaux événements sont compatibles en
version mineure. Toute suppression, modification de type, réutilisation d'un
nom avec une autre sémantique ou changement des règles de séquence impose une
version majeure du protocole d'événements et une stratégie de migration.

## Dépréciation et migration

Une dépréciation doit être annoncée dans OpenAPI ou AsyncAPI et dans le
changelog. Sauf correction de sécurité imposant une action immédiate, un
élément déprécié reste disponible pendant au moins une version mineure. Sa
suppression s'accompagne d'une version majeure, de la correspondance ancien →
nouveau et d'une procédure de migration testable.

Les contrats `api/openapi.yaml` et `api/asyncapi.yaml`, les tests d'intégration
et l'inventaire de `dcc-api-conformance --list-endpoints` constituent ensemble
la référence vérifiable de compatibilité.
