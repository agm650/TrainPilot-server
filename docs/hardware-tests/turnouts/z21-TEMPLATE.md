# Campagne aiguillages z21 — AAAA-MM-JJ

## Identification

| Champ | Valeur |
| --- | --- |
| Date et opérateur | À renseigner |
| Version/commit TrainPilot | À renseigner |
| OS / machine | À renseigner |
| Centrale et couleur | À renseigner |
| Firmware centrale | À renseigner |
| Driver | `z21` |
| Décodeur accessoire | À renseigner |
| Firmware décodeur | À renseigner ou inconnu |
| Actionneur / charge | À renseigner |
| `station.offlineAfter` | À renseigner |
| `station.accessoryPulse` | À renseigner |
| `turnout.confirmationTimeout` | À renseigner |
| Références des logs/captures | À renseigner, sans secret |

## Câblage et adressage

| Endpoint | Adresse constructeur | Adresse TrainPilot | `position1` | `position2` | Inverted |
| --- | --- | ---: | --- | --- | --- |
| A | À renseigner | À renseigner | À renseigner | À renseigner | oui/non |
| B si composé | À renseigner | À renseigner | À renseigner | À renseigner | oui/non |

| Frontière | Adresse constructeur | Adresse TrainPilot | Sortie observée | Résultat |
| --- | --- | ---: | --- | --- |
| référence | À renseigner | 1 | À renseigner | NOT_TESTED |
| fin groupe | À renseigner | 4 | À renseigner | NOT_TESTED |
| groupe suivant | À renseigner | 5 | À renseigner | NOT_TESTED |
| fin groupe | À renseigner | 8 | À renseigner | NOT_TESTED |
| groupe suivant | À renseigner | 9 | À renseigner | NOT_TESTED |

## Résultats

| Test | Attendu | Observé | Résultat |
| --- | --- | --- | --- |
| simple `position1` | bonne sortie, une impulsion | À renseigner | NOT_TESTED |
| simple `position2` | bonne sortie, une impulsion | À renseigner | NOT_TESTED |
| 20 allers-retours | aucun manque/doublon | À renseigner | NOT_TESTED |
| pulse `50ms` | activation puis désactivation sûre | À renseigner | NOT_TESTED |
| pulse `100ms` | activation puis désactivation sûre | À renseigner | NOT_TESTED |
| pulse `150ms` | activation puis désactivation sûre | À renseigner | NOT_TESTED |
| retour z21 | `reportedPosition`, qualité `station` | À renseigner | NOT_TESTED |
| changement externe | état rapporté, aucun ordre correctif | À renseigner | NOT_TESTED |
| coupure/reconnexion | offline puis online | À renseigner | NOT_TESTED |
| commande pendant panne | refusée | À renseigner | NOT_TESTED |
| absence de rejeu | aucun mouvement pendant 20 s | À renseigner | NOT_TESTED |
| nouvelle commande | un mouvement | À renseigner | NOT_TESTED |
| triple électrique/mécanique | séquence sûre, état interdit absent | À renseigner | NOT_TESTED |
| TJD électrique/mécanique | toutes positions/transitions | À renseigner | NOT_TESTED |
| échec partiel | aucun faux `physical` | À renseigner | NOT_TESTED |
| endurance | aucun manque/doublon/fuite observé | À renseigner | NOT_TESTED |

## Limites et incidents

- Retour `station` distinct d'une confirmation mécanique : à confirmer.
- Matériel non disponible : à renseigner.
- Incidents reproductibles : à renseigner.
- Corrections ou tickets associés : à renseigner.

## Conclusion

Statut global : `NOT_TESTED`

Justification et périmètre réellement validé : à renseigner.
