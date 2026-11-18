# Signalisation — spécification SIG-001

Version cible du serveur : **2.0.0**, avec les autres lots de signalisation.
Ce document fixe le contrat des lots B et C, validé le 18 novembre 2026,
et les décisions préparatoires du lot D.
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

Le firmware z21 du banc est déclaré à jour par l'utilisateur. Son numéro exact
devra figurer dans la recette matérielle. La version DCC-EX du banc est confirmée
à **5.6.6**. Le firmware de l'ATtiny et sa table de codes restent à consigner.
Ces déclarations ne constituent pas une validation matérielle des pilotes étendus.

## Décisions confirmées pour le lot D

Ces décisions encadrent SIG-014 à SIG-020. Les livraisons logicielles sont
décrites ci-dessous ; elles ne remplacent pas les recettes matérielles.

- **SIG-017 — Délai d'arrêt d'urgence :** objectif de **100 ms** côté serveur
  avant l'appel au pilote. Ce budget devra être réglable dans le fichier de
  configuration, sous forme d'une durée, avec **`100ms` par défaut**.
  La transmission et la réaction physique du matériel sont mesurées séparément.
- **SIG-016/017 — Rafraîchissement des aspects :** option configurable,
  **désactivée par défaut**. Son activation permettra de rafraîchir un aspect
  inchangé pour un décodeur disposant d'un watchdog adapté. Chaque émission
  devra respecter l'état courant et les générations de synchronisation.
  Cette option ne fournit pas de watchdog au décodeur et ne confirme pas les feux.
- **R-BUS — Recette après correction :** les essais Ethernet et OFF/ON DCC
  n'ont pas encore été refaits. L'utilisateur prévoit de les réaliser ; leurs
  résultats restent attendus. Le [compte rendu du lot A](hardware-tests/rbus/lot-A.md)
  reste la référence pour cette recette.
- **SIG-019 — Banc z21 :** aucun prototype ATtiny n'est disponible actuellement.
  Un montage sur breadboard est envisagé. La recette attend ce prototype,
  son adresse, sa table de codes et la revalidation matérielle R-BUS.
- **SIG-020 — Banc DCC-EX :** **reporté à la demande de l'utilisateur**.
  La détection d'occupation DCC-EX n'est pas encore disponible sur son banc.
  Ce ticket ne doit pas être entrepris dans la prochaine phase du lot D.

Les tickets logiciels **SIG-014 à SIG-018 peuvent avancer**. Les recettes
matérielles ne seront pas déclarées réussies à partir des seuls tests simulés.
Le report de SIG-020 ne retire pas le support logiciel DCC-EX livré par SIG-013.

## État de l'implémentation

SIG-017 borne la file à **64 commandes en attente**, avec un seul worker.
Une cible plus récente remplace celle du même signal. Un repli passe avant
les ouvertures et peut évincer une ouverture en attente si la file est pleine.
La cible évincée revient au repli. Les signaux restants sont repris par la boucle
de calcul ; la saturation ne crée pas de goroutines par signal.
Le gate existant conserve la sérialisation des écritures : sécurité générale,
puis replis des signaux, puis commandes ordinaires. Une commande de sécurité
annule l'I/O d'un signal en cours. Les pilotes doivent respecter cette annulation.
Les lectures SQLite et la réception des retours ne tiennent pas le gate pendant
l'I/O. Un paquet déjà parti ne peut pas être retiré ; une correction restrictive
est envoyée dès libération du transport. L'arrêt d'urgence interdit toute
ouverture jusqu'à son réarmement explicite par remise sous tension.

