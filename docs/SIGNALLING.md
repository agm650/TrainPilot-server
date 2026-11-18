# Signalisation — spécification SIG-001

Version cible du serveur : **2.0.0**, avec les autres lots de signalisation.
Ce document fixe le contrat validé le 18 novembre 2026 pour SIG-002 à SIG-007.
La validation comprend les précisions de généricité apportées par l'utilisateur.
Les exemples
décrivent des données futures, pas des fonctionnalités déjà disponibles.

## Décisions confirmées le 18 novembre 2026

- L'aval est déclaré explicitement : signal suivant ou limite terminale.
- Le banc utilisera un décodeur de feux maison à base d'ATtiny.
- Ce décodeur recevra les paquets **Extended Accessory Decoder Control**.
- Le mapping des aspects métier vers les codes du décodeur sera configurable.
- Tout décodeur conforme au protocole étendu peut être ciblé, selon les plages
  et capacités de la centrale. Le fabricant et le microcontrôleur ne sont pas
  des critères d'acceptation dans le serveur.
- Les profils de signalisation sont configurables. `SNCF_BAL_3` est un profil
  fourni, sans imposer ses noms, couleurs ou codes aux profils personnalisés.
- Le LEC000043 pourra participer à une étude ultérieure du freinage ABC.
  La compatibilité du montage complet avec le Roco 10819 reste à vérifier.

Le firmware de la z21, celui de l'ATtiny et sa table de codes ne sont pas encore
consignés. Ils devront figurer dans la recette du banc. Ce manque concerne la
recette matérielle ; il ne bloque pas le modèle générique et le simulateur.

## État de l'implémentation

SIG-001 est validé avec les précisions de généricité ci-dessus. SIG-002 fournit
les types et validations dans [signal.go](../internal/model/signal.go) et
[signal_profile.go](../internal/model/signal_profile.go). Le profil SNCF est
un [preset séparé](../internal/model/signal_profile_bal.go).

Les tests couvrent aussi un profil personnalisé, des codes matériels
arbitraires, plusieurs cibles, neuf feux, l'extinction et les indications
combinées. Le modèle représente `flashing` sans exécuter de clignotement.
Un état opérationnel neuf utilise le repli du profil et n'a aucun retour
confirmé. Ces types ne sont pas encore exposés par une API ni persistés.

Le modèle valide la forme des références ; leur existence et leur continuité
orientée dans le circuit seront vérifiées par SIG-006. Le jeu `bal-3` est
identifié et validé dans les profils, mais son calcul reste prévu par SIG-014.

SIG-003 fournit l'interface optionnelle `station.ExtendedAccessoryController`,
la commande validée et `SendExtendedAccessory`. Les limites portables sont
`1..2040` et `0..255`. Un pilote peut annoncer des limites plus étroites dans
`extendedAccessoryLimits`. Sans cette propriété, les limites portables
s'appliquent. Sans `extendedAccessoryControl: true`, la commande est refusée.
L'interface et la capacité doivent toutes deux être présentes. Les pilotes
z21 et DCC-EX annoncent encore le support étendu à `false`.
Le contrat HTTP est 1.16.0 ; le contrat événements est 1.14.0.
L'ajout de capacités optionnelles n'impose pas de migration des anciens clients.

Le retour optionnel `ExtendedAccessoryStateEvent` distingue un aspect connu
d'un retour inconnu. Il porte une qualité explicite et ne transforme jamais
un succès d'envoi en confirmation physique. La saturation du flux sera
documentée par chaque pilote qui implémente ce retour.

SIG-004 annonce le support étendu dans le simulateur. Les observations y ont
la qualité `assumed`. Le flux optionnel comporte 64 entrées, en livraison
best-effort ; le snapshot contient l'état courant même si une notification
est perdue. Un envoi identique reste dans l'historique, sans événement de
changement supplémentaire. Une coupure invalide les aspects observés ; la
reconnexion ne rejoue aucune commande. Un reset efface les états et le journal.
L'opération `extended_accessory` accepte les mêmes défauts et délais que les
autres opérations simulées. Les scénarios versions 1/2 acceptent l'action
`extended_accessory.set` avec les champs `address` et `aspect` obligatoires.

## Périmètre du premier jalon

Le premier jalon couvre le BAL de pleine voie à trois aspects : sémaphore
`S`, avertissement `A` et voie libre `VL`. La signalisation est informative.
Elle n'accorde aucune autorité de mouvement et ne commande pas l'arrêt d'une
locomotive. Elle ne remplace pas les contrôles d'occupation des itinéraires.

