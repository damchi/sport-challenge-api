# Catégories à la carte — plan d'implémentation

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal :** un challenge a toujours le temps cumulé, un sport de référence au choix, et en option la distance cumulée, l'élimination et une nouvelle catégorie Points.

**Architecture :** la table `categories` (type + `params jsonb`) accueille les catégories optionnelles ; `challenges.reference_sport` porte la référence ; un nouveau scorer pur `scoring.Points` est recalculé à la lecture à partir des activités brutes (jamais de points stockés), puis figé dans `result_snapshot` comme les autres.

**Tech Stack :** Go, Gin, oapi-codegen (`make generate`), GORM + SQL brut, golang-migrate, PostgreSQL.

**Spec :** `docs/superpowers/specs/2026-11-20-categories-a-la-carte-design.md`

## Global Constraints

- Une tâche = un ticket GitHub (SC-API-131 à SC-API-136) = un commit, sur la branche du ticket ; message en anglais, Conventional Commits, `Closes #N` puis `Refs: SC-API-1xx`.
- Contrat d'abord : `api/openapi.yaml` modifié avant le code, puis `make generate` et `make openapi-lint` sans écart.
- Nouvelles migrations seulement (`make migrate-new NAME=…`, paire up/down, `up → down → up` vérifié) ; jamais de modification de 000001–000011 ; aucune reprise de données (rien en production).
- `internal/scoring` n'importe que la bibliothèque standard ; tests de scoring écrits avant le code.
- Aucun `time.Now()` : `platform/clock`, `Window.Now`.
- Erreurs de validation : `apperr.InvalidFields` avec les noms de champs du contrat, triés.
- Avant de conclure chaque tâche : `make fmt && make lint && make test && make test-integration` ; agents `go-reviewer`, `spec-guardian`, `migration-reviewer` (si migration), `security-reviewer` (si route).
- Valeurs figées par la spec : référence par défaut `run` ; coefficient suggéré `min(1, f_sport / f_référence)` ; bornes des coefficients 0,1–2 ; barèmes top 5 = 10-8-6-4-2, top 10 = 25-18-15-12-10-8-6-4-2-1 ; durée minimale d'une séance pré-remplie à 20 min, bornée de 1 à 180 min ; ordre (positions) : temps 1, distance 2, élimination 3, points 4.

## Review Focus

