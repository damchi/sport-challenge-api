# Sport Challenge — API (backend)

API Go d'une application de challenges sportifs entre amis : catégories avec podiums, élimination hebdomadaire, badges. Le frontend (web + mobile) est un autre dépôt.

## Source de vérité

- `README.md` est la spécification du produit et de l'architecture. **Lis le §2 (règles fonctionnelles) et le §5 (architecture) avant toute tâche qui touche au métier ou à la structure.**
- Légende du README : ✅ décidé · 🟡 proposition · ❓ question ouverte. Applique les ✅. Ne transforme jamais un 🟡 ou un ❓ en règle sans demander.
- **Backlog** : issues GitHub (importées depuis `TASKS.md`, qui reste le document de référence ; numérotation non consécutive : les anciens tickets de préparation ont été fusionnés dans les tickets fonctionnels ; un ticket = un commit). Utilise le skill `/dev-workflow:next-task` pour le traiter ; le statut vit dans l'issue, pas dans `TASKS.md`.
- **Workflow Claude** : plugins `dev-workflow` et `backend-review` de [claude-library](https://github.com/damchi/claude-library), configurés par `.claude/workflow.json` (préfixe, Project, vérifications, taille maximale d'une pull request). `ticket config` en donne le résumé. Si `ticket` est introuvable, le plugin `dev-workflow` n'est pas chargé, et sa garde Git non plus : arrête-toi et demande à l'utilisateur de l'installer (`/plugin`) avant toute écriture git.
- Contrat HTTP : `api/openapi.yaml` (spec-first : on modifie le contrat avant le code).
- Si le code et le README se contredisent, signale-le et demande, ne tranche pas seul.

## Stack

Go (version dans `go.mod`) · Gin · PostgreSQL (pgx) · GORM pour le CRUD, SQL brut pour classements et agrégations · **golang-migrate** · `log/slog` · validation JWT via JWKS (**Auth0**) · Docker · GitHub Actions · GCP Montréal (Cloud Run, Cloud SQL, Cloud Storage, Cloud Scheduler, Secret Manager), infrastructure configurée à la main et décrite dans `docs/deploiement.md`.

## Commandes

Cibles du `Makefile`, ajoutées **au fur et à mesure** par les tickets qui s'en servent (voir `TASKS.md`) :

```
make run                  API en local
make worker TASK=...      tâche du worker (close-weeks, finalize-challenges, retry-auth0-deletions)
make test                 tests unitaires
make test-integration     tests avec PostgreSQL en conteneur
make lint                 golangci-lint
make vuln                 govulncheck (vulnérabilités atteintes par le code)
make fmt                  gofmt + gofumpt
make migrate-new NAME=x   crée une paire de migrations
make migrate-up           applique les migrations (base locale)
make migrate-down         annule la dernière migration (base locale)
make generate             régénère le code depuis openapi.yaml
make docker-up / docker-down   PostgreSQL (émulateur de stockage en V1.1)
```

**Avant de dire qu'une tâche est terminée** : `make fmt && make lint && make test` passent. Lance aussi `make test-integration` si tu as touché au SQL ou aux migrations. La définition de terminé complète est dans `TASKS.md`, section Conventions.

## Architecture

Structure **cible** : chaque dossier est créé par le premier ticket qui y met du code utile.

```
cmd/api, cmd/worker, cmd/migrate
internal/
  platform/   config, db, httpx, auth, storage, clock, apperr
  api         code generated from api/openapi.yaml (make generate)
  health      GET /health, GET /ready
  user, challenge, invitation, sport, activity, scoring,
  leaderboard, closure, workerrun, admin, badge (V1.1), integration (V2+), notification (V3)
migrations/   api/openapi.yaml   deploy/   .github/workflows/
```

Règles de dépendance :
- `handler` → `service` → `repository`. Le handler ne contient aucune règle métier. Seul le repository parle à la base.
- Un module n'importe **jamais** le repository d'un autre module. Les modules se parlent par de petites interfaces définies côté consommateur.
- `internal/scoring` n'importe que la bibliothèque standard.
- `platform` peut être importé par tous et n'importe aucun module métier.
- Pas de variable globale mutable ; tout est injecté depuis `cmd/*/main.go` (câblage manuel, pas de framework d'injection).

## Règles métier : où les lire

Les règles produit sont **uniquement** dans le README : lis la section concernée avant d'écrire, ne t'appuie pas sur ta mémoire d'une version précédente.

| Sujet | README |
|---|---|
| Challenges : fenêtre, durée, visibilité, sport de référence, paramètres figés à la création | §2.1 |
| Participation : inscription, abandon, exclusion, suppression de compte | §2.2 |
| Catégories et podiums : temps, distance, élimination, Points, D+ cumulé | §2.3 |
| Coefficients de distance | §2.4 |
| Activités : saisie, fenêtres, verrouillage des semaines | §2.5 |
| Classement, égalités | §2.6 |
| Badges (V1.1 : ne rien développer en V1) | §2.7 |
| Administration : corrections, recalcul, journal | §2.12 |
| Matrice des droits | §5.7 |
| Données et conformité | §6 |

## Règles techniques non négociables

- **Temps** : tout instant est stocké en UTC (`timestamptz`) ; le fuseau du challenge sert à découper. Jours et semaines viennent de `scoring.StartOfDay`, `scoring.Window.WeekBounds` et `scoring.Window.WeekLocksAt`, jamais de durées fixes (7×24 h). Aucun appel direct à `time.Now()` dans les services ni dans `scoring` : passe par `platform/clock`.
- **Classement** : toujours recalculé depuis les activités brutes. Jamais de totaux stockés comme source de vérité (le cache de `leaderboard.Service` garde en mémoire un classement calculé, jamais en base, et ne sert pas les lectures dans une transaction). Seul `result_snapshot` fige les résultats ; les Points ne sont jamais stockés.
- **Règles pures** : élimination, Points et conditions de badges sont des fonctions pures de `internal/scoring` (et `internal/badge/rules.go`), testées sans base. Une condition de badge est structurée, jamais du code ni une expression libre.
- **Worker** : chaque tâche est idempotente et peut être relancée sans effet si le travail est fait.
- **Création d'activité** : un **point d'entrée unique** (`activity.Service`), utilisé par la saisie manuelle et, plus tard, par les imports ; c'est là que s'appliquent le verrouillage des semaines, la validation et l'évaluation des badges. `activity.source` et `external_id` existent dès la V1.
- **Autorisation** : toujours dans le **service**, jamais dans un handler ni un middleware. Chaque cas d'usage reçoit `actorID` explicitement, charge la ressource, puis appelle une fonction de politique pure (`policy.go`) avant d'écrire. Ressource privée inaccessible = 404, action interdite = 403 `forbidden`. Les listes filtrent dans la requête SQL. Chaque règle a un test « interdit ». **Admin** : la permission `admin` vient de l'access token et se vérifie dans `policy.go` ; les modifications admin sont des cas d'usage admin séparés dans chaque service, jamais un `if admin` dans les règles existantes ; une route `/admin/*` appelée sans la permission répond 404 `not_found`, et un test parcourt ces routes pour le vérifier.
- **Données** : résumés d'activité uniquement (pas de traces GPS) ; la suppression de compte est une anonymisation (détail au README §2.2 et §2.12).

## Base de données et migrations

- Le schéma vient **uniquement** des migrations golang-migrate. Jamais `AutoMigrate`. Les tags GORM ne pilotent pas le schéma.
- Une paire `NNNNNN_nom.up.sql` / `.down.sql` par changement. Ne modifie jamais une migration déjà appliquée : crée-en une nouvelle.
- Modèle de données : README §5.4. Voir le skill `new-migration` et l'agent `backend-review:migration-reviewer`.

## Conventions de code

- **Pas de code d'avance** : n'ajoute une fonction, un type, un helper, un package, une variable de configuration ou une cible du `Makefile` que si le ticket en cours les utilise. L'arborescence du README §5.2 est la cible, pas un échafaudage à créer d'avance : un dossier naît avec son premier fichier utile (pas de `doc.go` ni de fichier vide). Un besoin imprévu s'ajoute au ticket en cours.
- Langue : code, identifiants, commentaires et messages de commit en **anglais** ; README et documentation en **français**.
- `context.Context` en premier paramètre de toute fonction qui touche à la base ou au réseau.
- Erreurs : wrapper avec `%w`, erreurs métier dans `platform/apperr` (écrit pour ce projet, contrat au README §5.8 ; le package maison de Faunadex n'est pas repris) avec un **code stable** en `snake_case` (`challenge_not_found`), un statut HTTP, des paramètres optionnels et une cause chaînée. La réponse est construite à un seul endroit (`platform/httpx`). Jamais de texte destiné à l'utilisateur dans l'API : le client traduit d'après le code. La traduction côté serveur est réservée aux textes émis par le serveur (courriels, notifications).
- Pas de `panic` hors `main`. Pas de logique métier dans les handlers.
- Logs structurés avec `slog`, avec l'identifiant de requête. **Jamais** de donnée personnelle ni de jeton dans les logs.
- GORM : pas de `Preload` en boucle (N+1) ; pour `Updates`, utilise une `map` ou `Select` (les valeurs zéro sont ignorées avec une struct).
- Tests unitaires en table-driven, à côté du code. Le temps est toujours injecté via `Clock`.

## API

- REST sous `/api/v1`, pagination par curseur, créations d'activité avec clé d'idempotence.
- Les erreurs ont un format unique : `{ "error": { "code": "...", "message": "..." } }`.
- Voir le skill `new-endpoint`.

## Sécurité

- Valide le JWT (signature JWKS, audience, expiration) dans un middleware ; l'utilisateur local est dans le contexte.
- **Chaque** route vérifie l'autorisation dans le service, d'après la matrice des droits du README §5.7 (créateur, participant, invité, public, admin) ; une ressource privée inaccessible est introuvable (404).
- Les requêtes SQL brutes sont toujours paramétrées.
- Aucun secret dans le dépôt : Secret Manager en déploiement, `.env` local ignoré par git.
- Ne lis pas et n'affiche pas `.env` ni les fichiers de secrets.

## Workflow attendu

1. Lis la partie du README concernée. S'il y a un ❓ ou un 🟡 qui bloque, pose la question.
2. Avant de coder, lance le skill du dépôt qui correspond au ticket (plusieurs si besoin) : `new-endpoint` s'il touche à `api/openapi.yaml`, `new-migration` s'il touche au schéma, `new-module` s'il crée un dossier `internal/<nom>`, `scoring-rules` s'il touche aux catégories, au classement ou aux règles de temps. Cela vaut aussi dans `/dev-workflow:next-task`.
3. Fais un court plan avant les changements importants (plusieurs fichiers, migration, nouveau module).
4. Pour `scoring`, les règles de temps et les règles de badges : écris d'abord les tests.
5. Implémente, puis lance format, lint et tests.
6. Fais relire par les agents adaptés (voir ci-dessous) avant de committer.
7. **Git** : une fois format, lint, tests et relectures passés, tu **commits et pushes sans demander l'accord** (en anglais, un changement cohérent par commit, sur la branche du ticket). Tu présentes ensuite les fichiers modifiés, le résumé du diff et les commits poussés.
   - Tu peux utiliser `git status`, `git diff`, `git log`, `git add`, `git commit` et `git push` (branche de travail) librement.
   - **Pull request** : dès que le code est poussé, ouvre-la sans demander (`gh pr create`, titre = message du commit, description avec `Closes #<numéro>`) ; ne la fusionne jamais. Une fois ouverte, et après chaque push, attends la fin de la CI (`Monitor` sur `gh pr checks`, sans sondage), vérifie qu'elle est entièrement verte, puis lis tous les commentaires (`gh pr view --comments`, commentaires en ligne). Applique les corrections fondées (commit et push sur la même branche) ou explique pourquoi tu ne les suis pas, et recommence jusqu'à une CI verte sans commentaire en suspens. Ne dis un ticket terminé qu'à ce moment-là ; un point qui touche une règle produit (🟡 ou ❓) ou le périmètre se demande à l'utilisateur.
   - **Taille** : une pull request fait au plus 200 lignes ajoutées + supprimées (`pr_size` de `.claude/workflow.json`, mesurée par `ticket size`). Au-delà, découpe le ticket (skill `/dev-workflow:new-ticket`) ; la garde Git demande la validation de l'utilisateur pour pousser une branche plus grosse.
   - **Issues** : toute issue créée rejoint le Project https://github.com/users/damchi/projects/7, statut « Todo » (`ticket new`, ou hook PostToolUse du plugin sur `gh issue create` ; à défaut `ticket add <numéro>`).
   - **Branches fusionnées** : supprimées automatiquement sur GitHub (réglage du dépôt « delete branch on merge ») et en local (`ticket cleanup`, lancé par le hook SessionStart du plugin et au début de `next-task` ; il supprime aussi la branche distante d'une pull request fusionnée si GitHub ne l'a pas fait, seule suppression distante autorisée sans demande).
   - Push vers `main`, suppression manuelle de branche distante, merge, rebase, tag, reset, `gh pr merge` : uniquement sur demande explicite de l'utilisateur, une action à la fois.
   - Push forcé, `git reset --hard`, `git clean`, `--no-verify` : interdits (bloqués par la garde Git du plugin et par les permissions).

## Ne jamais

- Pousser sur `main`, fusionner une pull request, ou contourner une confirmation ou un refus de la configuration (autre commande git équivalente, script, `git -C`, etc.).
- Créer, modifier ou supprimer une ressource GCP, ou déployer, sans demande explicite. Toute commande `gcloud` passe `--project=sport-challenge-prod`.
- Modifier une migration déjà appliquée, ou utiliser `AutoMigrate`.
- Appeler `time.Now()` dans la logique métier.
- Ajouter une dépendance sans la justifier (préfère la bibliothèque standard).
- Inventer une règle produit absente du README.
- Écrire du code d'avance : helper, méthode, package, variable de configuration ou cible `make` que le ticket en cours n'utilise pas.

## Agents et skills

Du dépôt (`.claude/skills/` et `.claude/agents/`) :

| Nom | Type | Quand l'utiliser |
|---|---|---|
| `new-module` | skill | Créer un module `internal/<nom>` |
| `new-migration` | skill | Ajouter ou modifier le schéma de la base |
| `new-endpoint` | skill | Ajouter une route (OpenAPI d'abord) |
| `scoring-rules` | skill | Toucher aux catégories, au classement ou aux règles de temps |
| `closure-reviewer` | agent | Relecture de la clôture des semaines : `internal/closure`, worker, `workerrun`, `result_snapshot`, verrou des semaines |

Des plugins (les tickets peuvent garder les noms courts) :

| Nom | Type | Quand l'utiliser |
|---|---|---|
| `dev-workflow:next-task` | skill | Prendre la prochaine issue GitHub du backlog, l'implémenter, committer, pousser, relire les commentaires de la PR |
| `dev-workflow:new-ticket` | skill | Créer un ticket au format du backlog, ou découper un ticket trop gros |
| `dev-workflow:spec-guardian` | agent | Vérifier que le code respecte les règles du README |
| `backend-review:go-reviewer` | agent | Relecture du code Go après modification |
| `backend-review:migration-reviewer` | agent | Relecture de toute migration |
| `backend-review:security-reviewer` | agent | Relecture sécurité des routes, de l'authentification et des données |
| `backend-review:openapi-contract-reviewer` | agent | Relecture de tout changement de `api/openapi.yaml` : changements cassants, cohérence avec le code généré, les codes d'erreur et les tests |
| `backend-review:sql-query-reviewer` | agent | Relecture des requêtes d'un repository, d'un classement, d'une agrégation ou d'une liste paginée |
| `backend-review:rules-tester` | agent | Écrire et lancer les tests des cas limites d'une règle produit touchée (complète `spec-guardian`) |
