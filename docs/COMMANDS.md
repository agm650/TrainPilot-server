# Référence des commandes TrainPilot

Ce document couvre les commandes fournies par les trois binaires du dépôt :

- `dccd` : serveur et administration locale des utilisateurs ;
- `dccctl` : client interactif de l'API TrainPilot ;
- `dcc-api-conformance` : validation du contrat d'une instance.

Les endpoints HTTP ne sont pas recopiés ici. Leur référence reste
`api/openapi.yaml`. Les commandes de développement et les scripts de test sont
documentés dans `docs/TESTING.md`.

Les exemples utilisent des binaires installés. Pendant le développement,
remplacer par `go run ./cmd/dccd`, `go run ./cmd/dccctl` ou
`go run ./cmd/dcc-api-conformance`.

## Précautions

Les commandes suivantes peuvent agir sur du matériel réel :

- `dccctl throttle` et `dccctl function` ;
- `dccctl power on` et `dccctl power off` ;
- `dccctl emergency-stop` ;
- `dccctl turnout` avec une position ;
- `dcc-api-conformance --allow-active-commands` ;
- `dcc-api-conformance --check-turnouts`.

Les utiliser uniquement sur une centrale explicitement sélectionnée.
Pour les tests automatisés, utiliser le simulateur.

## Variables utilisées dans les exemples

```bash
export TRAINPILOT_URL='http://127.0.0.1:8080'
export DCC_PASSWORD='correct-horse-1'
export DCC_DISPATCHER_PASSWORD='correct-horse-dispatcher'
export DCC_ADMIN_PASSWORD='correct-horse-admin'
export DCCD_SOCKET='/tmp/dccd-admin.sock'
```

Ne pas conserver de vrais mots de passe dans l'historique du shell.

## `dccd`

### `dccd serve`

Démarre le serveur HTTP, le socket Unix d'administration et la centrale
configurée. Le listener de diagnostic est aussi démarré s'il est activé.

```bash
dccd serve --config config.json
```

Options :

- `--config <fichier>` : charge une configuration JSON ;
- sans `--config` : utilise les valeurs par défaut intégrées.

### Options communes de `dccd user`

Les commandes utilisateur parlent au socket Unix local. Le serveur doit être
en cours d'exécution.

- `--socket <chemin>` : socket d'administration ; défaut
  `/tmp/dccd-admin.sock` ;
- `--username <nom>` : utilisateur ciblé ;
- `--display-name <nom>` : nom affiché lors d'une création ;
- `--role <rôle>` : `viewer`, `driver`, `dispatcher` ou `administrator` ;
- `--must-change` : impose un changement de mot de passe ;
- `--password-stdin` : lit le mot de passe sur l'entrée standard.

### `dccd user bootstrap`

Crée le premier utilisateur. Cette commande est refusée dès qu'un utilisateur
existe déjà.

```bash
printf '%s\n' "$DCC_ADMIN_PASSWORD" | dccd user bootstrap \
  --socket "$DCCD_SOCKET" \
  --username admin \
  --display-name 'Administrateur' \
  --role administrator \
  --password-stdin
```

### `dccd user add`

Ajoute un utilisateur activé.

```bash
printf '%s\n' "$DCC_PASSWORD" | dccd user add \
  --socket "$DCCD_SOCKET" \
  --username alice \
  --display-name 'Alice' \
  --role driver \
  --password-stdin
```

Ajouter `--must-change` pour imposer un nouveau mot de passe à la prochaine
connexion.

### `dccd user list`

Liste les utilisateurs, leurs rôles et leur état d'activation.

```bash
dccd user list --socket "$DCCD_SOCKET"
```

### `dccd user enable`

Réactive un utilisateur désactivé.

```bash
dccd user enable --socket "$DCCD_SOCKET" --username alice
```

### `dccd user disable`

Désactive un utilisateur et révoque ses sessions actives.

```bash
dccd user disable --socket "$DCCD_SOCKET" --username alice
```

