# Sport Challenge — API (backend)

API Go d'une application de challenges sportifs entre amis : catégories avec podiums, élimination hebdomadaire, badges. Le frontend (web + mobile) est un autre dépôt.

## Source de vérité

- `README.md` est la spécification du produit et de l'architecture. **Lis le §2 (règles fonctionnelles) et le §5 (architecture) avant toute tâche qui touche au métier ou à la structure.**
- Légende du README : ✅ décidé · 🟡 proposition · ❓ question ouverte. Applique les ✅. Ne transforme jamais un 🟡 ou un ❓ en règle sans demander.
- **Backlog** : issues GitHub (importées depuis `TASKS.md`, qui reste le document de référence ; 67 tickets `SC-API-001` à `SC-API-075`, numérotation non consécutive : les anciens tickets de préparation ont été fusionnés dans les tickets fonctionnels ; un ticket = un commit). Utilise le skill `next-task` pour le traiter ; le statut vit dans l'issue, pas dans `TASKS.md`.
- Contrat HTTP : `api/openapi.yaml` (spec-first : on modifie le contrat avant le code).
- Si le code et le README se contredisent, signale-le et demande, ne tranche pas seul.

## Stack

Go (version dans `go.mod`) · Gin · PostgreSQL (pgx) · GORM pour le CRUD, SQL brut pour classements et agrégations · **golang-migrate** · `log/slog` · validation JWT via JWKS (**Auth0**) · Docker · GitHub Actions · Terraform · GCP Montréal (Cloud Run, Cloud SQL, Cloud Storage, Cloud Scheduler, Secret Manager).

## Commandes

Cibles du `Makefile`, ajoutées **au fur et à mesure** par les tickets qui s'en servent (voir `TASKS.md`) :

```
make run                  API en local
make worker TASK=...      tâche du worker (close-weeks, finalize-challenges)
make test                 tests unitaires
make test-integration     tests avec PostgreSQL en conteneur
make lint                 golangci-lint
make fmt                  gofmt + gofumpt
make migrate-new NAME=x   crée une paire de migrations
make migrate-up           applique les migrations (base locale)
make migrate-down         annule la dernière migration (base locale)
make generate             régénère le code depuis openapi.yaml
make docker-up / docker-down   PostgreSQL + émulateur de stockage
```

**Avant de dire qu'une tâche est terminée** : `make fmt && make lint && make test` passent. Lance aussi `make test-integration` si tu as touché au SQL ou aux migrations.

## Architecture

Structure **cible** : chaque dossier est créé par le premier ticket qui y met du code utile.

```
cmd/api, cmd/worker
internal/
  platform/   config, db, httpx, auth, storage, clock, apperr
  user, challenge, invitation, sport, activity, scoring,
  leaderboard, closure, badge, integration (V2+), notification (V3)
migrations/   api/openapi.yaml   deploy/   .github/workflows/
```