Les itinéraires à travers les aiguillages, l'identification des trains,
la couverture intégrale du réseau, le freinage ABC et la circulation
automatique restent hors de ce premier jalon.

| Profil | Aspects ou représentation | Livraison |
| --- | --- | --- |
| `SNCF_BAL_3` | `S` rouge, `A` jaune, `VL` vert | Premier jalon |
| Manœuvre | Carré violet `Cv`, blanc `M` | Extension SIG-021 |
| TID | Indication de direction, fondée sur une direction établie | Extension SIG-022 |
| Protection des aiguillages | Carré et règles d'itinéraire | Extension SIG-023 |
| Cibles complexes | Combinaisons, extinction et clignotement, jusqu'à neuf feux | Extension SIG-024 |

Les règles automatiques des extensions ne sont pas disponibles tant qu'elles
ne sont pas implémentées. Un profil personnalisé valide pourra être utilisé
en commande manuelle dès la livraison de celle-ci. Il ne reçoit aucune règle
SNCF implicite. Le calcul BAL automatique sera livré au lot D.
Neuf feux décrivent une représentation ; ils n'imposent ni neuf sorties DCC,
ni neuf adresses, ni une correspondance entre un bit et une lampe.

## Configuration, représentation et état opérationnel

Trois notions restent indépendantes :

- **Aspect métier** : identifiant tel que `S`, `A` ou `VL`, interprété par les règles.
- **Représentation** : cibles et feux allumés, éteints ou, ultérieurement, clignotants.
- **Code matériel** : entier envoyé à une adresse DCC étendue pour cet aspect.

Un profil définit les aspects autorisés et leur représentation. Un signal
définit son profil, son implantation orientée, son canton protégé, son aval,
son mode, son repli et son endpoint. Le mapping matériel appartient à
l'endpoint ; les règles BAL ne dépendent jamais des codes du décodeur.

Quatre éléments sont configurés séparément :

| Élément | Contenu |
| --- | --- |
| Profil de signalisation | Identifiants et noms d'aspects, cibles, feux et couleurs |
| Sémantique des aspects | Rôles `stop`, `caution`, `proceed` ou `other`, indépendants des noms |
| Jeu de règles | `bal-3` pour le premier calcul automatique ; absent pour un profil manuel |
| Endpoint matériel | Adresse DCC et mapping des identifiants d'aspects vers les codes du décodeur |

Le jeu `bal-3` exige trois aspects, un par rôle `stop`, `caution`, `proceed`.
Un profil personnalisé peut réutiliser ce jeu avec d'autres identifiants et
représentations. Un profil plus complexe peut déclarer plusieurs aspects du
même rôle en mode manuel. Il exige un jeu de règles adapté pour l'automatique.
Le rôle `other` ne devient jamais implicitement un aspect permissif.

Un profil définit son aspect de repli, de rôle `stop`. La définition d'un
signal peut le préciser avec un autre aspect `stop` du même profil.
Le cœur générique ne recherche jamais les chaînes `S`, `A` ou `VL`.

Exemple de profil personnalisé réutilisant la sémantique BAL :

```json
{
  "id": "CUSTOM_3",
  "name": "Profil personnalisé à trois aspects",
  "ruleSetId": "bal-3",
  "fallbackAspect": "HALT",
  "targets": [{ "id": "main", "name": "Cible principale" }],
  "lights": [
    { "id": "l1", "targetId": "main", "color": "#FF0000" },
    { "id": "l2", "targetId": "main", "color": "#FFFF00" },
    { "id": "l3", "targetId": "main", "color": "#00FF00" }
  ],
  "aspects": [
    { "id": "HALT", "name": "Arrêt", "role": "stop", "indications": [{ "lightId": "l1", "state": "on" }] },
    { "id": "WARN", "name": "Prudence", "role": "caution", "indications": [{ "lightId": "l2", "state": "on" }] },
    { "id": "GO", "name": "Passage", "role": "proceed", "indications": [{ "lightId": "l3", "state": "on" }] }
  ]
}
```

La liste des indications est parcimonieuse : un feu absent est éteint et une
liste vide décrit l'extinction complète. Les feux peuvent être regroupés sur
plusieurs cibles. Le modèle réserve l'état `flashing` ; son exécution et sa
présentation détaillée relèvent des extensions prévues.

Un seul endpoint étendu suffit au premier jalon. La représentation du profil
peut contenir plusieurs cibles. La commande coordonnée de plusieurs endpoints
pour un même signal nécessite un contrat ultérieur de succès partiel.

Exemple de définition avec le profil SNCF fourni :