### `dccd user role`

Change le rôle d'un utilisateur.

```bash
dccd user role \
  --socket "$DCCD_SOCKET" \
  --username alice \
  --role dispatcher
```

### `dccd user passwd`

Remplace le mot de passe et révoque les sessions de l'utilisateur.

```bash
printf '%s\n' "$DCC_PASSWORD" | dccd user passwd \
  --socket "$DCCD_SOCKET" \
  --username alice \
  --password-stdin \
  --must-change
```

## `dccctl`

### Authentification et options globales

Chaque commande métier exige `--username`.

- `--server <URL>` : URL du serveur ; défaut `http://127.0.0.1:8080` ;
- `--username <nom>` : utilisateur de l'API ; obligatoire ;
- `--password-env <variable>` : variable contenant le mot de passe ;
- `--state-file <fichier>` : stockage local de la session et des leases.

Si `--password-env` est absent, `dccctl` demande le mot de passe. Le mot de
passe n'est jamais écrit dans le fichier d'état. Les tokens et leases le sont,
avec des permissions `0600`.

Le préfixe commun utilisé ci-dessous est :

```bash
dccctl --server "$TRAINPILOT_URL" --username alice --password-env DCC_PASSWORD
```

### `dccctl locomotives`

Liste les locomotives avec leur ID, leur adresse DCC et leur nom.

```bash
dccctl --server "$TRAINPILOT_URL" --username alice \
  --password-env DCC_PASSWORD locomotives
```

### `dccctl locomotive-show`

Affiche toutes les propriétés d'une locomotive.

```bash
dccctl --server "$TRAINPILOT_URL" --username alice \
  --password-env DCC_PASSWORD locomotive-show loco-bb26001
```

### `dccctl locomotive-add`

Ajoute une locomotive. Le rôle `administrator` est requis.

Syntaxe :

```text
locomotive-add <nom> <adresse-dcc> [short|long] [14|28|128] [fabricant] [modèle]
```

Exemple :

```bash
dccctl --server "$TRAINPILOT_URL" --username admin \
  --password-env DCC_ADMIN_PASSWORD \
  locomotive-add 'BB 26001' 3 short 128 Jouef 'BB 26000'
```

Le type d'adresse est déduit si son argument est absent.

### `dccctl locomotive-update`

Modifie une locomotive. Le rôle `administrator` est requis. Une locomotive
avec un lease actif ne peut pas être modifiée.

```bash
dccctl --server "$TRAINPILOT_URL" --username admin \
  --password-env DCC_ADMIN_PASSWORD \
  locomotive-update loco-bb26001 'BB 26001 rénovée' 3 short 128 Jouef 'BB 26000'
```

### `dccctl locomotive-delete`

Supprime une locomotive. Le rôle `administrator` est requis. Une locomotive
référencée par l'historique des leases ne peut pas être supprimée.

```bash
dccctl --server "$TRAINPILOT_URL" --username admin \
  --password-env DCC_ADMIN_PASSWORD locomotive-delete loco-test
```

### `dccctl acquire`

Acquiert le contrôle exclusif d'une locomotive. Le lease est conservé dans le
fichier d'état local.

```bash
dccctl --server "$TRAINPILOT_URL" --username alice \
  --password-env DCC_PASSWORD acquire loco-bb26001
```

### `dccctl throttle`

Règle la vitesse et le sens. Un lease sauvegardé par `acquire` est obligatoire.
La vitesse est comprise entre 0 et 100. Le sens vaut `forward` par défaut.

```bash
dccctl --server "$TRAINPILOT_URL" --username alice \
  --password-env DCC_PASSWORD throttle loco-bb26001 40 forward
```

Une vitesse de `0` est une commande d'arrêt prioritaire.

### `dccctl function`

Active ou désactive une fonction de locomotive. Un lease est obligatoire.
Le numéro est compris entre 0 et 68, sous réserve des capacités de la centrale.