`signalling.safetyDispatchBudget` est une durée **strictement positive**, avec
**`"100ms"` par défaut**. C'est l'objectif mesuré entre la prise en charge serveur
et l'appel au pilote pour l'arrêt d'urgence ou OFF. Le serveur n'abandonne jamais
une commande de sécurité si ce budget est dépassé. Les métriques privées
`trainpilot_safety_dispatch_seconds` et
`trainpilot_safety_dispatch_over_budget_total` permettent de le contrôler.
Ce budget ne couvre ni la transmission DCC ni la réaction physique des feux.
Le système d'exploitation et un pilote ne respectant pas l'annulation peuvent
le dépasser ; les essais logiciels ne constituent pas une garantie temps réel.

SIG-016 impose le repli après erreur et avant une nouvelle ouverture.
Les tentatives de repli sont espacées de 250 ms, puis doublées jusqu'à 2 s.
Le succès efface l'erreur ; l'automatique recalcule ensuite avec les données
courantes. L'import efface les observations et redemande les détecteurs aux
pilotes compatibles. Les reprises partielles restent restrictives par canton.
Un retour courant inconnu ou contradictoire hors commande en cours déclenche
une resynchronisation du signal. Un décodeur silencieux ne permet pas de détecter
son redémarrage. Sans retour, aucune confirmation physique n'est annoncée.

`signalling.refreshInterval` est une durée du fichier JSON : **`"0s"` par
défaut**, option désactivée. Exemple : `"1s"`. Les valeurs négatives sont refusées.
Le contrôle périodique a une résolution de 100 ms. Chaque réémission vérifie la
puissance, la génération et l'occupation ; elle ne réactive pas une ancienne
ouverture manuelle. Un watchdog local, capable d'afficher le repli en l'absence
de paquets, doit être implémenté et testé dans le décodeur. Cette option ne
garantit ni la réception des paquets ni la couleur physique des feux.

SIG-015 active l'automatique pour les profils `bal-3`. Le calcul utilise une
vue atomique de toutes les occupations. Une transition simultanée entre cantons
ne produit pas de vue intermédiaire libre. Les événements répétés sont
dédupliqués ; les boucles convergent sans réémission continue. Chaque résultat
expose `calculationRevision` et `occupancyRevision`. Le serveur vérifie à nouveau
la génération et l'occupation avant l'envoi. HTTP passe à **1.20.0**, les
événements à **1.17.0**. Les protections de reprise et de priorité sont
complétées par SIG-016 et SIG-017 avant les recettes matérielles.

SIG-014 fournit `CalculateBAL`, une fonction pure sans horloge, transport ni
persistance. Elle utilise les rôles des profils et n'impose pas les identifiants
SNCF. Un canton occupé ou inconnu reste au repli. Un terminal explicite restrictif
donne l'avertissement si le canton protégé est libre. Une voie libre exige un
aval non restrictif déjà commandé, fiable dans la génération courante et sans
nouvelle condition de repli. L'absence de retour physique n'est pas transformée
en confirmation. Un aval manuel fiable est utilisable par le calcul amont.
Deux passes bornées traitent également les boucles, sans récursion ni dépendance
à l'ordre des signaux. Les entrées du calcul restent inchangées.
L'intégration événementielle est livrée par SIG-015.