1. **Challenge sans catégorie d'élimination** : `close-weeks` l'ignore, `finalize-challenges` fige quand même ses résultats (`LastWeek.CategoryID` vide, aucune élimination insérée). Test dans la tâche 4.
2. **Sport de référence dont le facteur dépasse 1 ou un sport à 0,1** : le coefficient calculé reste dans [0,1 ; 1] et a deux décimales au plus. Test dans la tâche 3.
3. **Mini-classement Points sans activité** (personne n'a couru, ou tout le monde est à 0) : personne ne marque, personne ne panique. Test dans la tâche 5.
4. **Points entre la fin d'une semaine et son verrouillage**, et entre la fin du challenge et le verrouillage final : les points restent provisoires, puis basculent dans les points acquis à 7 h. Test dans la tâche 5.
5. **Lecture brute des activités** pour un challenge à points mêlé, dans `Standings`, à des challenges sans points : chaque challenge garde son mode (agrégé ou brut). Test dans la tâche 6.

---

### Task 1 (SC-API-131) : règles produit dans le README

**Files :**
- Modify : `README.md` §2.1, §2.3, §2.4, §2.5 (dernier point), §5.4, §8
- Modify : `CLAUDE.md`, `.claude/agents/spec-guardian.md`, `.claude/skills/scoring-rules/SKILL.md`, `.claude/skills/bootstrap/SKILL.md` (les tickets eux-mêmes vivent dans GitHub, #250 à #255 ; `TASKS.md` n'est pas modifié)
- Add : `docs/superpowers/specs/2026-11-20-categories-a-la-carte-design.md`, ce plan

- [ ] **Step 1 :** §2.1 : remplacer « toujours les 3 catégories » par le tableau des catégories de la spec (temps toujours présent, les autres optionnelles, figées à la création) et le sport de référence (✅, décision du 2026-11-20).
- [ ] **Step 2 :** §2.3 : renommer la « catégorie 3 » en « élimination », sur le sport de référence ; ajouter la sous-section « Catégorie Points » (critères, barèmes, verrouillage, points provisoires, ex aequo, durée minimale), tout en ✅.
- [ ] **Step 3 :** §2.4 : coefficients relatifs à la référence, plafonnés à ×1 ; la référence vaut ×1 et ne s'envoie pas.
- [ ] **Step 4 :** vérifier qu'aucune autre phrase ne dit encore « 3 catégories » ou « course seule » pour l'élimination : `grep -n "3 catégories\|trois catégories\|catégorie 3" README.md CLAUDE.md` ; corriger aussi `CLAUDE.md` (« Catégories : exactement 3 ») et le skill `.claude/skills/scoring-rules` s'il l'énonce.
- [ ] **Step 5 : Commit** `docs: optional categories, reference sport and points category`

### Task 2 (SC-API-132) : renommer `run_elimination` en `elimination`

Refactor sans changement de comportement : la catégorie n'est plus propre à la course.

**Files :**
- Create : `migrations/000012_rename_elimination_category.{up,down}.sql`
- Modify : `api/openapi.yaml` (enums `Category.type`, `CategoryRanking.type`, `MyRanking.type`, descriptions qui citent `run_elimination`)
- Modify : `internal/challenge/model.go` (`CategoryRunElimination` → `CategoryElimination = "elimination"`), `internal/leaderboard/model.go`, `internal/closure/service.go`, `internal/closure/repository.go:54`, et leurs tests
- Test : `migrations/schema_integration_test.go`

- [ ] **Step 1 : migration up** : `UPDATE categories SET type = 'elimination' WHERE type = 'run_elimination'` (et `result_snapshot.category_type`, `elimination.category_type`) ; remplacer `categories_type_check`, `elimination_category_type_check` et le `DEFAULT` de `elimination.category_type`. Les deux clés étrangères composites `(category_id, challenge_id, category_type)` vers `categories` (`elimination_category_fkey`, `result_snapshot_category_fkey`) portent sur le type : les supprimer puis les recréer autour des `UPDATE`. Down : l'inverse exact.
- [ ] **Step 2 :** ajouter à `schema_integration_test.go` un cas qui insère une catégorie `run_elimination` et attend un refus de `categories_type_check`.
- [ ] **Step 3 :** renommer les constantes et la valeur dans le contrat, `make generate`.
- [ ] **Step 4 :** `make test && make test-integration` : PASS sans autre changement que les libellés.
- [ ] **Step 5 : Commit** `refactor(challenge): rename the run elimination category to elimination`

### Task 3 (SC-API-133) : sport de référence

**Files :**
- Create : `migrations/000013_add_challenge_reference_sport.{up,down}.sql`
- Modify : `api/openapi.yaml` (`ChallengeCreate.reference_sport`, `Challenge.reference_sport`, `ChallengeDetail.reference_sport` ; descriptions de `distance_factors` et de `CategoryParams.sport`)
- Modify : `internal/challenge/rules.go`, `service.go`, `model.go`, `handler.go`, `repository.go`
- Test : `internal/challenge/rules_test.go`, `service_test.go`, `handler_test.go`, `repository_integration_test.go`

**Interfaces :**
- Produces : `Challenge.ReferenceSport string` ; `Draft.ReferenceSport string` (vide = `run`) ; `func referenceFactors(suggested map[string]float64, reference string) map[string]float64` dans `rules.go` ; le port `Catalogue` gagne `DistanceMode(ctx context.Context, code string) (string, bool, error)`, déjà offert par `sport.Service`.

- [ ] **Step 1 : migration** : `ALTER TABLE challenges ADD COLUMN reference_sport text NOT NULL DEFAULT 'run' REFERENCES sports (code)`. Down : `DROP COLUMN`.
- [ ] **Step 2 : tests qui échouent**, dans `rules_test.go` :

```go
func TestReferenceFactors(t *testing.T) {
	suggested := map[string]float64{"run": 1, "walk": 1, "ride": 0.3, "rowing": 0.5, "other": 1}
	tests := []struct {
		name      string
		reference string
		want      map[string]float64
	}{
		{"run keeps the catalogue", "run", map[string]float64{"run": 1, "walk": 1, "ride": 0.3, "rowing": 0.5, "other": 1}},
		{"ride: slower sports capped at 1", "ride", map[string]float64{"run": 1, "walk": 1, "ride": 1, "rowing": 1, "other": 1}},
		{"rowing: ride 0.3/0.5", "rowing", map[string]float64{"run": 1, "walk": 1, "ride": 0.6, "rowing": 1, "other": 1}},
	}
	// assert maps equal
}
```

  Ajouter un cas où la référence a un facteur de 2 et un sport de 0,1 : le résultat est 0,1, jamais en dessous (Review Focus 2). Ajouter aussi un cas où un rapport non exact (0,3/0,7) est arrondi à deux décimales, la moitié vers le haut.
- [ ] **Step 3 :** implémenter `referenceFactors` : `clamp(round2(f / fRef), 0.1, 1)` pour chaque sport, la référence à 1. Supprimer la constante `referenceSport` ; `invalidFactors` reçoit la référence et refuse `distance_factors.<référence>`.
- [ ] **Step 4 :** validation : `reference_sport` doit être un sport du catalogue en mode `required`, sinon champ `reference_sport`. Tests de `Validate` : `ride` accepté, `yoga`, `other` et `unknown` refusés ; `distance_factors.ride` refusé quand la référence est `ride`, accepté quand c'est `run`.
- [ ] **Step 5 :** `Service.Create` : facteurs = `referenceFactors(suggested, ref)` puis ceux du brouillon ; `Params.Sport` de l'élimination = la référence ; `Challenge.ReferenceSport` stocké et relu (`toAPI`, `toDetail`). Test du service : un challenge `ride` a les facteurs `run: 1, ride: 1` et une élimination sur `ride`.
- [ ] **Step 6 :** `UpdateChallenge` : ajouter `reference_sport` aux champs figés (`handler.go:250`) ; test du handler : 422 avec ce champ.
- [ ] **Step 7 :** `make test && make test-integration` : PASS.
- [ ] **Step 8 : Commit** `feat(challenge): choose the reference sport of a challenge`

### Task 4 (SC-API-134) : distance cumulée et élimination optionnelles

**Files :**
- Modify : `api/openapi.yaml` : `ChallengeCreate` perd `elimination_step_meters` (au premier niveau) et gagne `categories`, un objet sans propriété supplémentaire : `cumulative_distance` (objet vide, sa présence active la catégorie) et `elimination` (`{ step_meters: 1..100000 }`, requis dans l'objet). Toutes les descriptions « 3 catégories » deviennent « les catégories du challenge, dans leur ordre ». `distance_factors` n'est accepté que si une catégorie s'en sert.
- Modify : `internal/challenge/rules.go`, `service.go`, `handler.go` (champs figés : `categories` y est déjà), `internal/closure/service.go`, `internal/leaderboard/service.go`
- Test : `internal/challenge/rules_test.go`, `service_test.go`, `handler_test.go`, `internal/closure/service_test.go`, `service_integration_test.go`, `internal/leaderboard/service_test.go`

**Interfaces :**
- Consumes : `Draft.ReferenceSport` (tâche 3).
- Produces : `Draft.Distance bool` ; `Draft.Elimination *EliminationDraft` avec `type EliminationDraft struct{ StepMeters int }` ; `Draft.EliminationStepMeters` disparaît. Positions fixes par type : 1, 2, 3 (`const positionTime, positionDistance, positionElimination = 1, 2, 3`).

- [ ] **Step 1 : tests de `Validate`** : sans `categories`, le challenge est valide avec le temps seul ; `elimination.step_meters` à 0 ou à 100 001 → champ `categories.elimination.step_meters` ; `distance_factors` sans distance cumulée → champ `distance_factors`.
- [ ] **Step 2 : test du service** : `Create` sans catégorie optionnelle stocke une seule catégorie (`cumulative_time`, position 1) ; avec `elimination` seule, positions 1 et 3.
- [ ] **Step 3 :** implémenter (validation, construction des catégories, handler).
- [ ] **Step 4 : test de `closure`** (Review Focus 1), en intégration : un challenge terminé sans élimination est ignoré par `CloseWeeks` (aucune ligne `week_closure` avant la fin) et `FinalizeChallenges` fige sa seule catégorie, avec la ligne `week_closure` de la dernière semaine et aucune élimination. Corriger `finalizeChallenge` si `LastWeek.CategoryID` vide fait échouer `insertEliminations`.
- [ ] **Step 5 : test du leaderboard** : `Get` et `Standings` d'un challenge à une seule catégorie renvoient une seule `CategoryRanking` et une seule `Place` ; `weeklySport` vide n'ajoute aucun regroupement par semaine.
- [ ] **Step 6 :** `make test && make test-integration` : PASS ; le test de contrat valide les réponses à 1, 2 et 3 catégories.
- [ ] **Step 7 : Commit** `feat(challenge): make the distance and elimination categories optional`

### Task 5 (SC-API-135) : scorer `scoring.Points` (tests d'abord)

**Files :**
- Create : `internal/scoring/points.go`, `internal/scoring/points_test.go`
- Modify : `internal/scoring/types.go` (champ `Provisional` d'`Entry`, champ `AbandonedAt` de `Participant`)

**Interfaces :**
- Produces :

```go
// Criteria of the points category.
const (
	CriterionLongestDuration = "longest_duration"
	CriterionLongestDistance = "longest_distance"
	CriterionSessionCount    = "session_count"
)

type Points struct {
	Criteria          []string           // at least one, no duplicate (checked at creation)
	Podium            int                // 5 or 10
	Factors           map[string]float64 // longest_distance: equivalent distance, as Distance
	MinSessionSeconds int                // session_count: shorter sessions do not count
}

func (p Points) Score(w Window, participants []Participant, activities []Activity) []Entry

// In Participant: AbandonedAt is when an abandoned participant left (nil
// otherwise); the points category keeps them in the weeks locked by then.
AbandonedAt *time.Time

// In Entry: Provisional is, in the points category only, the points of the
// weeks started but not locked yet at w.Now; nil elsewhere, for an abandoned
// participant, and once every started week is locked.
Provisional *int
```

  `Entry.Value` = points acquis (semaines verrouillées : `n <= w.LockedWeeks()`) ; le rang porte sur `Value` seul.

- [ ] **Step 1 : tests qui échouent**, table-driven, avec une fenêtre de 3 semaines à Montréal et `Window.Now` choisi par cas :
  - `TestPointsLongestDuration` : semaine 1 verrouillée, A 60 min, B 45 min, C 45 min, podium 5 → A 10, B 8, C 8 ; rangs 1, 2, 2.
  - `TestPointsLongestDistanceEquivalent` : facteurs `run: 1, ride: 0.3` ; A court 10 km, B roule 40 km (12 km éq.) → B 10, A 8.
  - `TestPointsSessionCountMinimum` : `MinSessionSeconds = 1200` ; A 3 séances de 20 min, B 5 séances de 19 min → A 10, B ne marque rien.
  - `TestPointsTieAtPodiumEdge` : podium 5, huit personnes : quatre valeurs distinctes, trois ex aequo au 5ᵉ rang, une dernière au 8ᵉ → les trois ex aequo reçoivent 2 chacun, la 8ᵉ ne marque pas.
  - `TestPointsPodium10Scale` : dix personnes aux valeurs distinctes → 25, 18, 15, 12, 10, 8, 6, 4, 2, 1.
  - `TestPointsNoActivity` (Review Focus 3) : personne n'a d'activité → tout le monde à 0, tous ex aequo au rang 1, `Provisional` à 0 pendant la semaine en cours.
  - `TestPointsProvisional` (Review Focus 4) : `Now` le lundi de la semaine 2 à 6 h 59, heure locale → semaine 1 encore provisoire (`Value` 0, `Provisional` = points de la semaine 1 + ceux de la semaine 2) ; à 7 h 00 → points de la semaine 1 dans `Value`, `Provisional` = semaine 2. Même vérification après la fin du challenge, avant et après le verrouillage final (alors `Provisional` nil).
  - `TestPointsAbandoned` : A, 1ᵉʳ en semaine 1, abandonne en semaine 2 (`AbandonedAt` après le verrouillage de la semaine 1) → en semaine 1, A garde la 1ʳᵉ place et B reste 2ᵉ (8 points) ; en semaine 2, A n'est plus classé et B prend la 1ʳᵉ place ; A apparaît abandonné (rang et valeur 0, `Provisional` nil). Un abandon **avant** le verrouillage de la semaine 1 le retire aussi de la semaine 1.
  - `TestPointsActivityOutsideWindow` : une activité hors fenêtre n'apporte rien.
- [ ] **Step 2 :** `go test ./internal/scoring/ -run Points` : FAIL (`Points` non défini).
- [ ] **Step 3 : implémenter** : pour chaque semaine n commencée à `w.Now` et pour chaque critère, la valeur de chaque participant actif, et de chaque participant parti après le verrouillage de la semaine n (`AbandonedAt >= w.WeekLocksAt(n)`) (durée max, `Distance{Factors}.Weighted` max, nombre de séances ≥ minimum) ; un mini-classement par `Rank` sur ces participants (traités comme actifs pour cette semaine) dont la valeur est > 0 ; les points du barème pour un rang ≤ `Podium` ; total dans `Value` si `n <= w.LockedWeeks()`, sinon dans `Provisional`. Classement final : `Rank(participants, acquis)`.
- [ ] **Step 4 :** `go test ./internal/scoring/` : PASS.
- [ ] **Step 5 : Commit** `feat(scoring): rank the points category`

### Task 6 (SC-API-136) : catégorie Points de bout en bout

**Files :**
- Create : `migrations/000014_allow_points_category.{up,down}.sql`
- Modify : `api/openapi.yaml` : `ChallengeCreate.categories.points` (`{ criteria: [..] minItems 1 uniqueItems, podium: 5|10, min_session_minutes: 1..180 (default 20) }`) ; `Category.type` et les enums de classement + `points` ; `CategoryParams` : `criteria`, `podium`, `min_session_seconds` ; `LeaderboardEntry.provisional_points` et `MyRanking.provisional_points` (entiers, nullables, « points category only ») ; `value` en points pour `points`.
- Modify : `internal/challenge` (model `Params`, rules, service, handler), `cmd/api/main.go:330` (adaptateur vers `leaderboard.Category`), `internal/leaderboard` (model, service `scorerOf`, repository `Scope`, handler), `internal/closure` (model, repository `EndedChallenges`, service `scorerOf`, interface `Activities`)
- Test : les fichiers `_test.go` correspondants, `migrations/schema_integration_test.go`

**Interfaces :**
- Consumes : `scoring.Points`, `Entry.Provisional` (tâche 5) ; `Draft` de la tâche 4.
- Produces : `CategoryPoints = "points"` (position 4) ; `Params.Criteria []string`, `Params.Podium int`, `Params.MinSessionSeconds int` (JSON `criteria`, `podium`, `min_session_seconds`), et `Params.Factors` aussi rempli sur la catégorie Points quand `longest_distance` est choisi ; `Draft.Points *PointsDraft{Criteria []string; Podium int; MinSessionMinutes int}` ; `leaderboard.Scope.Raw bool` ; `closure.Activities.Activities(ctx, challengeID string, w scoring.Window, weeklySport string, raw bool)`.

- [ ] **Step 1 : migration** : `categories_type_check` accepte `points`, `categories_position_check` passe à `BETWEEN 1 AND 4`. Test de schéma : une catégorie `points` en position 4 est acceptée.
- [ ] **Step 2 : validation** (tests d'abord) : `criteria` vide, inconnu ou en double → `categories.points.criteria` ; `podium` ≠ 5 et 10 → `categories.points.podium` ; `min_session_minutes` hors 1–180 → `categories.points.min_session_minutes` ; `distance_factors` accepté quand seul `longest_distance` s'en sert. La durée est stockée en secondes.
- [ ] **Step 3 : lecture brute** (`leaderboard/repository.go`) : quand `Scope.Raw` est vrai, `ActivitiesOf` renvoie chaque activité avec son vrai `started_at`, sans agréger, et inclut celles des participants qui ont abandonné (pas des comptes supprimés) ; sinon, rien ne change. `ParticipantsOf` renvoie `AbandonedAt`. Les autres scorers ignorent déjà la valeur d'un abandon (`Rank`, `Eliminations`) : vérifier par un test qu'un challenge à points mêlant abandons et élimination classe les autres catégories à l'identique. Mettre à jour le commentaire du contrat de `ActivitiesOf`. Test d'intégration (Review Focus 5) : un appel avec deux scopes, l'un brut et l'autre agrégé, renvoie 3 activités brutes pour le premier et un seul total pour le second.
- [ ] **Step 4 : leaderboard** : `scorerOf` construit `scoring.Points` ; `Raw` vaut vrai quand le challenge a une catégorie Points (dans `Get` et dans `Standings`) ; le handler expose `provisional_points`. Tests du service : classement Points avec points provisoires ; `Standings` donne la place Points.
- [ ] **Step 5 : clôture** : `EndedChallenges` lit `criteria`, `podium`, `min_session_seconds` ; `scorerOf` construit `scoring.Points` ; `finalizeChallenge` lit les activités en brut s'il y a une catégorie Points. Test d'intégration : le résultat figé de Points est celui calculé juste après le verrouillage final, et la lecture figée (`frozen`) le sert à l'identique, sans `provisional_points`. Après la suppression d'un compte, les autres remontent dans les rangs ; leurs points ne sont pas recalculés, comme pour les autres catégories (rangs recalculés à la lecture, valeurs figées).
- [ ] **Step 6 :** `make test && make test-integration` : PASS ; le test de contrat valide un classement et « Mes défis » avec une catégorie Points.
- [ ] **Step 7 : Commit** `feat(challenge): add the points category`