```bash
dccctl --server "$TRAINPILOT_URL" --username alice \
  --password-env DCC_PASSWORD function loco-bb26001 0 true
```

Utiliser `false` pour désactiver la fonction.

### `dccctl release`

Demande l'arrêt contrôlé puis la libération du lease.

```bash
dccctl --server "$TRAINPILOT_URL" --username alice \
  --password-env DCC_PASSWORD release loco-bb26001
```

### `dccctl power status`

Affiche la connectivité, l'alimentation, l'arrêt d'urgence et la télémétrie
connue de la centrale.

```bash
dccctl --server "$TRAINPILOT_URL" --username alice \
  --password-env DCC_PASSWORD power status
```

### `dccctl power on`

Active l'alimentation de la voie. Aucun lease n'est requis.

```bash
dccctl --server "$TRAINPILOT_URL" --username alice \
  --password-env DCC_PASSWORD power on
```

Après un arrêt d'urgence, cette commande autorise de nouveau les commandes
actives si elle réussit.

### `dccctl power off`

Coupe l'alimentation de la voie. Cette commande de sécurité est prioritaire.

```bash
dccctl --server "$TRAINPILOT_URL" --username alice \
  --password-env DCC_PASSWORD power off
```

### `dccctl emergency-stop`

Envoie un arrêt d'urgence global. Cette commande est prioritaire.

```bash
dccctl --server "$TRAINPILOT_URL" --username alice \
  --password-env DCC_PASSWORD emergency-stop
```

### `dccctl turnouts`

Liste les aiguillages et leur état opérationnel.

```bash
dccctl --server "$TRAINPILOT_URL" --username dispatcher \
  --password-env DCC_DISPATCHER_PASSWORD turnouts
```

### `dccctl turnout --positions`

Liste les positions logiques déclarées pour un aiguillage.

```bash
dccctl --server "$TRAINPILOT_URL" --username dispatcher \
  --password-env DCC_DISPATCHER_PASSWORD turnout turnout-1 --positions
```

### `dccctl turnout <id> <position>`

Commande une position logique. Le rôle `dispatcher` ou `administrator` est
requis. Seules les positions déclarées sont acceptées.

```bash
dccctl --server "$TRAINPILOT_URL" --username dispatcher \
  --password-env DCC_DISPATCHER_PASSWORD turnout turnout-1 diverging
```

### `dccctl export-rolling-stock`

Exporte le matériel roulant dans une archive. Le fichier est écrit avec des
permissions `0600`. Un fichier existant est remplacé.

```bash
dccctl --server "$TRAINPILOT_URL" --username alice \
  --password-env DCC_PASSWORD export-rolling-stock rolling-stock.zip
```

### `dccctl import-rolling-stock`

Importe une archive de matériel roulant. Le rôle `administrator` est requis.
Par défaut, les données sont fusionnées.

```bash
dccctl --server "$TRAINPILOT_URL" --username admin \
  --password-env DCC_ADMIN_PASSWORD import-rolling-stock rolling-stock.zip
```

Ajouter `--replace` pour remplacer la bibliothèque existante. Cette option est
destructrice et peut être refusée si des leases sont actifs.

### `dccctl export-layout`

Exporte les cantons, mappings de feedback, aiguillages et itinéraires.

```bash
dccctl --server "$TRAINPILOT_URL" --username alice \
  --password-env DCC_PASSWORD export-layout layout.zip
```

### `dccctl import-layout`

Importe une archive de réseau. Le rôle `administrator` est requis. Par défaut,
les données sont fusionnées.

```bash
dccctl --server "$TRAINPILOT_URL" --username admin \
  --password-env DCC_ADMIN_PASSWORD import-layout layout.zip
```

Ajouter `--replace` pour remplacer la configuration existante.

### Aide et autocomplétion

Afficher l'aide générale ou celle d'une commande :

```bash
dccctl --help
dccctl help throttle
```

Générer l'autocomplétion pour `bash`, `fish`, `powershell` ou `zsh` :