SIG-013 implémente `<A adresse code>` pour DCC-EX. Le pilote interroge l'identité
et la puissance avec `<s>` au démarrage et après reconnexion réseau. Il annonce
le support étendu seulement après une identité **5.6.6**, version vérifiée dans
le code officiel. Les autres versions gardent les fonctions existantes mais
n'annoncent pas ce nouveau support sans vérification supplémentaire.
La plage annoncée est `1..2040` pour l'adresse et **0..31** pour le code d'aspect.
Le firmware applique lui-même le décalage RCN-213 ; le serveur ne l'ajoute pas.
Les codes 32..255 restent valides dans une configuration générique mais sont
refusés par ce pilote. Il ne les tronque ni ne les remappe implicitement.
Un signal dont le repli dépasse les capacités du pilote reste inhibé sous
`unsupported_endpoint` ; une ouverture ne peut pas contourner cette restriction.
Un aspect demandé hors limites est refusé avant de modifier la cible.
Les écritures étendues ont un délai maximal de deux secondes et respectent
l'annulation, y compris pendant l'attente d'une autre écriture.
Le protocole ne fournit pas d'acquittement corrélé pour cette commande : seul
`commandedAspect` est actualisé, `reportedAspect` reste nul sans autre source.
Les retours de puissance `MAIN`, `JOIN` ou globaux alimentent le service sans
client connecté. Un retour `PROG` seul n'autorise aucune ouverture. Un arrêt
par voie est conservativement inconnu jusqu'au retour agrégé `MAIN`.
Les générations réseau/puissance invalident les anciennes cibles et les
observations utilisées avant une nouvelle ouverture. Une reprise de puissance
est traitée avant d'autoriser l'envoi du repli sur le flux renouvelé.
Les tests TCP et le test de démarrage du serveur sans client ne constituent pas
une recette matérielle. L'essai physique SIG-020 est reporté selon les décisions
du lot D ci-dessus.

SIG-012 implémente les accessoires étendus dans le pilote z21. La découverte
`LAN_X_GET_FIRMWARE_VERSION` intervient après les requêtes R-BUS initiales.
Sans réponse BCD valide indiquant un firmware au moins égal à 1.40, le pilote
n'annonce pas `extendedAccessoryControl`. La découverte est renouvelée après
changement de connexion ou de génération de puissance, sans désactiver R-BUS.
Le pilote envoie `LAN_X_SET_EXT_ACCESSORY`, puis demande un retour de la dernière
commande par `LAN_X_GET_EXT_ACCESSORY_INFO`. Il n'attend pas d'acquittement
physique. Sans réponse, `reportedAspect` reste inconnu. Les retours connus,
inconnus ou invalides conservent la qualité `station`. Leur canal de 64 entrées
est best-effort et conserve les retours les plus récents en cas de saturation.
Les retours des anciennes connexions sont écartés. Les tests UDP ne remplacent
pas la recette physique SIG-019, qui reste à réaliser.

SIG-011 ajoute `signals` et `signalProfiles` au snapshot WebSocket, toujours
sous forme de tableaux. Les événements sont `signal.aspect.changed` (état
opérationnel complet), `signal.commanded`, `signal.command.failed` et
`signal.mode.changed`. Les états identiques sont dédupliqués. Deux demandes
qui produisent réellement deux écritures conservent deux événements de commande.
Les erreurs et annulations n'annoncent aucune confirmation physique. Les
séquences et la récupération par `client.snapshot_request` utilisent le bus
existant. Son abonnement interne filtré évite les rechargements dus au trafic
des signaux eux-mêmes. Le contrat événements est **1.16.0**.

SIG-010 expose les signaux sous `/api/v1/signals` et les profils sous
`/api/v1/signal-profiles`. Un signal lu contient `definition` et `state`.
Les lectures exigent la permission `view`. Les commandes `PUT /signals/{id}/aspect`
avec `{"aspect":"S"}` et `PUT /signals/{id}/mode` avec `{"mode":"manual"}`
exigent la permission `dispatch`. Elles renvoient 204 après acceptation.
L'activation automatique reste refusée en 409 `signal_automatic_unavailable`.
Un canton non libre donne 409 `signal_unsafe`, une indisponibilité donne
503 `signal_unavailable`. `system/info.signalling` distingue la disponibilité
du service des capacités du pilote. Le contrat HTTP est **1.19.0**.
Les imports passent par une barrière qui annule les anciennes commandes avant
la transaction et recharge les définitions avant l'accusé de réception HTTP.

