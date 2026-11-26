---
name: scoring-rules
description: Règles de classement, catégories, fuseau horaire, semaines, élimination, verrouillage des semaines et ex aequo du backend Sport Challenge. À utiliser pour créer ou modifier un type de catégorie, le calcul d'un classement, une clôture de semaine ou toute règle qui dépend du temps.
---

# Règles de classement et de temps

Les règles viennent **uniquement** du `README.md` : §2.3 (catégories, élimination, Points, D+ cumulé), §2.4 (coefficients), §2.5 (fenêtre, semaines, verrouillage), §2.6 (classement, égalités). Lis la section concernée avant d'écrire : ce skill dit **où** et **comment** les coder, pas ce qu'elles sont.

Le package `internal/scoring` est du **Go pur** (bibliothèque standard seulement) : il ne connaît ni la base, ni Gin, ni GORM. Une catégorie par fichier (`duration.go`, `distance.go`, `elimination.go`, `points.go`, `elevation.go`), les égalités dans `ranks.go`.

## Où vit chaque règle de temps

| Règle | Code |
|---|---|
| Premier instant d'un jour local (changement d'heure compris) | `scoring.StartOfDay` |
| Bornes de la semaine n | `Window.WeekBounds`, calculées depuis la **date** locale de début, jamais depuis l'instant |
| Instant de verrouillage d'une semaine | `Window.WeekLocksAt` : seul endroit qui porte cette règle |
| Semaines jugées (éliminations, Points acquis) | `Window.LockedWeeks` |
| Paramètres d'un challenge et de ses catégories | `challenge/rules.go` |

- **Jamais** `7*24*time.Hour` ni `24*time.Hour` pour avancer d'un jour ou d'une semaine : les changements d'heure les faussent.
- Le scorer lit les coefficients et paramètres **figés dans la catégorie**, jamais le catalogue des sports.
- L'instant courant vient de la fenêtre, fournie par l'horloge injectée : pas de `time.Now()`.
- Un jugement est déterministe : il ne dépend pas du moment où le worker tourne.

## Ajouter ou modifier un type de catégorie

1. **Tests d'abord** dans `internal/scoring`, avec au moins : cas normal ; égalités, y compris à la limite d'un top ; activité hors fenêtre ; début et fin de fenêtre exacts ; **changement d'heure** (mars et novembre, par exemple `America/Toronto`) ; dernière seconde d'une semaine incluse et seconde suivante exclue ; instant exact du verrouillage, y compris pendant un changement d'heure ; participant sans activité ; abandon ou exclusion avant et après un verrouillage ; compte supprimé. Ajoute les seuils propres à la règle tels que le README les décrit : exactement au seuil, juste en dessous, juste au-dessus.
2. Implémente `Scorer` (activités + participants + fenêtre → entrées classées).
3. Valide les paramètres de la catégorie dans `challenge/rules.go`.
4. Branche-le dans `leaderboard` (sélection du scorer par type) et, si la règle évolue dans le temps, dans `closure`.
5. Mets à jour le `README.md` (§2.3) **seulement** si l'utilisateur a validé le changement de règle.
6. Agents `spec-guardian` puis `go-reviewer`.

## Pièges

- Ne stocke jamais un classement comme source de vérité : recalcul depuis les activités brutes. Seul `result_snapshot` fige les résultats.
- Les tâches de clôture tournent toutes les heures et sont **idempotentes** : relancer ne doit rien changer si le travail est fait.
- Une semaine verrouillée l'est pour de bon. Seule une correction admin efface éliminations et résultats figés, puis rejoue toutes les semaines verrouillées avec le code du worker (README §2.12).
