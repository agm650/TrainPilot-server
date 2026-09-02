# Campagne aiguillages DCC-EX — AAAA-MM-JJ

## Identification

| Champ | Valeur |
| --- | --- |
| Date et opérateur | À renseigner |
| Version/commit TrainPilot | À renseigner |
| OS / machine | À renseigner |
| Centrale / carte DCC-EX | À renseigner |
| Firmware DCC-EX | À renseigner |
| Driver | `dccex` |
| Décodeur accessoire | À renseigner |
| Firmware décodeur | À renseigner ou inconnu |
| Actionneur / charge | À renseigner |
| `station.offlineAfter` | À renseigner |
| `station.accessoryPulse` | Sans effet pour le driver DCC-EX |
| `turnout.confirmationTimeout` | À renseigner |
| Références des logs/captures | À renseigner, sans secret |

## Câblage et adressage

| Endpoint | Adresse constructeur | Adresse TrainPilot | Trame `position1` | Trame `position2` | Inverted |
| --- | --- | ---: | --- | --- | --- |
| A | À renseigner | À renseigner | `<a N 0>` | `<a N 1>` | oui/non |
| B si composé | À renseigner | À renseigner | `<a N 0>` | `<a N 1>` | oui/non |

| Adresse testée | Sortie attendue | Sortie observée | Résultat |
| ---: | --- | --- | --- |
| 1 | À renseigner | À renseigner | NOT_TESTED |
| 4 | À renseigner | À renseigner | NOT_TESTED |
| 5 | À renseigner | À renseigner | NOT_TESTED |
| 8 | À renseigner | À renseigner | NOT_TESTED |
| 9 | À renseigner | À renseigner | NOT_TESTED |
| adresse du réseau | À renseigner | À renseigner | NOT_TESTED |

## Résultats

| Test | Attendu | Observé | Résultat |
| --- | --- | --- | --- |
| simple `position1` | `<a linear 0>`, bonne sortie | À renseigner | NOT_TESTED |
| simple `position2` | `<a linear 1>`, bonne sortie | À renseigner | NOT_TESTED |
| 20 allers-retours | aucun manque/doublon | À renseigner | NOT_TESTED |
| qualité après écriture | `assumed`, jamais `physical` inventé | À renseigner | NOT_TESTED |
| autre client `<a>` | support/absence de retour documenté | À renseigner | NOT_TESTED |
| coupure TCP | degraded puis offline | À renseigner | NOT_TESTED |
| reconnexion TCP | retour online sans redémarrer `dccd` | À renseigner | NOT_TESTED |
| commande pendant panne | refusée | À renseigner | NOT_TESTED |
| absence de rejeu | aucun mouvement pendant 20 s | À renseigner | NOT_TESTED |
| nouvelle commande | un mouvement | À renseigner | NOT_TESTED |
| triple électrique/mécanique | séquence sûre, état interdit absent | À renseigner | NOT_TESTED |
| TJD électrique/mécanique | toutes positions/transitions | À renseigner | NOT_TESTED |
| échec partiel | aucune confirmation physique inventée | À renseigner | NOT_TESTED |
| endurance | aucun manque/doublon/fuite observé | À renseigner | NOT_TESTED |

## Limites et incidents

- La qualité nominale attendue est `assumed` : à confirmer.
- Changement externe observable : à déterminer, sans supposition.
- Matériel non disponible : à renseigner.
- Incidents reproductibles : à renseigner.
- Corrections ou tickets associés : à renseigner.

## Conclusion

Statut global : `NOT_TESTED`

Justification et périmètre réellement validé : à renseigner.