SIG-009 fournit les commandes manuelles d'aspect et de mode aux rôles
`dispatcher` et `administrator`. Tout aspect autre que le rôle `stop` exige
un canton protégé connu libre, y compris un aspect personnalisé de rôle `other`.
Ces conditions sont revérifiées après l'attente du transport. Une commande
restrictive annule une ouverture en cours ; une commande ordinaire ne remplace
pas une commande déjà en attente. Une occupation ou une perte de validité
ramène la cible au repli sans mémoriser d'ouverture à rejouer.
Les tentatives acceptées et les résultats, refus inclus, sont audités avec
l'acteur et un code de résultat. Aucun détail de transport sensible n'est stocké.
Le mode est une configuration persistante ; sa modification ne restaure aucun
aspect opérationnel. L'activation automatique renvoie une erreur explicite
jusqu'à SIG-014. La dernière erreur reste visible après un repli réussi,
jusqu'à une nouvelle demande manuelle ou une réinitialisation.

SIG-008 fournit `SignalService`, démarré et arrêté avec le serveur. Il charge
les profils et définitions dans un même snapshot SQLite. Son registre expose
des copies cohérentes, sans persister d'aspect opérationnel. Le service envoie
le repli sans client connecté au démarrage, après import et après reprise
réseau/DCC. Les commandes ont un délai maximal de deux secondes. Une invalidation
annule les commandes en cours et écarte leurs résultats et retours obsolètes.
Les erreurs restent visibles dans `error`, avec un code et un message génériques.
`inhibitedReason` explique une indisponibilité ou l'absence du moteur automatique.
Les contrats REST et WebSocket de ces états sont livrés par SIG-010/011.

SIG-001 est validé avec les précisions de généricité ci-dessus. SIG-002 fournit
les types et validations dans [signal.go](../internal/model/signal.go) et
[signal_profile.go](../internal/model/signal_profile.go). Le profil SNCF est
un [preset séparé](../internal/model/signal_profile_bal.go).

Les tests couvrent aussi un profil personnalisé, des codes matériels
arbitraires, plusieurs cibles, neuf feux, l'extinction et les indications
combinées. Le modèle représente `flashing` sans exécuter de clignotement.
Un état opérationnel neuf utilise le repli du profil et n'a aucun retour
confirmé. Leur persistance et leur lecture HTTP sont maintenant livrées par SIG-005 à SIG-007.

Le modèle valide la forme des références ; leur existence et leur continuité
orientée dans le circuit sont vérifiées par SIG-006. Le jeu `bal-3` est
identifié et validé dans les profils, mais son calcul reste prévu par SIG-014.

SIG-003 fournit l'interface optionnelle `station.ExtendedAccessoryController`,
la commande validée et `SendExtendedAccessory`. Les limites portables sont
`1..2040` et `0..255`. Un pilote peut annoncer des limites plus étroites dans
`extendedAccessoryLimits`. Sans cette propriété, les limites portables
s'appliquent. Sans `extendedAccessoryControl: true`, la commande est refusée.
L'interface et la capacité doivent toutes deux être présentes. Les pilotes
z21 n'annonce le support étendu qu'après découverte d'un firmware compatible.
DCC-EX exige une identité de firmware vérifiée avant d'annoncer le support étendu.
SIG-003 introduit le contrat HTTP 1.16.0 et le contrat événements 1.14.0.
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

SIG-006 valide ces références dans la transaction d'import et lors des mises
à jour de signaux ou de profils. Les définitions et profils sont exposés par
la topologie. Depuis SIG-007, leur révision `signallingRevision` est distincte
du checksum physique historique, pour préserver les clients existants. Une configuration
vide conserve le comportement et la révision historiques. Les collections
de configuration sont ordonnées par identifiant pour le calcul de révision.
`UnsignalledBlockIDs` identifie les cantons sans signal déclaré, sans en
déduire une occupation ou une autorité de mouvement.

Le premier jalon vérifie la continuité sur les sections du canton protégé.
Un chemin aval nécessitant une commutation d'aiguillage n'est pas accepté
comme une continuité BAL établie ; les règles d'itinéraire restent hors périmètre.

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

