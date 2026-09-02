# Campagne matérielle des aiguillages

Cette procédure couvre AIG-009. Elle complète les tests automatiques avec des
observations impossibles à prouver par un faux serveur : sortie réellement
actionnée, convention d'adresse du constructeur, échauffement, mouvement
mécanique et comportement d'un autre client.

Au 2 septembre 2026, aucune fiche datée n'est présente dans ce répertoire. Le
support logiciel est testé sans matériel, mais la recette z21/DCC-EX décrite
ici reste à exécuter. Ne pas transformer les modèles `*-TEMPLATE.md` en résultat
positif sans observation réelle.

## 1. Sécurité

- Utiliser d'abord des LEDs, relais ou charges protégées lorsque le câblage est
  incertain.
- Vérifier les limites électriques du décodeur et de l'actionneur.
- Garder la coupure d'alimentation et l'arrêt d'urgence accessibles.
- Ne jamais maintenir un solénoïde alimenté au-delà de sa durée admissible.
- Isoler la traction si elle n'est pas nécessaire.
- Réserver les appareils testés et prévenir les autres opérateurs.
- Arrêter immédiatement en cas d'échauffement, bruit continu, odeur ou
  mouvement imprévu.

Le script exige `--acknowledge-hardware-risk`. Cette option autorise l'envoi
des commandes ; elle ne remplace aucune vérification de sécurité.

## 2. Informations à relever avant le test

Copier le modèle correspondant vers un nom daté :

```bash
cp docs/hardware-tests/turnouts/z21-TEMPLATE.md \
  docs/hardware-tests/turnouts/z21-2026-09-02.md

cp docs/hardware-tests/turnouts/dccex-TEMPLATE.md \
  docs/hardware-tests/turnouts/dccex-2026-09-02.md
```

Renseigner avant toute commande :

- date, version/commit TrainPilot, OS et machine ;
- centrale, firmware et driver ;
- décodeur et firmware si connu ;
- type d'actionneur ou charge de test ;
- adresse affichée par le constructeur et adresse linéaire TrainPilot ;
- câblage `position1`/`position2` et éventuelle inversion ;
- `station.offlineAfter`, `station.accessoryPulse` et
  `turnout.confirmationTimeout` ;
- moyen d'observation des paquets, logs et mouvements.

Ne jamais placer un mot de passe ou un jeton dans une fiche ou un log.

## 3. Préparation de TrainPilot

L'utilisateur doit avoir le rôle `dispatcher` ou `administrator`. Le layout
doit déjà contenir le turnout et ses positions logiques. Vérifier sans commander :

```bash
DCC_PASSWORD='mot-de-passe-local' \
go run ./cmd/dccctl \
  --server http://127.0.0.1:8080 \
  --username dispatcher \
  --password-env DCC_PASSWORD \
  turnouts

DCC_PASSWORD='mot-de-passe-local' \
go run ./cmd/dccctl \
  --server http://127.0.0.1:8080 \
  --username dispatcher \
  --password-env DCC_PASSWORD \
  turnout T1 --positions
```

Effectuer d'abord une simulation de la campagne :

```bash
scripts/test-turnouts.sh \
  --server http://127.0.0.1:8080 \
  --username dispatcher \
  --password-env DCC_PASSWORD \
  --turnout T1 \
  --positions straight,diverging \
  --cycles 20 \
  --dry-run
```

Le mode réel est volontairement interactif :

```bash
scripts/test-turnouts.sh \
  --server http://127.0.0.1:8080 \
  --username dispatcher \
  --password-env DCC_PASSWORD \
  --turnout T1 \
  --positions straight,diverging \
  --cycles 20 \
  --delay 0.5 \
  --external-check \
  --offline-check \
  --log /tmp/trainpilot-z21-turnouts.log \
  --acknowledge-hardware-risk
```

Le fichier passé à `--log` doit être recopié ou résumé dans la fiche. Le script
utilise un état `dccctl` temporaire, supprimé à la fin, sauf si `--state-file`
est fourni.

## 4. Aiguillage simple et adressage

Pour chaque adresse testée :

1. commander `position1` et confirmer une seule activation de la bonne sortie ;
2. commander `position2` et confirmer une seule activation de l'autre sortie ;
3. effectuer vingt allers-retours ;
4. relever les commandes manquantes ou dupliquées ;
5. noter la position et la qualité retournées par TrainPilot ;
6. vérifier l'absence d'échauffement anormal.

Pour z21, tester au minimum les adresses linéaires `1`, `4`, `5`, `8` et `9`.
Elles encadrent deux changements de groupe de quatre. Pour DCC-EX, reprendre
ces valeurs et au moins une adresse réellement utilisée. Reporter chaque
correspondance :

| Affichage constructeur | Adresse TrainPilot | Sortie attendue | Résultat |
| --- | ---: | --- | --- |
| à renseigner | à renseigner | position1/position2 | PASS/FAIL |

Un écart ne doit pas être corrigé par plusieurs conversions empilées. Noter la
convention observée avant toute correction de code ou de configuration.

## 5. Pulse z21

Exécuter séparément la recette simple avec :

```text
station.accessoryPulse = 50ms
station.accessoryPulse = 100ms
station.accessoryPulse = 150ms
```