```json
{
  "id": "signal-a",
  "name": "Signal d'entrée du canton A",
  "profileId": "SNCF_BAL_3",
  "mode": "manual",
  "placement": {
    "trackSectionId": "section-a",
    "entryNodeId": "node-a"
  },
  "protectedBlockId": "block-a",
  "downstream": {
    "kind": "signal",
    "signalId": "signal-b"
  },
  "fallbackAspect": "S",
  "endpoint": {
    "kind": "dcc-extended",
    "linearAddress": 1,
    "aspectCodes": { "S": 0, "A": 1, "VL": 2 }
  }
}
```

Les codes `0`, `1`, `2` sont un exemple de convention. Ils ne constituent pas
une table universelle SNCF ou DCC. Pour un autre décodeur, seule l'adresse et
la table `aspectCodes` changent. Les codes configurés doivent correspondre
aux indications documentées ou programmées dans ce décodeur.
Un profil `CUSTOM_3` pourrait, par exemple, mapper `HALT`, `WARN`, `GO` vers
`0`, `7`, `16`, sans ajouter un pilote propre au fabricant.

L'état opérationnel distingue au minimum :

| Champ proposé | Signification |
| --- | --- |
| `computedAspect` | Résultat des règles, avec motif de repli si nécessaire |
| `desiredAspect` | Cible retenue selon le mode et les restrictions |
| `commandedAspect` | Dernier aspect dont la commande a été envoyée avec succès |
| `reportedAspect` | Dernier aspect observé par une source identifiée |
| `reportQuality` | `assumed`, `station` ou `physical`, selon la source réelle |
| `pending` | Commande en cours, sans présumer de confirmation physique |
| `synchronized` | Validité des données nécessaires au calcul courant |

Au démarrage et après import, les cibles reviennent au repli du signal ou du
profil, `S` dans l'exemple SNCF. Les champs
de commande et de retour sont inconnus. Une écriture réussie permet d'actualiser
`commandedAspect`, mais ne produit jamais une confirmation `physical`.
L'absence de retour est représentée par une valeur nulle, y compris pour la
qualité du retour. Un éventuel état `assumed` reste explicitement identifié.

```json
{
  "id": "signal-a",
  "computedAspect": "S",
  "desiredAspect": "S",
  "commandedAspect": null,
  "reportedAspect": null,
  "reportQuality": null,
  "pending": false,
  "synchronized": false
}
```

Sans capteur de feux, l'état physique reste non confirmé. Les aspects logiques
peuvent néanmoins être calculés et affichés. Le client expose leur qualité
et ne présente pas le calcul ou l'écho de la centrale comme un retour physique.

## Implantation et aval explicite

`entryNodeId` désigne l'extrémité de la section depuis laquelle on entre dans
le canton protégé. Cette référence définit l'orientation indépendamment du
dessin. Le nœud doit appartenir à la section déclarée. La section doit faire
partie du canton protégé.

Deux signaux opposés peuvent partager une frontière. Leurs identifiants,
orientations, cantons protégés et endpoints restent distincts. Les références
aval doivent suivre une continuité orientée non ambiguë. Les portions sans
couverture sont diagnostiquées ; elles ne sont jamais déclarées libres.

Une référence aval vise soit un signal existant, soit une limite terminale.
Les champs des deux formes sont exclusifs. Exemple de limite proposée :

```json
{
  "kind": "terminal",
  "aspect": "S"
}
```

Cette limite est une référence logique restrictive. Elle ne représente pas
un signal physique automatiquement découvert ou commandé. Avec un canton
protégé libre, elle conduit à `A` sur le signal précédent. Le premier jalon
n'autorise pas de terminal implicite à `VL`. Pour un profil personnalisé,
l'aspect terminal est un identifiant `stop` de ce profil. Un signal aval
d'un autre profil est interprété par le rôle de son aspect, pas par son nom.

Un aval manquant, orphelin, orienté à contre-sens ou ambigu est une erreur
de configuration. L'import est refusé atomiquement. Si cette incohérence est
détectée à l'exécution, la cible est le repli du profil et la synchronisation
est invalide.
Les boucles BAL valides restent autorisées ; leur évaluation ne doit pas
utiliser une récursion sans borne ni dépendre de l'ordre d'itération.

## Modes et matrice de repli

Le mode `manual` est le défaut d'une nouvelle définition. Il permet à un
opérateur autorisé de demander un aspect supporté. Le mode `automatic`
requiert un jeu de règles compatible explicitement déclaré par le profil.
Un jeu absent ou non supporté interdit le mode automatique. Un changement
de mode ne restaure pas une ancienne
commande permissive. Les restrictions communes restent prioritaires.