```bash
dccctl --username alice --password-env DCC_PASSWORD completion bash > dccctl.bash
dccctl --username alice --password-env DCC_PASSWORD completion fish > dccctl.fish
dccctl --username alice --password-env DCC_PASSWORD completion powershell > dccctl.ps1
dccctl --username alice --password-env DCC_PASSWORD completion zsh > _dccctl
```

La commande d'autocomplétion hérite actuellement de l'initialisation globale.
Elle requiert donc un nom d'utilisateur et une session valide.

## `dcc-api-conformance`

Ce binaire vérifie le contrat public d'un serveur en cours d'exécution. Il
retourne un code non nul si au moins une vérification échoue.

### Vérifications passives

Le mode par défaut vérifie la santé, les versions, l'authentification, les
lectures, les erreurs structurées et les exports. Il ne commande pas la voie.

```bash
dcc-api-conformance \
  --server "$TRAINPILOT_URL" \
  --user1 alice --pass1 "$DCC_PASSWORD" \
  --user2 bob --pass2 'correct-horse-2'
```

Ce mode crée et révoque des sessions de test.

### Inventaire des endpoints

Affiche chaque endpoint public et sa classe de conformité. Aucun serveur n'est
contacté.

```bash
dcc-api-conformance --list-endpoints
```

### Commandes actives

Ajoute les tests d'alimentation, de lease, de vitesse et de fonctions.

```bash
dcc-api-conformance \
  --server "$TRAINPILOT_URL" \
  --user1 alice --pass1 "$DCC_PASSWORD" \
  --user2 bob --pass2 'correct-horse-2' \
  --allow-active-commands
```

Utiliser uniquement une instance de test explicitement sélectionnée.

### Mutations de configuration

Ajoute les tests CRUD et les imports temporaires. Un compte administrateur est
obligatoire. Utiliser une base jetable.

```bash
dcc-api-conformance \
  --server "$TRAINPILOT_URL" \
  --user1 alice --pass1 "$DCC_PASSWORD" \
  --user2 bob --pass2 'correct-horse-2' \
  --admin admin --admin-pass "$DCC_ADMIN_PASSWORD" \
  --allow-configuration-mutations
```

### Vérification des aiguillages

Commande les positions déclarées et vérifie leurs confirmations. Un compte
administrateur est obligatoire.

```bash
dcc-api-conformance \
  --server "$TRAINPILOT_URL" \
  --user1 alice --pass1 "$DCC_PASSWORD" \
  --user2 bob --pass2 'correct-horse-2' \
  --admin admin --admin-pass "$DCC_ADMIN_PASSWORD" \
  --check-turnouts
```

Cette option est active même sans `--allow-active-commands`.

### Expiration des sessions

Vérifie l'expiration naturelle des access tokens et refresh tokens. Utiliser
des TTL courts sur une instance dédiée.

```bash
dcc-api-conformance \
  --server "$TRAINPILOT_URL" \
  --user1 alice --pass1 "$DCC_PASSWORD" \
  --user2 bob --pass2 'correct-horse-2' \
  --check-session-expiration \
  --session-expiration-max-wait 15s
```

La durée maximale vaut `15s` par défaut. Elle évite d'attendre les TTL de
production.

### Options complètes

- `--server <URL>` : serveur ciblé ;
- `--user1`, `--pass1` : premier compte conducteur ;
- `--user2`, `--pass2` : second compte conducteur ;
- `--admin`, `--admin-pass` : compte utilisé par les tests administratifs ;
- `--allow-active-commands` : autorise les commandes de voie ;
- `--allow-configuration-mutations` : autorise les mutations temporaires ;
- `--check-turnouts` : commande et vérifie les aiguillages ;
- `--check-session-expiration` : vérifie les expirations naturelles ;
- `--session-expiration-max-wait <durée>` : borne chaque attente ;
- `--list-endpoints` : affiche l'inventaire puis quitte.

Les mots de passe sont passés en arguments par cet outil. Utiliser uniquement
des comptes de test dédiés.