Chaque modification exige un redémarrage de `dccd`. Pour chaque durée :

- observer l'activation `A=1`, puis la désactivation `A=0` ;
- confirmer le mouvement complet et l'absence d'alimentation persistante ;
- noter si le décodeur possède sa propre coupure automatique ;
- interrompre une requête pendant l'impulsion si le banc permet de le faire
  sans risque, puis vérifier que `A=0` est tout de même envoyé.

La valeur par défaut reste `100ms` tant que la campagne ne démontre pas qu'une
autre valeur est préférable pour le matériel de référence.

## 6. Retours et changements externes

### z21

1. Commander `position1` depuis TrainPilot.
2. Capturer ou observer `LAN_X_TURNOUT_INFO`.
3. Commander `position2` depuis Z21 App, WLANmaus ou multiMAUS.
4. Vérifier que `reportedPosition` évolue sans modifier automatiquement
   `desiredPosition` ni renvoyer l'ancienne position.
5. Vérifier `quality=station`.

Cette qualité confirme l'état rapporté par la centrale, pas la position
physique des lames.

### DCC-EX

Lancer la phase externe avec :

```bash
scripts/test-turnouts.sh ... \
  --external-check \
  --external-expect none \
  --acknowledge-hardware-risk
```

Noter si un autre client envoyant `<a linear 0|1>` provoque réellement un
retour exploitable. En l'absence de retour documenté, TrainPilot doit conserver
la qualité `assumed` après ses propres écritures et ne rien inventer.

## 7. Coupure, reconnexion et absence de rejeu

La phase `--offline-check` guide la séquence suivante :

1. placer l'appareil dans la première position ;
2. couper la centrale ou le transport ;
3. attendre au-delà de `station.offlineAfter` ;
4. présenter la deuxième position et vérifier son refus ;
5. rétablir la centrale ;
6. attendre `20` secondes sans commande ;
7. confirmer l'absence de mouvement ;
8. envoyer une nouvelle commande explicite ;
9. confirmer exactement un mouvement.

Pour z21, consigner les transitions `online/degraded/offline/online`. Pour
DCC-EX, consigner également la rupture du socket et la reconnexion TCP. Une
commande refusée ne doit jamais être mise en file ni rejouée.

## 8. Triple et appareils composés

Avec un triple réel ou deux charges de test, déclarer les vecteurs A/B réels.
Exécuter :

```bash
scripts/test-turnouts.sh \
  --server http://127.0.0.1:8080 \
  --username dispatcher \
  --password-env DCC_PASSWORD \
  --turnout T3 \
  --positions left,straight,right,straight,left,right,left \
  --cycles 1 \
  --acknowledge-hardware-risk
```

Enregistrer la séquence des endpoints pour chaque transition. La combinaison
interdite A2/B2 de la fixture de référence ne doit jamais être maintenue ni
commandée. Si elle est imposée extérieurement, TrainPilot doit afficher une
position rapportée inconnue/invalide.

Sans triple réel, une validation par deux LEDs ou relais prouve uniquement la
séquence électrique. La fiche doit conserver « triple mécanique non testé ».

## 9. TJD

Construire la table à partir du câblage réel. Ne pas reprendre une table
générique d'une autre marque. Tester les quatre positions, puis les douze
transitions ordonnées entre positions distinctes. Une séquence couvrant les
paires peut être fournie à `--positions`, ou chaque paire peut être exécutée
séparément pour faciliter le diagnostic.

Sans TJD réelle, les tests automatiques AIG-008 restent la seule validation du
modèle. La fiche matérielle doit alors indiquer `NOT_TESTED`, pas `PASS`.

## 10. Échec partiel

Sur une charge sûre, débrancher temporairement un endpoint ou son actionneur :

- vérifier que l'erreur ou le timeout est visible ;
- vérifier qu'aucun faux état `physical` n'est produit ;
- relever l'état logique intermédiaire et l'événement publié ;
- ne pas attendre de rollback automatique, hors périmètre actuel.

Une z21 peut rapporter l'état électrique commandé malgré l'absence de mouvement
mécanique. Ce résultat doit être consigné comme limite du retour `station`.

## 11. Endurance

- Pour 500 commandes simples avec deux positions, utiliser `--cycles 250`.
- Pour 100 transitions de triple, fournir une séquence sûre et choisir un nombre
  de cycles donnant au moins 100 changements.
- Pour une TJD, tester 100 transitions uniquement si le matériel supporte cette
  campagne sans risque.

Relever la durée, les erreurs driver, commandes manquantes ou doubles,
reconnexions, mémoire et nombre de goroutines si un outil d'observation est
disponible. Aucune exigence de débit extrême n'est imposée.

## 12. Conclusion et classement

Chaque ligne doit être classée `PASS`, `FAIL`, `BLOCKED` ou `NOT_TESTED`.
Une campagne est exploitable seulement si la fiche contient les versions, le
matériel, le mapping, les résultats observés et les références de logs.

Après une campagne réelle :

1. ajouter la fiche datée ;
2. mettre à jour `docs/TURNOUTS.md` avec les faits observés ;
3. mettre à jour `docs/TESTING.md` et les backlogs ;
4. corriger le code uniquement si l'observation est reproductible ;
5. rejouer toute la validation logicielle avant livraison.