La matrice ci-dessous concerne le profil SNCF BAL. Les premières lignes ont
priorité. Le moteur traduit ses résultats par les rôles des aspects configurés.
Un aval « fiable » désigne ici un aspect logique valide dans la génération
courante, avec un état d'envoi exploitable, sans échec ou commande obsolète.
Une confirmation physique n'est pas exigée en l'absence de capteur, mais son
absence reste visible. Un retour de qualité inférieure ne devient pas physique.

| Situation | Cible BAL |
| --- | --- |
| Centrale `degraded` ou `offline`, puissance inconnue/OFF, défaut électrique ou resynchronisation du canton protégé incomplète | `S` |
| Configuration invalide, endpoint indisponible ou échec de commande | Repli `S`, état matériel non confirmé |
| Canton protégé occupé ou inconnu | `S` |
| Canton protégé libre, aval connu à `S` ou aval logique inconnu | `A` |
| Canton protégé libre, aval fiable à `A` ou `VL` | `VL` |
| Canton protégé libre, limite terminale explicitement à `S` | `A` |

Une absence totale de déclaration aval relève de l'erreur de configuration,
pas de la ligne « aval logique inconnu ». L'arrêt d'urgence seul ne signifie
pas une perte d'alimentation électrique ou de validité des détecteurs.
Une réponse partielle ne synchronise que les détecteurs effectivement observés.
L'absence d'une observation aval peut conduire à `A` si le canton protégé est
déjà fiable ; elle ne permet jamais de déclarer ce canton libre.

Les demandes manuelles ne contournent pas les défauts de centrale, de puissance,
de synchronisation ou de configuration. Un aspect permissif est refusé si le
canton protégé est occupé ou inconnu. Les règles ne modifient aucune vitesse
de locomotive et n'activent aucun freinage ABC.

Au retour de communication ou de puissance, une ancienne cible permissive
n'est jamais rejouée. Il faut une nouvelle acquisition des observations et un
nouveau calcul ou une nouvelle demande manuelle. Un repli calculé à `S` ne
prouve pas que sa commande a pu atteindre le décodeur pendant une coupure.

## Contrat de pilote étendu — proposition pour SIG-003

Ajouter une interface optionnelle `ExtendedAccessoryController`. La commande
contient une adresse et un code d'aspect validés avant toute émission.
`CommandStation.SetBasicAccessory` garde son contrat et ses limites actuelles.
Ce contrat transporte une adresse et un entier. Il ne connaît ni fabricant,
ni ATtiny, ni SNCF, ni couleur. Un décodeur conforme utilise le même chemin
de commande ; ses particularités se déclarent dans la configuration du signal.

La convention publique proposée utilise des adresses linéaires à partir de
`1`. La plage portable initiale proposée est `1..2040`. Cette borne est un
choix TrainPilot conservateur, distinct des limites brutes d'un protocole.
Les adresses réservées ou de diffusion ne sont jamais des endpoints de signal.
La plage générique des codes est `0..255`, réduite aux codes supportés par le
pilote et par le mapping du signal.

Chaque aspect accepté possède un code matériel. Les codes sont distincts pour
permettre une résolution inverse non ambiguë. Un aspect absent du profil ou du
mapping est refusé. La validation interdit deux propriétaires d'un même endpoint
dans le même espace de commandes. Une éventuelle collision entre commandes
binaires et étendues doit être étudiée selon le décodeur ; ne pas supposer
qu'une même valeur numérique prouve leur indépendance électrique.

Une capacité explicite annonce le support étendu. Une interface absente ou
une capacité fausse donne `unsupported`. Distinguer les erreurs de validation,
de transport et de disponibilité. Respecter l'annulation du contexte.

La documentation officielle z21, sections 5.4 à 5.6, décrit le support à partir
du firmware 1.40. L'adresse publique `1` correspond à l'adresse brute `4`.
Cette conversion ne réutilise pas celle des aiguillages binaires. Le retour
z21 rapporte la commande mémorisée par la centrale, sans prouver l'état des
feux. Son statut inconnu reste inconnu. L'intégration z21 appartient à SIG-012.

Le simulateur implémentera le contrat avec des états séparés par adresse,
des défauts et délais déterministes, un journal et un reset. Ses observations
représentent un banc simulé, jamais une preuve de fonctionnement matériel.
Le pilote DCC-EX sera traité séparément par SIG-013 et SIG-020.

## Persistance, archives et contrats clients

