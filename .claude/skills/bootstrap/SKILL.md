---
name: bootstrap
description: Démarre le projet backend Sport Challenge ou indique la prochaine étape. À utiliser quand on dit « démarre le projet », « initialise le repo », « on commence par quoi », « quelle est la prochaine étape » ou quand le dépôt est vide ou incomplet.
---

# Démarrage du backend, phase par phase

> Le détail ticket par ticket est dans les issues GitHub (importées depuis `TASKS.md`). Pour travailler, utilise le skill `next-task` ; ce skill donne la vue d'ensemble des phases.

**Pas de code d'avance** : une phase ne crée que ce que ses tickets utilisent ; le socle (`config`, erreurs, horloge, transactions, tests d'intégration, pagination…) arrive avec la première fonctionnalité qui en a besoin.

Avance **une phase à la fois**. À la fin de chaque phase : `make fmt && make lint && make test` passent, puis **propose** (sans l'exécuter) la liste des fichiers et un message de commit, et attends la validation de l'utilisateur avant de commiter et de passer à la suivante. Ne commit et ne push jamais de toi-même. Avant de commencer, lis le `README.md` (§2 et §5) et `CLAUDE.md`, et regarde l'état du dépôt pour trouver la première phase non terminée.

## Phase 0 — Fondations minimales et CI

- `go mod init` (chemin du module à confirmer avec l'utilisateur) ; aucun `doc.go` ni fichier vide : les dossiers du README §5.2 naissent avec leur premier fichier utile.
- `Makefile` limité aux cibles immédiatement utiles (`run`, `build`, `fmt`, `lint`, `test`, `check`, migrations, `docker-up`) ; `migrate` s'appelle ainsi : `migrate -path migrations -database "$(DATABASE_URL)" up` ; création : `migrate create -ext sql -dir migrations -seq $(NAME)`.
- Installer le CLI : `go install -tags 'postgres' github.com/golang-migrate/migrate/v4/cmd/migrate@latest`.
- `.golangci.yml`, `.gitignore` (`.env`, `CLAUDE.local.md`, binaires), `.env.example` (seulement les variables déjà lues par le code).
- `deploy/docker-compose.yml` : PostgreSQL seulement (l'émulateur de stockage viendra avec l'upload d'avatar, V1.1).
- CI GitHub Actions (format, lint, tests) dès le début, et relecture des pull requests par Claude.

**Terminé quand** : `make docker-up` et `make check` fonctionnent sur une machine propre et la CI passe.

## Phase 1 — Serveur minimal

Configuration limitée aux variables utilisées, `slog` direct, Gin avec `Recovery`, `RequestID` et `Logger`, `GET /healthz`, pool `pgx` et `GET /readyz`, arrêt propre. Pas encore de GORM, d'erreurs applicatives, d'horloge ni de transactions : ils arrivent avec leur premier usage.

**Terminé quand** : l'API démarre, répond sur `/healthz` et `/readyz`.

## Phase 2 — Utilisateur et authentification

Migrations (`set_updated_at`, `users` avec l'identifiant du fournisseur `auth_subject` unique, pseudo et avatar renseignés plus tard), middlewares `Authenticate` (qui introduit `apperr` et les réponses d'erreur), `CORS` et `CurrentUser`, création de la ligne locale au premier login (insertion idempotente, qui introduit GORM et les tests d'intégration), `GET/PATCH /me`. `RateLimitIP` et `RateLimitUser` viennent en phase 9, avant l'exposition publique. Le fournisseur est **Auth0** : isole-le derrière `platform/auth` (interface `TokenVerifier`, configuration `AUTH0_DOMAIN` et `AUTH0_AUDIENCE`) et un double de test. Le jeton vérifié est l'**access token** de l'API, pas l'ID token.

## Phase 3 — Catalogue de sports

Migration `sport` avec les données de référence idempotentes (course ×1 ; marche, randonnée, natation ×1 ; vélo ×0,3 ; aviron, roller, ski de fond ×0,5 ; yoga, escalade… sans distance ; « autre » : distance facultative, ×1), `GET /sports`.

## Phase 4 — Challenges et catégories

Migrations `challenge`, `challenge_creator`, `category`, `participant`. Validation pure dans `rules.go` (fuseau, dates, paramètres de catégories), création, modification limitée (titre, description), rejoindre (jusqu'à la fin de la semaine 1), abandonner, exclure, supprimer le challenge, co-créateurs. Coefficients copiés dans le challenge à la création.

## Phase 5 — Activités

Migration `activity` (`source` avec la valeur « manuelle » en V1, `external_id` nul avec unicité (`source`, `external_id`) quand il est renseigné, clé d'idempotence, dates de création, de modification et de suppression). Un **point d'entrée unique** pour créer une activité (futur point commun des imports de la V2). Saisie manuelle, validation (durée obligatoire, distance selon le sport ; vitesse calculée, jamais saisie ; bornes techniques), fenêtre du challenge, marquage « manuelle ».

## Phase 6 — Scoring et classement

Utilise le skill `scoring-rules`. Écris les tests d'abord. Packages `scoring` (les 3 catégories : temps cumulé, distance cumulée, course avec élimination ; ex aequo) puis `leaderboard` et `GET /challenges/{id}/leaderboard`.

## Phase 7 — Clôtures et worker

Migrations `week_closure`, `elimination`, `result_snapshot`. Tâches `close-weeks` et `finalize-challenges`, idempotentes, testées avec une horloge décalée (changement d'heure, instant exact de fin de semaine).

## Phase 8 — Invitations (badges reportés en V1.1)

> Décision D10 : les badges et l'upload d'avatar sont reportés à la V1.1. En V1, ne développe que les invitations de cette phase.

`badge_definition` (par challenge, règle en JSON structuré) et `badge_award` (unique par badge et utilisateur). Évaluation **pure** des conditions dans `internal/badge/rules.go`, avec ses tests d'abord (une seule sortie, cumul sur une semaine, cumul sur le challenge, plusieurs semaines de suite, filtre de sport, seuil exact, idempotence). Déclenchement après chaque création d'activité ; les conditions « semaines de suite » sont évaluées par `close-weeks`. Routes de création, modification et suppression réservées aux créateurs, 50 badges maximum par challenge. Puis `invitation` (liens, règle « les invités peuvent inviter »).

## Phase 9 — Limitation de débit et déploiement

Limitation de débit par IP et par utilisateur, `Dockerfile` multi-stage, complément de la CI (build de l'image), Terraform (`deploy/terraform`, un projet GCP par environnement), alerte de budget, horloge réglable en staging. **Ne lance jamais `terraform apply` sans demande explicite.**

## Fin de chaque phase

Lance les agents `go-reviewer` (toujours), `migration-reviewer` (si migration), `spec-guardian` (si métier) et `security-reviewer` (si routes ou authentification), puis résume les points à décider et propose le commit (fichiers + message), sans le faire.