Pour le lot C, l'activation du mode automatique sera refusée tant que le moteur
SIG-014 du lot D n'est pas disponible. Les définitions déjà configurées en
automatique seront conservées. Leur état restera au repli, avec un motif
explicite indiquant l'indisponibilité du moteur.

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

Le lot C devra envoyer l'aspect de repli configuré au démarrage, après import
et après reconnexion ou rétablissement de l'alimentation. Cet envoi sera
indépendant des clients connectés. Il attendra une centrale `online`, une
alimentation connue active et l'absence de défaut électrique, avec un endpoint
valide et supporté. Il n'exigera pas un canton libre pour envoyer un aspect
de rôle `stop`. Pendant une coupure, le repli restera une cible logique.
Une écriture réussie ne confirmera jamais à elle seule l'état physique des feux.

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

### Prérequis firmware z21 pour SIG-012

La commande des signaux étendus exige un firmware z21 **1.40 ou supérieur**.
Cette exigence provient de la
[spécification officielle, sections 5.4 à 5.6](https://www.z21.eu/media/Kwc_Basic_DownloadTag_Component/root-en-main_47-1652-959-downloadTag-download/default/d559b9cf/1699290380/z21-lan-protokoll-en.pdf).
Un firmware antérieur ne doit pas être annoncé compatible avec cette capacité.
Ce prérequis concerne les accessoires étendus et n'ajoute pas de restriction
aux fonctionnalités R-BUS ou accessoires binaires existantes.

L'adresse publique `1` correspond à l'adresse brute `4`.
Cette conversion ne réutilise pas celle des aiguillages binaires. Le retour
z21 rapporte la commande mémorisée par la centrale, sans prouver l'état des
feux. Son statut inconnu reste inconnu. L'intégration z21 appartient à SIG-012.

Le simulateur implémentera le contrat avec des états séparés par adresse,
des défauts et délais déterministes, un journal et un reset. Ses observations
représentent un banc simulé, jamais une preuve de fonctionnement matériel.
Le pilote DCC-EX est livré par SIG-013 ; sa recette matérielle reste SIG-020.

## Persistance, archives et contrats clients

SIG-005 persiste les définitions dans `signal_profiles` et `signals`. Les
profils sont stockés sous forme JSON normalisée. Les colonnes relationnelles
portent les références et l'unicité des endpoints. Les clés étrangères aval
sont différées pour permettre les boucles lors d'un remplacement transactionnel.
Une modification de profil qui invalide un signal existant est refusée. Les
listes vides sont non nulles. Les tables ne contiennent aucun état runtime.

- Stocker les définitions, profils configurables, politiques et références.
- Ne jamais restaurer de commande en attente ou d'aspect physique confirmé.
- Migrer les bases existantes sans perdre le circuit ni l'occupation configurée.
- Produire des listes vides JSON `[]` et des révisions déterministes.
- Valider les références et remplacements dans la transaction d'import existante.
- Conserver une révision de présentation indépendante des états opérationnels.
- Importer les archives sans signaux comme des circuits sans signalisation.

Le format de circuit courant est **9** depuis SIG-007 pour
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

Les tests logiciels des fondations sont exécutés à chaque commit dédié.
Les recettes des lots C/D et les essais matériels restent à réaliser. La recette
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
- [DCC-EX 5.6.6 : syntaxe des commandes et identité](https://github.com/DCC-EX/CommandStation-EX/blob/v5.6.6-Prod/DCCEXParser.cpp).
- [DCC-EX 5.6.6 : encodage, décalage d'adresse et limite à cinq bits](https://github.com/DCC-EX/CommandStation-EX/blob/v5.6.6-Prod/DCC.cpp).
- [DCC-EX 5.6.6 : retours de puissance](https://github.com/DCC-EX/CommandStation-EX/blob/v5.6.6-Prod/CommandDistributor.cpp).
- [LEC000043 : détection et exemple de relais commandant un freinage](https://lectix.fr/produit/detecteur-doccupation-dcc-par-consommation-de-courant-3a/).
- [LEC001021 : module de freinage ABC](https://lectix.fr/notices/LEC001021-notice-fr.pdf).
- [Roco 10819 : détection de courant et R-BUS](https://www.roco.cc/ren/products/control/10819-z21-detector-x16.html).


## Livraison SIG-007

Le format 9 conserve les profils personnalisés, définitions, politiques,
orientations et codes matériels, ainsi que les symboles graphiques. Les champs
runtime sont exclus. Le format 7 du matériel roulant ne change pas.
La présentation HTTP 1.18 expose `signals` (placements optionnels) sans
modifier la topologie logique. Une suppression de signal supprime son symbole
si elle réussit ; un échec de référence annule aussi cette suppression graphique.
Un remplacement de définitions conservant un identifiant préserve son symbole.
Les mises à jour de présentation seules ne changent pas la révision topologique.


La version HTTP finale du lot B est **1.18.0**, celle des événements **1.15.0**.
`signallingRevision` est un champ optionnel de la topologie et du snapshot.
Il change lors d'une modification des profils ou définitions. `topologyRevision`
conserve son calcul physique historique, y compris pour les clients qui ignorent
les nouveaux champs. Le client récent suit les deux clés pour sa lecture de
`GET /api/v1/topology`. La révision de présentation reste indépendante.

## Validation du lot B — 18 novembre 2026

| Ticket | Suite standard avant commit | Contrôles complémentaires |
| --- | --- | --- |
| SIG-003 | 1 158 tests réussis | `go vet`, YAML et références locales |
| SIG-004 | 1 166 tests réussis | Suite complète `-race`, `go vet` |
| SIG-005 | 1 168 tests réussis | Migration, redémarrage, rollback, `go vet` |
| SIG-006 | 1 187 tests réussis | REST/WS, cycles, références, `go vet`, YAML |
| SIG-007 et intégration finale | 1 200 tests réussis | Suite complète `-race`, suite `CGO_ENABLED=0`, `go vet`, YAML |

Les différences et le formatage Go sont vérifiés. Les cinq fixtures de circuit
ont été régénérées au format 9 ; les archives de matériel roulant sont inchangées.
Les quatre exemples JSON du présent document et ses liens locaux sont valides.
Ces contrôles utilisent des simulateurs et des sockets locaux. Aucun essai
physique z21, ATtiny, LEC000043 ou ABC n'a été exécuté dans le cadre du lot B.

## Livraison et validation du lot C

SIG-008 à SIG-013 sont livrés pour la future version serveur **2.0.0**.
Chaque ticket possède un commit dédié et une entrée dans le CHANGELOG.

| Ticket | Suite complète `-race` réussie |
| --- | --- |
| SIG-008 | 1 206 tests |
| SIG-009 | 1 211 tests |
| SIG-010 | 1 215 tests |
| SIG-011 | 1 221 tests |
| SIG-012 | 1 230 tests |
| SIG-013 et intégration finale | 1 242 tests |

La suite standard et la suite `CGO_ENABLED=0` réussissent également avec
**1 242 tests dans 32 packages**. `go vet`, le formatage, les différences Git
et les références YAML sont vérifiés. Les contrats finaux du lot C sont
HTTP **1.19.0** et événements **1.16.0**. Les formats d'archives restent
layout **9** et matériel roulant **7**.

Les capacités matérielles sont découvertes après connexion. Un client doit
actualiser `system/info` après reprise avant de se fier à leur valeur en cache.
Le serveur vérifie toujours les capacités courantes avant chaque commande.
L'activation automatique reste refusée jusqu'au lot D. Les contrôles de ce lot
utilisent des simulateurs TCP/UDP et ne valident pas les feux réels.
La recette SIG-019 attend le prototype et les essais R-BUS. SIG-020 est reporté
tant que la détection DCC-EX du banc n'est pas disponible.