- Stocker les définitions, profils configurables, politiques et références.
- Ne jamais restaurer de commande en attente ou d'aspect physique confirmé.
- Migrer les bases existantes sans perdre le circuit ni l'occupation configurée.
- Produire des listes vides JSON `[]` et des révisions déterministes.
- Valider les références et remplacements dans la transaction d'import existante.
- Conserver une révision de présentation indépendante des états opérationnels.
- Importer les archives sans signaux comme des circuits sans signalisation.

Le format de circuit courant est **8**. SIG-007 proposera le format **9** pour
les signaux, avec lecture des versions 1 à 8. Le format du matériel roulant
reste **7**. Aucun aspect runtime ne figure dans l'archive.

La cible serveur `2.0.0` ne modifie pas automatiquement les majeures des API.
Les ajouts compatibles pourront conserver `/api/v1`. Les schémas et versions
OpenAPI/AsyncAPI évolueront avec les fonctions effectivement livrées.
SIG-001 n'annonce aucun endpoint, événement ou capacité déjà disponible.

Les lectures suivront les permissions de consultation existantes. La
configuration et l'import restent réservés à `administrator`. Les demandes
manuelles et changements de mode seront réservés à `dispatcher` et
`administrator`. `driver`, `viewer` et `sensor` n'obtiennent pas ce droit.

## Validation et recette à réaliser

| Lot ou ticket | Vérifications attendues |
| --- | --- |
| SIG-001 | Relecture du contrat, des exemples JSON, de la matrice et des cas inconnus |
| SIG-002/003 | Aspects invalides, limites, mapping, contexte annulé et pilotes sans capacité |
| SIG-004 | Aspects indépendants, délais/défauts, reset et coexistence avec commandes binaires |
| SIG-005/006 | Migration, doublons, listes vides, références, orientations, boucle et rollback |
| SIG-007 | Round-trip, archives anciennes, symboles orphelins et exclusion du runtime |
| Lots C/D | REST, WebSocket, concurrence, commandes obsolètes et repli après resynchronisation |
| SIG-019 | z21 + ATtiny, table réelle des codes, pertes réseau/DCC et état réel des feux |

Aucun de ces essais n'est déclaré exécuté par ce document. La recette
matérielle devra relever les firmwares, le câblage et la version du circuit.
Elle couvrira notamment un redémarrage avec canton occupé, les réponses
partielles, les défauts d'envoi et la reprise sans mouvement supplémentaire.

## Étude ultérieure : ABC et détection de courant

Le LEC000043 détecte le courant et actionne un relais. La documentation LECTIX
montre l'utilisation de ce relais avec un module de freinage séparé. La
production du signal ABC appartient notamment aux modules LEC001020/LEC001021
et exige un décodeur de locomotive compatible et configuré pour l'ABC.

Le 10819 remonte la détection de courant par R-BUS. La documentation consultée
ne suffit pas à valider l'association exacte ATtiny, 10819, LEC000043 et ABC.
Cette association reste une hypothèse de banc. Son étude devra vérifier la
détection d'un train arrêté sous ABC, les effets du montage sur l'asymétrie,
les transitions entre zones et la reprise de l'occupation après coupure.
Une occupation perdue ou inconnue ne devra jamais autoriser une ouverture.

L'ABC n'est donc ni une capacité du signal, ni une commande de freinage ajoutée
par les lots B à D. Sa validation électrique et les éventuelles règles
logicielles feront l'objet d'un travail distinct.

## Sources et suivi

- Ticket SIG-001 du lot B fourni dans l'archive de préparation
  `TrainPilot-Signalling-Tickets`, basée sur le commit `30da507`.
  Le ticket impose la validation des contrats avant développement.
- [Compatibilité des API](API_COMPATIBILITY.md).
- [Archives](ARCHIVE_FORMAT.md).
- [Occupation et validité des observations](OCCUPANCY.md).
- [Z21 LAN Protocol Specification, sections 5.4 à 5.6](https://www.z21.eu/media/Kwc_Basic_DownloadTag_Component/root-en-main_47-1652-959-downloadTag-download/default/d559b9cf/1699290380/z21-lan-protokoll-en.pdf).
- [LEC000043 : détection et exemple de relais commandant un freinage](https://lectix.fr/produit/detecteur-doccupation-dcc-par-consommation-de-courant-3a/).
- [LEC001021 : module de freinage ABC](https://lectix.fr/notices/LEC001021-notice-fr.pdf).
- [Roco 10819 : détection de courant et R-BUS](https://www.roco.cc/ren/products/control/10819-z21-detector-x16.html).