Règles de dépendance :
- `handler` → `service` → `repository`. Le handler ne contient aucune règle métier. Seul le repository parle à la base.
- Un module n'importe **jamais** le repository d'un autre module. Les modules se parlent par de petites interfaces définies côté consommateur.
- `internal/scoring` n'importe que la bibliothèque standard.
- `platform` peut être importé par tous et n'importe aucun module métier.
- Pas de variable globale mutable ; tout est injecté depuis `cmd/*/main.go` (câblage manuel, pas de framework d'injection).

## Règles métier non négociables

- **Temps** : tout instant est stocké en UTC (`timestamptz`). Le fuseau du challenge sert à découper : minuit local, semaines calculées en heure locale (`AddDate`, pas des durées fixes de 7×24 h). Aucun appel direct à `time.Now()` dans les services ni dans `scoring` : passe par `platform/clock`.
- **Classement** : toujours recalculé depuis les activités brutes. Jamais de totaux stockés comme source de vérité. Seul `result_snapshot` fige les résultats à la fin du challenge.
- **Catégories** : exactement **3** (temps cumulé tous sports ; distance cumulée avec ratio à la course ; course seule avec élimination). Un participant est classé dans **toutes** les catégories. Catégories et coefficients sont **figés** à la création.
- **Activité** : compte si `started_at ∈ [starts_at, ends_at)` ; refusée après la fin. Durée obligatoire ; distance seulement si le sport produit des km.
- **Participation** : inscription possible jusqu'à la fin de la semaine 1. Personne ne quitte vraiment : « quitter » ou la suppression d'un compte = **abandon** (éliminé de toutes les catégories, définitivement, affiché « a abandonné »). Les créateurs peuvent supprimer le challenge, ajouter un créateur et exclure un participant (un exclu apparaît comme un abandon). Un challenge est supprimable à tout moment. La vitesse n'est jamais saisie : elle est calculée à partir de la durée et de la distance.
- **Élimination** (catégorie 3) : X est un incrément en km fixé par le créateur ; la distance de la semaine s+1 doit être **au minimum** celle de la semaine s + X, sinon éliminé de cette catégorie à la fin de la semaine. Pas d'élimination en semaine 1. ✅ La référence est la distance réellement courue : si elle dépasse le minimum, elle devient la nouvelle base (minimum suivant = distance réelle + X). Isole la règle dans une seule fonction pure. Jugée à l'instant exact de la fin de la semaine (**pas de délai de grâce**), jamais en cours de semaine. ✅ Une semaine terminée est verrouillée : l'API refuse d'ajouter, modifier ou supprimer une activité datée de cette semaine (l'horloge fait foi, pas le passage du worker). Les personnes qui terminent sans élimination sont classées par km cumulés, quelle que soit leur base de départ.
- **Égalités** : ex aequo (1, 1, 3).
- **Worker** : chaque tâche est idempotente et peut être relancée sans effet si le travail est fait.
- **Badges** (V1.1 : reportés après la V1, ne rien développer en V1) : créés par les créateurs d'un challenge, avec une condition **structurée** (mesure, portée, filtre de sport, seuil) : jamais de code ni d'expression libre. L'évaluation est une fonction pure (`internal/badge/rules.go`), déclenchée après chaque création d'activité ; l'attribution est immédiate et **idempotente** (unique par badge et utilisateur). 🟡 Un badge gagné est conservé ; 50 badges maximum par challenge.
- **Création d'activité** : un **point d'entrée unique** (`activity.Service`), utilisé par la saisie manuelle et, plus tard, par les imports ; c'est là que s'appliquent le verrouillage des semaines, la validation et l'évaluation des badges. `activity.source` et `external_id` (nul en V1) existent dès la V1.
- **Autorisation** : toujours dans le **service**, jamais dans un handler ni un middleware. Chaque cas d'usage reçoit `actorID` explicitement, charge la ressource, puis appelle une fonction de politique pure (`policy.go`) avant d'écrire. Ressource privée inaccessible = 404, action interdite = 403 `forbidden`. Les listes filtrent dans la requête SQL. Chaque règle a un test « interdit ». La matrice des droits est au README §5.7.
- **Données** : résumés d'activité uniquement (pas de traces GPS). Suppression de compte = anonymisation, traitée comme un départ.

## Base de données et migrations

- Le schéma vient **uniquement** des migrations golang-migrate. Jamais `AutoMigrate`. Les tags GORM ne pilotent pas le schéma.
- Une paire `NNNNNN_nom.up.sql` / `.down.sql` par changement. Ne modifie jamais une migration déjà appliquée : crée-en une nouvelle.
- Voir le skill `new-migration` et l'agent `migration-reviewer`.

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
- **Chaque** route vérifie l'autorisation dans le service (créateur, participant, public). Un challenge privé n'est lisible que par ses participants et invités.
- Les requêtes SQL brutes sont toujours paramétrées.
- Aucun secret dans le dépôt : Secret Manager en déploiement, `.env` local ignoré par git.
- Ne lis pas et n'affiche pas `.env` ni les fichiers de secrets.

## Workflow attendu

1. Lis la partie du README concernée. S'il y a un ❓ ou un 🟡 qui bloque, pose la question.
2. Fais un court plan avant les changements importants (plusieurs fichiers, migration, nouveau module).
3. Pour `scoring` et les règles de temps : écris d'abord les tests.
4. Implémente, puis lance format, lint et tests.
5. Fais relire par les agents adaptés (voir ci-dessous) avant de proposer un commit.
6. **Git : tu ne commits et ne pushes jamais de ta propre initiative.** À la fin d'un changement, présente la liste des fichiers modifiés, le résumé du diff et un **message de commit proposé** (en anglais, un changement cohérent par commit), puis attends l'accord explicite de l'utilisateur dans la conversation. Même après cet accord, la commande demande une confirmation à l'écran : c'est voulu.
   - Tu peux utiliser `git status`, `git diff`, `git log` et `git add` librement.
   - Commit, push, merge, rebase, tag, reset, `gh pr create/merge` : uniquement sur demande explicite de l'utilisateur, une action à la fois.
   - Push forcé, `git reset --hard`, `git clean`, `--no-verify` : interdits (bloqués par la configuration).

## Ne jamais

- Faire un commit ou un push sans validation explicite de l'utilisateur, enchaîner plusieurs commits d'un coup, ou contourner la confirmation (autre commande git équivalente, script, `git -C`, etc.).
- Lancer `terraform apply`, `terraform destroy` ou un déploiement sans demande explicite.
- Modifier une migration déjà appliquée, ou utiliser `AutoMigrate`.
- Appeler `time.Now()` dans la logique métier.
- Ajouter une dépendance sans la justifier (préfère la bibliothèque standard).
- Inventer une règle produit absente du README.
- Écrire du code d'avance : helper, méthode, package, variable de configuration ou cible `make` que le ticket en cours n'utilise pas.

## Agents et skills du projet

| Nom | Type | Quand l'utiliser |
|---|---|---|
| `next-task` | skill | Prendre la prochaine issue GitHub du backlog, l'implémenter, proposer le commit |
| `bootstrap` | skill | Aperçu des phases du projet (le détail est dans `TASKS.md`) |
| `new-module` | skill | Créer un module `internal/<nom>` |
| `new-migration` | skill | Ajouter ou modifier le schéma de la base |
| `new-endpoint` | skill | Ajouter une route (OpenAPI d'abord) |
| `scoring-rules` | skill | Toucher aux catégories, au classement ou aux règles de temps |
| `go-reviewer` | agent | Relecture du code Go après modification |
| `migration-reviewer` | agent | Relecture de toute migration |
| `spec-guardian` | agent | Vérifier que le code respecte les règles du README |
| `security-reviewer` | agent | Relecture sécurité des routes, de l'authentification et des données |
