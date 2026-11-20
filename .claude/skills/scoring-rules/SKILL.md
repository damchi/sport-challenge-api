---
name: scoring-rules
description: Règles de classement, catégories, fuseau horaire, semaines, élimination, verrouillage des semaines et ex aequo du backend Sport Challenge. À utiliser pour créer ou modifier un type de catégorie, le calcul d'un classement, une clôture de semaine ou toute règle qui dépend du temps.
---

# Règles de classement et de temps

Source de vérité : `README.md` §2.3 à §2.6. Le package `internal/scoring` est du **Go pur** (bibliothèque standard seulement) : il ne connaît ni la base, ni Gin, ni GORM.

## Les 3 catégories

| # | Catégorie | Sports comptés | Valeur classée |
|---|---|---|---|
| 1 | Temps cumulé | tous | secondes cumulées |
| 2 | Distance cumulée | sports avec km, avec coefficients | mètres équivalents cumulés |
| 3 | Course avec élimination | course seule | mètres cumulés, survivants d'abord (voir plus bas) |

Il n'y a **pas** de catégorie « course cumulée » séparée.

Coefficients **suggérés** par le catalogue (`sports.suggested_factor`) : course ×1 ; marche, randonnée, natation et autres sports à km plus lents ×1 ; vélo ×0,3 ; aviron, roller, ski de fond ×0,5 ; « autre » ×1 si une distance est saisie (D9). Le créateur choisit ceux de son challenge à la création (0,1 à 2, deux décimales), sauf la course, référence toujours ×1 (README §2.4). Ils sont ensuite **figés** dans les paramètres de la catégorie 2 ; le scorer lit ces paramètres, jamais le catalogue.

## Fenêtre et semaines

- Début = premier instant du premier jour local (minuit, ou la fin de l'heure sautée si un changement d'heure saute minuit : `scoring.StartOfDay`), entre aujourd'hui et un an plus tard ; fin = début + N × 7 jours **locaux**, avec N entier de 2 à 52 : pas de semaine partielle. Le tout dans le **fuseau du challenge**, stocké en UTC (`challenge/rules.go`).
- Une activité compte si `started_at ∈ [début, fin)`.
- Semaines : la semaine n commence à `scoring.StartOfDay` de la date locale de début + 7 × (n-1) jours (`Window.WeekBounds`), calculée depuis la **date** et non depuis l'instant de début (qui peut être 01:00). **Jamais** `7*24*time.Hour` (changement d'heure). Toutes les semaines sont complètes.

## Élimination

- **X** = incrément en km, paramètre de la catégorie (défini par le créateur). Règle : `distance(s+1) >= distance(s) + X`, « au minimum » (✅). Sinon, éliminé de la catégorie au verrouillage de la semaine.
- ✅ `distance(s)` = distance **réellement courue** en semaine s (cumul de toutes les courses de la semaine, pas une seule sortie). **Si elle dépasse le minimum, elle devient la nouvelle base** : `minimum(s+1) = distance(s) + X`. Exemple X = 2 : 5 km en semaine 1 → minimum 7 en semaine 2 ; si 10 km courus → minimum 12 en semaine 3. Il n'y a **pas** de calendrier fixe.
- ✅ Conséquences : pas de distance de départ, **aucune élimination en semaine 1**, et `minimum(s+1)` est **définitif** au verrouillage de la semaine s ; avant, il est provisoire.
- ✅ Une semaine se **verrouille à 7 h, heure locale du challenge, le premier jour de la semaine suivante** (la dernière : le premier jour après le challenge, le jour de `ends_at`), au premier instant qui existe si un changement d'heure saute ou répète 7 h. Seule `scoring.Window.WeekLocksAt` porte cette règle. Jusque-là, ses activités s'ajoutent, se modifient et se suppriment ; ensuite, plus rien (`week_locked`). Les bornes des semaines ne bougent pas : une activité compte entièrement pour la semaine de son début.
- Une semaine est jugée à **son verrouillage** (`Window.LockedWeeks`), jamais avant : délai de grâce jusqu'à 7 h locale (6 à 8 heures selon les changements d'heure). Entre sa fin et 7 h, la semaine affichée est la suivante, avec un minimum provisoire. Le résultat est déterministe : il ne dépend pas du moment où le worker tourne (seul le statut d'abandon est lu au passage). Les résultats sont figés au verrouillage de la dernière semaine.
- Éliminé = éliminé de **cette catégorie seulement**, définitivement.
- Statut en cours de semaine : « objectif atteint », « en cours » ou « à risque ». Jamais d'élimination avant la fin de la semaine.
- Classement : les personnes qui terminent sans élimination d'abord, par km cumulés, **quelle que soit leur base de départ** (✅) ; puis les éliminés, dernier éliminé devant, puis plus de km cumulés en cas d'égalité sur la semaine d'élimination (✅). Si la distance est aussi égale : ex aequo.

## Égalités

Ex aequo : 1, 1, 3. Pas de départage arbitraire.

## Participation

Un participant est classé dans **toutes** les catégories (les 3). Un participant qui quitte est éliminé partout, définitivement, et n'est plus éligible au podium (affiché « a abandonné »).

## Ajouter ou modifier un type de catégorie

1. **Tests d'abord** dans `internal/scoring` : cas normal, égalités, activité hors fenêtre, début et fin de fenêtre exacts, **changement d'heure** (mars et novembre, par exemple `America/Toronto`), instant exact de fin de semaine (dernière seconde incluse, seconde suivante exclue), instant exact du verrouillage (7 h, y compris un changement d'heure), participant sans activité. Pour la catégorie 3 : exactement le minimum (non éliminé), un cran en dessous (éliminé), dépassement du minimum (nouvelle base), semaine 1 sans élimination, semaine sans course, plusieurs courses dans la même semaine, activité datée d'une semaine verrouillée refusée, deux personnes avec des bases différentes qui finissent toutes les deux (le plus de km gagne).
2. Implémente `Scorer` (activités + participants + fenêtre → entrées classées). Pas de `time.Now()` : la fenêtre porte l'instant courant fourni par l'horloge.
3. Valide les paramètres de la catégorie dans `challenge/rules.go`.
4. Branche-le dans `leaderboard` (sélection du scorer par type) et, si la règle évolue dans le temps, dans `closure`.
5. Mets à jour le `README.md` (§2.3) **seulement** si l'utilisateur a validé le changement de règle.
6. Agents `spec-guardian` puis `go-reviewer`.

## Pièges

- Ne stocke jamais un classement comme source de vérité : recalcul depuis les activités brutes. Seul `result_snapshot` fige les résultats, au verrouillage de la dernière semaine.
- Les tâches de clôture tournent toutes les heures et sont **idempotentes** : relancer ne doit rien changer si le travail est fait.
- Une semaine verrouillée l'est pour de bon : l'élimination qui en découle ne change plus ; au verrouillage de la dernière semaine, plus rien ne bouge.
