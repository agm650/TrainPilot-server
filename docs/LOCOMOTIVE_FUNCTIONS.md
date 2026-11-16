# Fonctions des engins moteurs

Les locomotives exposent une liste de définitions `functions` dans REST,
les événements `locomotive.created` et `locomotive.updated`, ainsi que dans
les messages `system.snapshot`.

```json
{
  "functions": [
    { "number": 0, "momentary": false },
    { "number": 2, "momentary": true }
  ]
}
```

Chaque numéro est unique, de 0 à 68. Les capacités de la centrale limitent
séparément les fonctions qui peuvent être commandées.
Une fonction non décrite ou sans `momentary` est normale (`false`).
Cette liste décrit le comportement du bouton, pas son état actif.

La création sans liste applique le comportement normal à toutes les fonctions.
Une modification sans liste, ou avec `null`, conserve les définitions.
Une liste fournie remplace toutes les définitions ; `[]` les réinitialise.
Les anciens clients peuvent donc modifier les autres propriétés sans effacer
les réglages des fonctions. La migration SQLite ne change aucun réglage
existant. Les archives `.dcclib` version 7 transportent ces définitions.

## Appui et relâchement

Le client utilise la commande existante :

```http
PUT /api/v1/locomotives/{locomotiveId}/functions/2
Content-Type: application/json

{ "leaseId": "...", "enabled": true }
```

Pour une fonction temporaire, il envoie `true` à l'appui et `false` au
relâchement. Il doit également envoyer `false` si le geste est annulé ou si
le bouton perd le focus pendant l'appui. Aucune durée d'impulsion n'est imposée.
Pour une fonction normale, le bouton conserve son fonctionnement à bascule.

## Arrêts gérés par le serveur

Le serveur suit les fonctions temporaires activées et leur session propriétaire.
Il envoie `OFF` dans les cas suivants :

- fermeture du dernier WebSocket d'événements de cette session ;
- libération, expiration ou transfert de la réservation de l'engin ;
- déconnexion explicite, révocation ou expiration de la session ;
- désactivation du compte propriétaire.

La fermeture d'un WebSocket d'une autre session ne les arrête pas.
Si une session a plusieurs WebSockets, seul le dernier à se fermer déclenche
l'arrêt. Un client dont le flux a été fermé doit le reconnecter avant un nouvel
appui temporaire. Les appuis déjà en attente lors d'une déconnexion sont rejetés,
même si le client se reconnecte avant leur exécution.

Les clients HTTP sans WebSocket restent acceptés. Ils doivent envoyer l'ordre
de relâchement ; l'expiration de leur réservation sert de secours.
Une coupure réseau silencieuse ne ferme pas nécessairement le socket tout de
suite. L'arrêt intervient alors à l'expiration de la réservation, suivie du
prochain passage du moniteur. Ce délai dépend de `control.leaseTTL` et de
`control.monitorPeriod`. Les clients doivent arrêter de renouveler une
réservation dont ils ont perdu le contrôle.

Un ordre `OFF` temporaire est autorisé même si l'alimentation est coupée,
inconnue, ou si un arrêt d'urgence est actif. Les autres contrôles de propriété
et de disponibilité de la centrale restent appliqués.

Si l'envoi échoue, le serveur conserve l'arrêt en attente et le réessaie au
prochain passage du moniteur. Une réservation en cours de libération reste
bloquée jusqu'au succès de l'arrêt. Les autres clients reçoivent
`locomotive.function.changed` avec `enabled=false` après l'envoi réussi.
Les fonctions normales ne sont pas désactivées par ce mécanisme.

La désactivation physique nécessite une centrale joignable. Le suivi des appuis
est en mémoire : il ne remplace pas une protection matérielle et ne survit pas
à un arrêt brutal du processus serveur.
