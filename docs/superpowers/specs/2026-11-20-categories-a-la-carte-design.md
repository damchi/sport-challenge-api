# Catégories à la carte, sport de référence et catégorie Points

Conception du 2026-11-20, décidée avec le propriétaire du produit. À reporter dans le README (§2.1, §2.3, §2.4, §5.4) avant le code, puis à découper en tickets.

## Pourquoi

Les challenges sont aujourd'hui centrés sur la course : 3 catégories imposées, coefficients exprimés par rapport à la course, élimination réservée à la course. On veut pouvoir lancer un challenge vélo, natation, aviron, etc., choisir ses catégories, et ajouter une catégorie à points qui récompense des performances hebdomadaires variées.

## Objectifs

- Un challenge a toujours la catégorie **temps cumulé** ; les autres sont optionnelles.
- Le créateur choisit un **sport de référence** (n'importe quel sport à distance du catalogue).
- Nouvelle catégorie **Points** à barème dégressif, façon F1.

## Non-objectifs

- Duplication de challenge et récap de semaine : chantiers séparés, conçus après celui-ci.
- Aucune reprise de données : rien n'est en production ; les challenges existants en base locale sont traités comme « référence = course ».
- Pas de nouveau type de catégorie au-delà de ceux décrits ici (régularité, objectif collectif… plus tard).

## Anti-objectifs

- Stocker des points ou des totaux comme source de vérité.
- Créer un bonus pour les sports plus lents que la référence (pas de conversion stricte).

## Règles produit décidées

### Catégories

| Catégorie | Présence | Activités | Classement |
|---|---|---|---|
| Temps cumulé | **toujours** | tous les sports | plus de temps cumulé |
| Distance cumulée | optionnelle | sports à distance, ramenés au sport de référence | plus de distance équivalente |
| Élimination | optionnelle | sport de référence seul, km réels | règle actuelle de la catégorie 3, inchangée |
| Points | optionnelle | voir plus bas | plus de points |

- Toutes les combinaisons sont permises ; un challenge peut n'avoir que le temps cumulé.
- Catégories, référence et paramètres sont **figés à la création**, comme aujourd'hui.

### Sport de référence

- Choisi à la création parmi les sports du catalogue dont la distance est obligatoire (`distance_mode = required`) ; **course par défaut**.
- Coefficient suggéré d'un sport dans un challenge = `min(1, facteur_sport / facteur_référence)`, arrondi à deux décimales (la moitié vers le haut) et jamais sous 0,1, les facteurs du catalogue restant exprimés par rapport à la course. Un sport plus lent que la référence compte donc 1 pour 1 (généralisation de la règle actuelle). Exemple, référence vélo : vélo ×1, course ×1, aviron ×1.
- Le créateur peut modifier chaque coefficient entre 0,1 et 2 ; le sport de référence vaut toujours ×1 et ne s'envoie pas (comme `run` aujourd'hui).
- L'incrément X de l'élimination reste en km, sur la distance réelle du sport de référence.

### Catégorie Points

- Le créateur coche les critères (au moins un) :
  1. **séance la plus longue en temps** (tous les sports) ;
  2. **séance la plus longue en distance équivalente** (coefficients du challenge, comme la distance cumulée) ;
  3. **nombre de séances** de la semaine, une séance ne comptant que si sa durée atteint un **minimum fixé par le créateur** (pré-rempli à 20 min).
- Chaque critère donne, chaque semaine, un mini-classement ; les points vont au **top 5** (10-8-6-4-2) ou au **top 10** (25-18-15-12-10-8-6-4-2-1), au choix du créateur.
- Points attribués au **verrouillage de la semaine** (7 h, heure locale, le premier jour de la semaine suivante), semaine 1 comprise.
- Pendant une semaine non verrouillée, le classement montre à part les **points provisoires** de cette semaine (même calcul, sur les activités du moment), en plus des points acquis ; le rang se calcule sur les points acquis seuls. Entre la fin d'une semaine et son verrouillage, ses points restent provisoires.
- Ex aequo dans un mini-classement : même rang, mêmes points (1, 1, 3), y compris à la limite du top (trois ex aequo au 5ᵉ rang reçoivent chacun les points du 5ᵉ) ; choix du concepteur.
- Les coefficients du challenge existent dès que la distance cumulée **ou** le critère de distance est choisi ; choix du concepteur.
- Une semaine sans activité pour un critère rapporte 0 point sur ce critère.
- Classement final : total des points ; ex aequo (1, 1, 3).
- Abandon ou exclusion : la personne **reste** dans les mini-classements des semaines verrouillées avant son départ (les points de ces semaines ne bougent pas), sort de ceux des semaines verrouillées ensuite, et apparaît « a abandonné », sans rang ni total.
- Compte supprimé avant le gel des résultats : retiré de tous les mini-classements, semaines verrouillées comprises (ses activités sont effacées) ; les suivants récupèrent ses places. Après le gel, seuls les rangs se recalculent.

## Approches considérées

- **A. Points recalculés à la lecture (retenue)** : fonction pure dans `internal/scoring` sur les semaines verrouillées. Conforme à « toujours recalculé depuis les activités brutes » ; la suppression d'un compte fait remonter les autres sans réécriture.
- **B. Points stockés à la clôture** (table remplie par `close-weeks`) : lectures plus simples, mais total stocké comme source de vérité et réécriture obligée à la suppression d'un compte (contraire au §2.2). Écartée.

Pour le modèle des catégories, une seule voie sensée : la table `categories` (type + `params jsonb`) accueille les nouveaux types, `run_elimination` devient `elimination` (sport en paramètre), le challenge porte `reference_sport`.

## Ce que ça touche

- **Migrations** (nouvelles, jamais de modification des existantes) : `challenges.reference_sport` ; contraintes `categories_type_check` et `categories_position_check` (1 à 4) ; types d'`elimination` et `result_snapshot` (contraintes figées sur `run_elimination`).
- **OpenAPI** : `ChallengeCreate` (référence, catégories choisies et leurs paramètres), `CategoryParams` (référence, critères, barème, durée minimale), descriptions « 3 catégories » de la création, du classement et de « Mes défis ».
- **`internal/scoring`** : coefficient suggéré relatif à la référence ; élimination paramétrée par sport ; nouveau scorer Points (tests d'abord).
- **`internal/challenge`** : validation de la création (temps toujours présent, au moins un critère, barème connu, durée minimale bornée).
- **`internal/leaderboard`, « Mes défis », `internal/closure`** : itérer sur les catégories présentes ; ne juger l'élimination que si elle existe ; figer aussi la catégorie Points dans `result_snapshot`.

## Laissé au constructeur

- Bornes techniques de la durée minimale (proposition : 1 à 180 min).
- Ordre d'affichage des catégories (proposition : temps, distance, élimination, points).
- Forme du `payload` figé de la catégorie Points.
