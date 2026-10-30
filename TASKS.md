# Backlog backend — Sport Challenge API

Tickets de développement du backend, rédigés comme des tickets Jira. Ils servent de feuille de route détaillée dans Claude Code : **un ticket = un commit** (deux au maximum si une migration et son code se séparent proprement).

Sources de vérité : `README.md` (règles et architecture) et `CLAUDE.md` (conventions). En cas de contradiction, le README gagne ; signale-la à l'utilisateur.

## Les tickets vivent dans GitHub

`TASKS.md` est la **source d'import** et le document de référence (conventions, décisions, épopées, critères détaillés). Une fois importés, chaque ticket est une **issue GitHub** : c'est l'issue qui fait foi pour le statut, les cases à cocher et les dépendances. **Ne mets plus à jour les statuts de ce fichier.**

Mise en place, une seule fois, par toi, depuis la racine du dépôt :

1. `gh auth login`, puis `gh auth refresh -s project` (droit d'écrire dans les GitHub Projects).
2. `python3 .claude/scripts/tasks.py import --dry-run` : liste ce qui serait créé, sans rien créer.
3. `python3 .claude/scripts/tasks.py import --done 075` (remplace par les numéros des tickets déjà faits à la main) : crée les étiquettes, une issue par ticket et le GitHub Project « Sport Challenge », partagé avec l'autre dépôt (il est retrouvé par son titre). Sûr à relancer : un ticket déjà présent n'est jamais recréé.

Correspondance avec les anciens statuts :

| Ancien statut | Dans GitHub |
|---|---|
| À faire | issue ouverte (statut « Todo » du Project) |
| En cours | étiquette `en-cours` et statut « In Progress » |
| Fait | issue **fermée**, automatiquement à la fusion de la pull request qui contient `Closes #N` |
| Reporté (V1.1) | étiquette `v1.1` |
| Bloqué | étiquette `blocked` |
| À valider | étiquette `needs-decision` |

Épopée, type, phase et points sont des étiquettes (`E1`, `type:story`, `phase:1`, `points:3`). L'ordre de traitement est le numéro d'issue (l'ordre du fichier) ; les dépendances sont écrites dans l'issue (`Dépend de : #12 (SC-API-002)`).

## Comment travailler avec les tickets dans Claude Code

1. Lance le skill `/next-task` (ou « prends le ticket SC-API-0xx ») : `python3 .claude/scripts/tasks.py next` donne la première issue ouverte dont les dépendances sont fermées, hors reportées, bloquées et en cours.
2. Claude lit l'issue (`gh issue view`), relit les sections du README citées, la marque « en cours » (`tasks.py start`) et annonce un court plan.
3. Implémentation, puis `make check` (et `make test-integration` si SQL ou migration).
4. Revue par les agents indiqués dans le ticket.
5. Claude coche les critères dans l'issue (`tasks.py tick`), puis **propose** (sans l'exécuter) les fichiers et le message de commit, qui se termine par `Closes #N`. **Aucun commit ni push sans ton accord explicite.**
6. Tu relis, tu commits, tu ouvres la pull request ; l'issue se ferme à la fusion et le ticket suivant devient disponible.

## Conventions

- **Estimation** en points : 1 (très petit), 2, 3, 5. Un ticket à 8 doit être découpé.
- **Statut** : voir le tableau de correspondance ci-dessus (issue ouverte, étiquettes `en-cours`, `v1.1`, `blocked`, `needs-decision`, issue fermée). La ligne `Statut` de chaque ticket ci-dessous ne sert qu'à l'import.
- **Types** : Story (comportement visible), Task (technique), Spike (décision ou recherche), Ops (hors code).
- **Commits** : Conventional Commits en anglais, `type(scope): sujet` à l'impératif, 72 caractères au plus. Types : `feat`, `fix`, `refactor`, `test`, `docs`, `chore`. Termine par `Closes #N` (numéro de l'issue) puis `Refs: SC-API-0xx` en pied de message.
- **Définition de terminé** (pour chaque ticket) :
  - [ ] Critères d'acceptation remplis
  - [ ] `make fmt && make lint && make test` passent (et `make test-integration` si SQL)
  - [ ] Tests ajoutés, dont les cas « interdit » et les cas limites de temps quand ils s'appliquent
  - [ ] Erreurs avec code stable ; aucune donnée personnelle dans les logs
  - [ ] Migration : paire `.up.sql` / `.down.sql`, `up → down → up` vérifié
  - [ ] **Route ajoutée ou modifiée** : `api/openapi.yaml` mis à jour **avant** le code, `make generate` et `make openapi-lint` sans différence ni erreur, réponses validées par le test de contrat
  - [ ] **Aucun code inutilisé** : chaque fonction, type, variable de configuration ou package ajouté est utilisé par ce ticket
  - [ ] Agents de revue lancés et points bloquants corrigés
  - [ ] README mis à jour **seulement** si l'utilisateur a validé un changement de règle

## Principe : pas de code d'avance

L'architecture du README §5 est la **cible**, pas un échafaudage à créer d'un coup. Un ticket ne crée un package, une fonction, un type, une variable de configuration ou une cible du `Makefile` que si **ce ticket** les utilise. Le socle (configuration, logs, erreurs, horloge, transactions, tests d'intégration, pagination, CORS, limitation de débit…) arrive donc **avec la première fonctionnalité qui en a besoin** ; la liste « Tickets fusionnés » ci-dessous montre où chaque ancien ticket de préparation est passé. Si un besoin n'était pas prévu, ajoute-le dans le ticket en cours et note-le dans le message de commit.

## Catalogue provisoire des codes d'erreur

Chaque code est ajouté à `apperr` par le premier ticket qui le renvoie.

`unauthenticated`, `invalid_token`, `token_expired`, `forbidden`, `not_found`, `validation_failed`, `rate_limited`, `internal_error`, `profile_incomplete`, `account_deleted`, `challenge_not_found`, `field_not_editable`, `registration_closed`, `already_participant`, `participation_abandoned`, `challenge_ended`, `cannot_exclude_creator`, `activity_not_found`, `activity_outside_window`, `week_locked`, `distance_not_allowed`, `distance_required`, `badge_limit_reached`, `badge_already_awarded`, `invalid_badge_rule`, `invitation_invalid`.

## Décisions prises

| # | Décision | Tickets |
|---|---|---|
| D1 ✅ | Une activité est saisie **une seule fois** pour tous les challenges de l'utilisateur ; elle compte dans chaque challenge dont la fenêtre la contient. Elle est non modifiable dès que sa semaine est terminée dans **l'un** d'eux | 040, 043, 044 |
| D2 ✅ | Tout challenge a **toujours les 3 catégories** ; le créateur règle seulement l'incrément X | 027, 029 |
| D3 ✅ | Durée = nombre **entier de semaines, de 2 à 52** | 027 |
| D4 ✅ | Statut de la catégorie 3 : « objectif atteint » ; « à risque » s'il reste 24 h ou moins ; « en cours » avant | 053 |
| D5 ✅ | Pseudo **unique sans tenir compte de la casse**, 3 à 20 caractères | 016, 021 |
| D6 ✅ | Un utilisateur peut **saisir un sport à la main** s'il n'est pas proposé (sport « autre » avec libellé libre) | 024, 041, 042 |
| D7 ✅ | La suppression de compte supprime aussi l'utilisateur **chez Auth0** | 015, 039, 057 |
| D8 ✅ | Distance d'un badge : **réelle**, sans coefficient | 060 |

### À confirmer

| # | Point | Proposition utilisée dans le backlog | Tickets |
|---|---|---|---|
| D9 ✅ | Un sport saisi à la main compte-t-il pour la **distance** ? | Oui **si une distance est saisie**, sinon non : pour `other`, la distance est **facultative** ; il compte toujours pour le temps (catégorie 1) ; avec distance, il compte pour la catégorie 2 avec un coefficient ×1 (hypothèse, à confirmer) | 024, 042, 050 |

## Tickets fusionnés (anciens tickets de préparation)

Ces numéros n'existent plus comme tickets : leur contenu est entré dans le ticket qui s'en sert en premier. Les autres numéros sont inchangés.

| Ancien ticket | Où il est passé |
|---|---|
| 004 Lint, format et premier test | 002 (le test factice est supprimé) |
| 005 Migration 000001 | 016 (première table qui utilise `set_updated_at()`) |
| 008 `platform/clock` | 029 (premier usage) ; décalage de staging au 073 |
| 009 `platform/db` | 007 (pool et ping), 019 (GORM, base de test), 029 (transactions) |
| 010 `platform/apperr` | 018 (premier code d'erreur renvoyé) |
| 011 Recovery, RequestID, Logger | 006 |
| 013 Réponses uniformes | 018 (erreurs), 021 (succès et validation), 030 (pagination) |
| 014 Serveur API | 006 et 007 |
| `platform/logger` (ancien 007) | supprimé : `slog` direct dans le serveur (006) |

Tickets déplacés ou réduits : 012 (seulement CORS), 022 (limitation de débit par IP et utilisateur, déplacée avant l'exposition publique en E9), 068 (CI, déplacée en tête), 057 (seulement `retry-auth0-deletions`), 075 (nouveau : Claude relecteur des pull requests).

## Périmètre de la V1 : aller le plus vite possible

Le frontend démarre en **web uniquement** (le mobile viendra en V2) et la V1 repose sur la saisie manuelle. Le backlog complet compte **67 tickets et 217 points** (E0 : 7 · E1 : 7 · E2 : 31 · E3 : 3 · E4 : 42 · E5 : 21 · E6 : 27 · E7 : 17 · E8 : 31 · E9 : 31).

**Décision D10 ✅** : les badges et l'upload d'avatar sont **reportés à une V1.1** pour sortir la V1 plus vite. La V1 compte donc **189 points** (217 − 28). Au lancement, les avatars sont des **initiales par défaut**.

| Reporté en V1.1 | Tickets | Points | Effet |
|---|---|---|---|
| Badges | 059 à 064 | 23 | Pas de badges au lancement ; la table et les règles viendront ensuite sans toucher au reste |
| Upload d'avatar | 023 | 5 | Avatars par défaut (initiales) au lancement |

À **garder** même pour aller vite : fondations et CI (E0, E1), authentification (E2), catalogue de sports, challenges et participation (E4), activités avec verrouillage de semaine (E5), classement des 3 catégories (E6), clôtures et résultats figés (E7), invitations (065 et 066, indispensables aux challenges privés), limitation de débit (022) et un déploiement minimal reproductible (067, 069 à 071, 073). L'observabilité (072) et le runbook (074) peuvent être réduits à l'essentiel mais pas supprimés : alerte de budget et alerte d'échec du worker comprises.

Les 7 tickets reportés ont le statut `Reporté (V1.1)` ; le skill `next-task` ne les prend jamais.

## Épopées

| Épopée | Contenu | Tickets |
|---|---|---|
| E0 | Fondations minimales et CI | 001, 003, 002, 068, 075 |
| E1 | Serveur minimal | 006, 007 |
| E2 | Utilisateurs et authentification (Auth0) | 015, 016, 017, 018, 019, 020, 012, 021, 023 |
| E3 | Catalogue de sports | 024, 025 |
| E4 | Challenges, catégories, participation | 026, 027, 028, 029, 030, 031, 032, 033, 034, 035, 036, 037, 038, 039 |
| E5 | Activités | 040, 041, 042, 043, 044, 045, 046 |
| E6 | Scoring et classement | 047, 048, 049, 050, 051, 052, 053 |
| E7 | Clôtures et worker | 054, 055, 056, 057, 058 |
| E8 | Invitations (badges reportés en V1.1) | 059, 060, 061, 062, 063, 064, 065, 066 |
| E9 | Sécurité avant exposition, déploiement et exploitation | 022, 067, 069, 070, 071, 072, 073, 074 |

---

# E0 — Fondations minimales et CI

### SC-API-001 · Initialiser le module Go
`E0` · Task · Phase 0 · Estimation 1 · Dépend de : —

**Description** : le strict minimum pour compiler. L'arborescence du README §5.2 est la **cible** : chaque dossier est créé par le premier ticket qui y met du code utile, pas avant.

**Critères d'acceptation**
- [ ] `go.mod` créé (chemin du module confirmé avec l'utilisateur).
- [ ] `.gitignore` (`.env`, `CLAUDE.local.md`, binaires), `README.md` et `CLAUDE.md` à la racine.
- [ ] **Aucun** fichier vide, `doc.go` ni code d'avance.
- [ ] `go vet ./...` passe.

**Tests** : `go vet ./...`.
**Revue** : `go-reviewer`.
**Commit** : `chore: initialize Go module`
**Statut** : À faire

### SC-API-003 · Environnement local : PostgreSQL
`E0` · Task · Phase 0 · Estimation 1 · Dépend de : 001

**Critères d'acceptation**
- [ ] `deploy/docker-compose.yml` : PostgreSQL 16 seulement (volume, healthcheck, port 5432). L'émulateur de stockage viendra avec l'upload d'avatar (V1.1).
- [ ] `.env.example` ne contient que les variables **déjà lues** par le code (ici `DATABASE_URL`) ; chaque ticket qui ajoute une variable l'ajoute aussi à ce fichier.
- [ ] Aucune valeur secrète réelle ; `.env` est ignoré par git.
- [ ] `docker compose up` démarre la base et `psql` s'y connecte.

**Tests** : démarrage manuel.
**Revue** : `go-reviewer`.
**Commit** : `chore: add local PostgreSQL compose and env example`
**Statut** : À faire

### SC-API-002 · Makefile, lint et formatage
`E0` · Task · Phase 0 · Estimation 2 · Dépend de : 001, 003

**Description** : les commandes dont on a besoin **tout de suite**. Les autres cibles seront ajoutées par les tickets qui en ont l'usage (`test-integration` au 019, `worker` au 055, `generate`, `openapi-lint` et `doc` au ticket 006, qui crée `api/openapi.yaml`).

**Critères d'acceptation**
- [ ] Cibles : `help` (par défaut), `run`, `build`, `tools`, `fmt`, `lint`, `test`, `check`, `migrate-new`, `migrate-up`, `migrate-down`, `migrate-version`, `docker-up`, `docker-down`.
- [ ] `migrate-up` et `migrate-down` **refusent** de s'exécuter si `DATABASE_URL` ne vise pas la machine locale (localhost, 127.0.0.1 ou l'hôte du compose), avec un message clair.
- [ ] `migrate-down` annule **une seule** migration. Aucune cible de déploiement. Pas de `migrate force`.
- [ ] `check` enchaîne `fmt`, `lint` et `test`.
- [ ] `tools` installe des versions fixées de `migrate` (avec le tag `postgres`), `golangci-lint` et `gofumpt`.
- [ ] `.golangci.yml` avec au minimum `govet`, `staticcheck`, `errcheck`, `gosec`, `revive`, `unused`, `ineffassign`, `bodyclose` ; `make fmt` et `make lint` passent.
- [ ] `DATABASE_URL` vaut par défaut la base du compose local ; le `.env` est chargé s'il existe.

**Notes techniques** : `.PHONY` partout. Pas de test factice : `make test` doit simplement passer tant qu'il n'y a pas de test.
**Tests** : lancer chaque cible ; vérifier que le garde-fou échoue avec une URL distante factice.
**Revue** : `go-reviewer`.
**Commit** : `chore: add Makefile, linting and local-only migration guard`
**Statut** : À faire

### SC-API-068 · CI GitHub Actions
`E0` · Task · Phase 0 · Estimation 2 · Dépend de : 002

**Description** : mise en place **tôt**, pour que chaque pull request soit testée dès le début. Le pipeline grossit avec le projet : chaque ticket qui introduit un nouveau type de vérification ajoute son étape (migrations au 016, tests d'intégration avec PostgreSQL au 019, build de l'image au 067).

**Critères d'acceptation**
- [ ] Workflow `.github/workflows/ci.yml` sur `push` et `pull_request` : installation de Go (version de `go.mod`), `make fmt` (aucune différence), `make lint`, `make test`.
- [ ] Cache des modules Go ; permissions minimales du jeton de la CI (`contents: read`).
- [ ] Protection de `main` documentée : pull request obligatoire et CI verte avant fusion (réglage fait par l'utilisateur dans GitHub).

**Revue** : `security-reviewer`.
**Commit** : `chore(ci): add GitHub Actions pipeline`
**Statut** : À faire

### SC-API-075 · [Ops] Claude comme relecteur des pull requests
`E0` · Ops · Phase 0 · Estimation 1 · Dépend de : 001

**Description** : tâche manuelle avec l'utilisateur. Elle peut déjà avoir été faite : vérifier l'existant avant de refaire quoi que ce soit.

**Critères d'acceptation**
- [ ] Application GitHub de Claude installée sur le dépôt (`/install-github-app` dans Claude Code, ou installation manuelle).
- [ ] Secret du dépôt créé (jeton de l'abonnement ou clé d'API) ; aucun secret dans le dépôt.
- [ ] Workflow `.github/workflows/claude-review.yml` sur `pull_request` (ouverture, nouveau commit, réouverture), qui relit le diff selon `CLAUDE.md` et le README.
- [ ] Vérifié sur une pull request de test : le commentaire de relecture apparaît.
- [ ] Claude commente seulement : il n'a aucun droit de fusion.

**Notes techniques** : chaque relecture consomme l'abonnement ou la clé d'API ; ne relit que le diff de la pull request, pas tout le dépôt.
**Revue** : `security-reviewer`.
**Commit** : `ci: add Claude pull request review workflow`
**Statut** : À faire

---

# E1 — Serveur minimal

### SC-API-006 · Contrat OpenAPI et serveur minimal : `GET /healthz`
`E1` · Story · Phase 1 · Estimation 5 · Dépend de : 002

**Description** : un serveur qui démarre, journalise et s'arrête proprement, rien de plus, **et le contrat OpenAPI qui le pilote** (décision : spec-first, doc toujours à jour). La configuration, les middlewares et les erreurs grossiront avec les tickets qui en ont besoin.

**Critères d'acceptation**
- [ ] `internal/platform/config` : structure `Config` avec seulement `ENV` (`local`, `staging`, `production`), `PORT` et `LOG_LEVEL`, lue depuis l'environnement ; **toutes** les variables manquantes ou invalides sont listées dans une seule erreur ; aucune valeur secrète dans les logs ni les erreurs.
- [ ] `log/slog` en JSON sur la sortie standard, compatible Cloud Logging (`severity`, `message`, `time`), installé comme logger par défaut dans `cmd/api/main.go`. Pas de package `logger` : on l'ajoutera si un besoin réel apparaît.
- [ ] Routeur Gin avec trois middlewares : `Recovery` (plantage = 500 `internal_error` générique, trace dans les logs), `RequestID` (reprend un `X-Request-ID` valide, sinon en génère un, le renvoie) et `Logger` (une ligne par requête : méthode, **modèle de route**, statut, durée, `request_id` ; jamais le jeton ni le corps).
- [ ] `GET /healthz` répond toujours 200 (corps `{"status":"ok"}`), **décrit d'abord dans le contrat** puis implémenté.
- [ ] `api/openapi.yaml` (OpenAPI 3.0.3, `info`, chemins complets : `/healthz` et `/readyz` à la racine, les routes métier sous `/api/v1`) ; le fichier est **la source** : on le modifie avant le code, jamais l'inverse.
- [ ] Génération avec `oapi-codegen` (version épinglée via `make tools`) : `api/oapi-codegen.yaml`, serveur Gin en mode strict, code généré **versionné** dans `internal/api/api.gen.go` (jamais modifié à la main) ; `GET /healthz` implémente l'interface générée.
- [ ] `make generate` régénère le code ; `make openapi-lint` valide le YAML ; `make doc` ouvre Swagger UI sur http://localhost:8082 (Docker, `api/` monté en lecture seule).
- [ ] Test de contrat : un helper de test valide la réponse réelle d'un handler contre `openapi.yaml` (`kin-openapi`) ; il est utilisé par le test de `/healthz` et le sera par chaque route.
- [ ] CI (`ci.yml`) : étapes `make openapi-lint` et `make generate` suivi de `git diff --exit-code` (échoue si le contrat et le code généré divergent).
- [ ] Arrêt propre à la réception de `SIGTERM` (compatible Cloud Run).
- [ ] `make run` démarre l'API ; `.env.example` complété avec `ENV`, `PORT`, `LOG_LEVEL`.

**Tests** : config (valide, manquant, invalide), `httptest` pour `/healthz` (avec la validation de contrat), un plantage, un identifiant valide et invalide, la ligne de log.
**Revue** : `go-reviewer`, `security-reviewer`.
**Commit** : `feat(api): add OpenAPI contract and minimal server with health endpoint`
**Statut** : À faire

### SC-API-007 · Connexion à la base et `GET /readyz`
`E1` · Story · Phase 1 · Estimation 2 · Dépend de : 003, 006

**Critères d'acceptation**
- [ ] `internal/platform/db` : pool `pgx` créé à partir de `DATABASE_URL` (ajoutée à `Config` et vérifiée au démarrage), avec fonction de ping et fermeture propre à l'arrêt.
- [ ] `GET /readyz` répond 200 si la base répond, 503 sinon ; la route est ajoutée **d'abord** à `api/openapi.yaml`, puis `make generate`.
- [ ] Aucun GORM, aucun helper de transaction ni base de test pour l'instant : ils seront ajoutés par les tickets 019 et 029 qui s'en servent.

**Tests** : `readyz` avec une base disponible et indisponible.
**Revue** : `go-reviewer`.
**Commit** : `feat(api): add database connection and readiness endpoint`
**Statut** : À faire

---

# E2 — Utilisateurs et authentification (Auth0)

### SC-API-015 · [Ops] Configurer les tenants Auth0
`E2` · Ops · Phase 2 · Estimation 3 · Dépend de : —

**Description** : tâche manuelle, à faire avec l'utilisateur. Elle peut se faire en parallèle de la phase 1.

**Critères d'acceptation**
- [ ] Un tenant par environnement (développement, staging, production), en région **Canada**. Si le Canada n'est pas disponible avec le forfait voulu : **s'arrêter et demander** (plan B : Google Identity Platform).
- [ ] Une **API** Auth0 par environnement ; son identifiant est l'audience (`AUTH0_AUDIENCE`).
- [ ] Applications : une SPA (web) et une application native (mobile), avec URL de rappel, de déconnexion et origines autorisées.
- [ ] Connexion activée : code à usage unique par e-mail, seule méthode en V1 (le New Universal Login ne propose pas le lien magique ; connexions sociales Google et Apple reportées).
- [ ] Durée de vie courte de l'access token et rotation des refresh tokens.
- [ ] Une application **machine à machine** par environnement, limitée à la permission de supprimer des utilisateurs (`delete:users`), pour l'API de gestion (ticket 039) ; ses identifiants vont dans Secret Manager.
- [ ] Aucun secret dans le dépôt ; les valeurs sont notées pour Secret Manager.
- [ ] Procédure rédigée dans `docs/auth0-setup.md`.

**Revue** : `security-reviewer`.
**Commit** : `docs(auth): document Auth0 tenant setup`
**Statut** : À faire

### SC-API-016 · Migrations `set_updated_at` et `users`
`E2` · Task · Phase 2 · Estimation 3 · Dépend de : 002, 003

**Description** : première utilisation des migrations. La fonction commune `set_updated_at()` est créée ici car la table `users` est la première à en avoir besoin.

**Critères d'acceptation**
- [ ] `000001_init_utils` : fonction `set_updated_at()` (déclencheur qui met `updated_at` à jour) ; le `.down.sql` la supprime. `gen_random_uuid()` est natif depuis PostgreSQL 13, aucune extension requise.
- [ ] `000002_create_users` : table `users` : `id uuid` (défaut `gen_random_uuid()`), `auth_subject text` unique non nul, `pseudo text` nul, `avatar_key text` nul, `language text` nul avec `CHECK` sur `fr` et `en`, `created_at`, `updated_at` (déclencheur), `anonymized_at timestamptz` nul.
- [ ] Index unique sur `lower(pseudo)` quand le pseudo n'est pas nul (D5 ✅).
- [ ] `make migrate-up` → `make migrate-down` → `make migrate-up` passent ; `make migrate-version` affiche la bonne version.
- [ ] La CI (ticket 068) vérifie `up → down → up` sur une base vide.

**Revue** : `migration-reviewer`.
**Commit** : `feat(user): add users table and updated_at trigger migrations`
**Statut** : À faire

### SC-API-017 · `platform/auth` : `TokenVerifier` et vérification Auth0
`E2` · Story · Phase 2 · Estimation 5 · Dépend de : 006

**Critères d'acceptation**
- [ ] Interface `TokenVerifier` : `Verify(ctx, rawToken) (Claims, error)`.
- [ ] Implémentation Auth0 : clés publiques (JWKS) téléchargées une fois, gardées en mémoire, rafraîchies quand une clé inconnue apparaît (avec une limite de fréquence pour éviter qu'un attaquant ne provoque des téléchargements en boucle).
- [ ] Vérifie : signature (RS256 uniquement ; `none` et HMAC refusés), émetteur (`https://{AUTH0_DOMAIN}/`), audience (`AUTH0_AUDIENCE`), expiration et début de validité avec 60 s de tolérance.
- [ ] Le jeton vérifié est l'**access token** de l'API, pas l'ID token.
- [ ] Les claims exposés se limitent à `sub`, expiration et scopes.

- [ ] `AUTH0_DOMAIN` et `AUTH0_AUDIENCE` ajoutées à `Config`, vérifiées au démarrage et listées dans `.env.example`.

**Notes techniques** : bibliothèques proposées : `golang-jwt/jwt/v5` et `MicahParks/keyfunc` ; justifier l'ajout à l'utilisateur avant d'installer.
**Tests** : clés RSA générées dans le test et faux serveur JWKS (`httptest`) : jeton valide, expiré, mauvaise audience, mauvais émetteur, mauvaise signature, algorithme `none`, clé inconnue puis rotation.
**Revue** : `go-reviewer`, `security-reviewer`.
**Commit** : `feat(auth): add JWT verifier with cached JWKS for Auth0`
**Statut** : À faire

### SC-API-018 · Middleware `Authenticate` et erreurs applicatives
`E2` · Story · Phase 2 · Estimation 3 · Dépend de : 006, 017

**Description** : premier code qui renvoie des erreurs métier. Ce ticket crée donc `platform/apperr` (écrit pour ce projet selon le contrat du README §5.8 ; le package maison de Faunadex n'est pas repris : code numérique, message traduit par le serveur, sans paramètres ni cause) et la conversion des erreurs en réponse HTTP, **avec seulement les codes utilisés ici** ; chaque ticket ajoute les siens.

**Critères d'acceptation**
- [ ] `platform/apperr` : type `Error` (code stable, statut HTTP, paramètres, cause chaînée ; `errors.Is` et `errors.As` fonctionnent ; la cause n'est jamais exposée) et les codes `unauthenticated`, `invalid_token`, `token_expired`, `internal_error`.
- [ ] `platform/httpx` : une fonction unique convertit toute erreur en `{ "error": { "code", "message", "params", "request_id" } }` ; une erreur inconnue donne 500 `internal_error` sans détail. `Recovery` (ticket 006) l'utilise.
- [ ] `Authenticate` lit `Authorization: Bearer <jeton>` ; absent ou mal formé : 401 `unauthenticated`.
- [ ] Jeton invalide : 401 `invalid_token` ; expiré : 401 `token_expired` ; l'en-tête `WWW-Authenticate: Bearer` est présent.
- [ ] En cas de succès, les claims sont placés dans le contexte.
- [ ] Le jeton n'apparaît jamais dans les logs.

**Tests** : `httptest` avec un faux `TokenVerifier` ; chaînage et `errors.As` d'`apperr` ; erreur inconnue.
**Revue** : `go-reviewer`, `security-reviewer`.
**Commit** : `feat(auth): add authentication middleware and application errors`
**Statut** : À faire

### SC-API-019 · Module `user` : récupération ou création idempotente
`E2` · Story · Phase 2 · Estimation 5 · Dépend de : 007, 016

**Description** : première interaction réelle avec la base. Ce ticket introduit GORM et l'outillage de tests d'intégration, parce que c'est lui qui s'en sert en premier.

**Critères d'acceptation**
- [ ] GORM branché sur le pool de `platform/db` (aucun `AutoMigrate`).
- [ ] Helper de test `testdb.New(t)` : crée une base temporaire depuis `TEST_DATABASE_URL`, applique les migrations, la supprime en fin de test ; `make test-integration` (tag `integration`) ; `TEST_DATABASE_URL` ajoutée à `.env.example`.
- [ ] La CI (ticket 068) lance les tests d'intégration avec un service PostgreSQL.
- [ ] `GetOrCreateBySubject(ctx, subject)` : insertion `ON CONFLICT (auth_subject) DO NOTHING`, puis lecture.
- [ ] 50 appels simultanés pour un même `sub` créent **un seul** utilisateur.
- [ ] Un utilisateur anonymisé (`anonymized_at` non nul) renvoie l'erreur `account_deleted` (code ajouté à `apperr`).
- [ ] Un nouvel utilisateur a un pseudo nul (le profil est complété ensuite).

**Tests** : intégration, dont la concurrence.
**Revue** : `go-reviewer`.
**Commit** : `feat(user): add idempotent get-or-create by auth subject`
**Statut** : À faire

### SC-API-020 · Middleware `CurrentUser` et groupes de routes
`E2` · Story · Phase 2 · Estimation 2 · Dépend de : 018, 019

**Critères d'acceptation**
- [ ] `CurrentUser` : `sub` du jeton → utilisateur local ; son identifiant est placé dans le contexte (helper `auth.UserID(ctx)`).
- [ ] Compte anonymisé : 401 `account_deleted`.
- [ ] Groupes : publiques (`/healthz`, `/readyz`) ; protégées (tout `/api/v1`) avec `Authenticate` puis `CurrentUser`.
- [ ] La connexion est obligatoire partout, y compris pour lire les challenges publics.

**Tests** : `httptest` avec faux vérificateur (sans jeton, jeton valide, compte supprimé).
**Revue** : `go-reviewer`, `security-reviewer`.
**Commit** : `feat(httpx): add current user middleware and protected route group`
**Statut** : À faire

### SC-API-012 · CORS
`E2` · Task · Phase 2 · Estimation 2 · Dépend de : 006

**Description** : nécessaire dès que le frontend appelle l'API (premier appel : `GET /me`, ticket 021).

**Critères d'acceptation**
- [ ] Origines autorisées lues dans `CORS_ALLOWED_ORIGINS` (ajoutée à `Config` et à `.env.example`), jamais `*`.
- [ ] Pré-requête `OPTIONS` répondue sans authentification ; en-têtes autorisés : `Authorization`, `Content-Type`, `Idempotency-Key`, `X-Request-ID`.
- [ ] Placé avant `Authenticate` dans la chaîne.

**Tests** : origine autorisée et refusée, pré-requête.
**Revue** : `go-reviewer`, `security-reviewer`.
**Commit** : `feat(httpx): add CORS middleware`
**Statut** : À faire

### SC-API-021 · `GET` et `PATCH /me` : profil
`E2` · Story · Phase 2 · Estimation 3 · Dépend de : 020, 012

**Critères d'acceptation**
- [ ] `GET /me` renvoie : identifiant, pseudo, avatar, langue, `profile_complete` (vrai si le pseudo est renseigné).
- [ ] `PATCH /me` modifie pseudo et langue (`fr` ou `en`). La clé d'avatar viendra avec l'upload d'avatar (ticket 023, V1.1) : en V1, l'avatar affiché est formé des initiales du pseudo.
- [ ] Pseudo : 3 à 20 caractères parmi lettres, chiffres, `_`, `.` et `-` ; unique sans tenir compte de la casse (D5 ✅) ; conflit : 409 `validation_failed` avec le champ `pseudo`.
- [ ] 🟡 Créer ou rejoindre un challenge exige un profil complet (erreur `profile_incomplete`, appliquée dans les tickets 029 et 033).

- [ ] Introduit les helpers de réponse de succès (200, 201, 204) et la conversion des erreurs de validation de requête en 400 `validation_failed` avec la liste des champs fautifs dans `params` (premier endpoint qui en a besoin).

**Tests** : validation du pseudo, unicité, `profile_complete`.
**Revue** : `go-reviewer`.
**Commit** : `feat(user): add profile read and update endpoints`
**Statut** : À faire

### SC-API-023 · `platform/storage` et upload d'avatar
`E2` · Story · Phase 2 · Estimation 5 · Dépend de : 021

**Critères d'acceptation**
- [ ] Ce ticket introduit `platform/storage`, l'émulateur `fake-gcs-server` dans le compose local et les variables `GCS_BUCKET` et `GCS_EMULATOR_HOST` (config et `.env.example`).
- [ ] Interface de stockage avec URL de dépôt signée ; implémentation Cloud Storage (URL signée V4, 5 minutes) et émulateur local.
- [ ] `POST /uploads/avatar` reçoit le type de contenu et la taille ; accepte JPEG, PNG et WebP, 2 Mo au plus ; renvoie l'URL de dépôt et la clé `avatars/{userId}/{uuid}.{ext}`.
- [ ] `PATCH /me` n'accepte une clé d'avatar que si elle commence par `avatars/{son identifiant}/`.
- [ ] Type et taille validés côté serveur ; bucket non public pour les fichiers privés.
- [ ] **CORS du bucket** configuré pour les origines du web (le navigateur envoie l'avatar directement au bucket par l'URL signée), y compris sur l'émulateur local.

**Tests** : types refusés, taille refusée, clé d'un autre utilisateur refusée.
**Revue** : `go-reviewer`, `security-reviewer`.
**Commit** : `feat(user): add signed avatar upload`
**Statut** : Reporté (V1.1)

---

# E3 — Catalogue de sports

### SC-API-024 · Migration `sport` et données de référence
`E3` · Task · Phase 3 · Estimation 2 · Dépend de : 002, 003

**Critères d'acceptation**
- [ ] Table `sport` : `code text` clé primaire, `has_distance boolean`, `distance_factor numeric(4,2)` avec `CHECK > 0`.
- [ ] Données idempotentes (`ON CONFLICT DO NOTHING`) : `run` ×1, `walk` ×1, `hike` ×1, `swim` ×1, `ride` ×0,3, `rowing` ×0,5, `roller` ×0,5, `xc_ski` ×0,5 (tous avec distance) ; `yoga`, `climbing`, `strength` sans distance ; `other` avec distance **facultative** et coefficient ×1 (D9).
- [ ] Les libellés ne sont **pas** en base : le frontend traduit les codes.
- [ ] Un utilisateur peut saisir un sport absent de la liste : il choisit `other` et renseigne un libellé libre, stocké sur l'activité (tickets 041 et 042). Pour `other`, la distance est facultative (D9 ✅).

**Points à valider** : coefficient ×1 du sport `other` quand une distance est saisie (D9).
**Revue** : `migration-reviewer`.
**Commit** : `feat(sport): add sport catalogue migration with reference data`
**Statut** : À faire

### SC-API-025 · Module `sport` : `GET /sports`
`E3` · Story · Phase 3 · Estimation 1 · Dépend de : 024, 020

**Critères d'acceptation**
- [ ] `GET /sports` renvoie code, `has_distance` et `distance_factor`.
- [ ] Réponse mise en cache côté HTTP (en-tête de durée courte).

**Tests** : intégration.
**Revue** : `go-reviewer`.
**Commit** : `feat(sport): add sports catalogue endpoint`
**Statut** : À faire

---

# E4 — Challenges, catégories, participation

### SC-API-026 · Migrations des challenges
`E4` · Task · Phase 4 · Estimation 3 · Dépend de : 016

**Critères d'acceptation**
- [ ] `challenge` : titre, description, visibilité (`CHECK` : `private` ou `public`), `members_can_invite`, `timezone`, `starts_at` et `ends_at` (`timestamptz`), `created_by`, `deleted_at`, `created_at`, `updated_at`.
- [ ] `challenge_creator` (clé : challenge, utilisateur).
- [ ] `category` : challenge, type (`CHECK` : `cumulative_time`, `cumulative_distance`, `run_elimination`), position, `params jsonb` ; unicité (challenge, type).
- [ ] `participant` : clé (challenge, utilisateur), statut (`CHECK` : `active` ou `abandoned`), `joined_at`, `abandoned_at`.
- [ ] Index : `participant (user_id)`, `category (challenge_id)`, `challenge (visibility, starts_at) WHERE deleted_at IS NULL`.
- [ ] Clés étrangères : aucune suppression en cascade des résultats (anonymisation).

**Revue** : `migration-reviewer`.
**Commit** : `feat(challenge): add challenge, category and participant tables`
**Statut** : À faire

### SC-API-027 · `challenge/rules.go` : validation pure
`E4` · Story · Phase 4 · Estimation 3 · Dépend de : 001

**Critères d'acceptation**
- [ ] Titre de 3 à 80 caractères, description jusqu'à 2000.
- [ ] Visibilité `public` ou `private` ; fuseau IANA valide ; `members_can_invite` booléen.
- [ ] Dates saisies en jours locaux ; début = minuit local du premier jour ; fin = début + N × 7 jours **calculés en heure locale** (`AddDate`, jamais 7 × 24 h) et converti en UTC. Début aujourd'hui ou plus tard.
- [ ] Durée = nombre entier de semaines, de 2 à 52 (D3 ✅).
- [ ] Un challenge a toujours les 3 catégories (D2 ✅) ; seul l'incrément X de la catégorie 3 est saisi, en mètres, strictement positif et 100 km au plus.
- [ ] Fonctions pures, sans base ni horloge système (l'instant courant est passé en paramètre).

**Tests** : table-driven ; changement d'heure (mars et novembre, `America/Toronto`) ; durée de 1 ou 53 semaines refusée.
**Revue** : `go-reviewer`, `spec-guardian`.
**Commit** : `feat(challenge): add pure challenge validation rules`
**Statut** : À faire

### SC-API-028 · `challenge/policy.go` : rôle et droit de voir
`E4` · Story · Phase 4 · Estimation 2 · Dépend de : 027

**Description** : socle de la politique d'autorisation. Seules les règles utilisées tout de suite sont écrites ; **chaque ticket suivant ajoute la sienne** (modifier, rejoindre, exclure, ajouter un créateur, supprimer, voir les activités, inviter, badges) avec ses tests « autorisé » et « interdit ».

**Critères d'acceptation**
- [ ] Rôle calculé à partir de la liste des créateurs, du statut de participant et d'une éventuelle invitation (créateur, participant actif, participant ayant abandonné, invité, extérieur).
- [ ] Fonction pure `CanView` : voir un challenge ou son classement (matrice du README §5.7).
- [ ] Une ressource privée inaccessible est signalée comme introuvable (404), pas interdite.
- [ ] Un test « autorisé » et un test « interdit » pour chaque rôle.

**Revue** : `go-reviewer`, `security-reviewer`, `spec-guardian`.
**Commit** : `feat(challenge): add role computation and view policy`
**Statut** : À faire

### SC-API-029 · Créer un challenge : `POST /challenges`
`E4` · Story · Phase 4 · Estimation 5 · Dépend de : 021, 025, 026, 027

**Critères d'acceptation**
- [ ] Profil complet exigé (`profile_incomplete`).
- [ ] Une transaction crée le challenge, le créateur, le participant (statut actif) et les 3 catégories.
- [ ] Les coefficients du catalogue de sports sont **copiés** dans les paramètres de la catégorie 2 (figés) ; la catégorie 3 reçoit `{ "sport": "run", "step_meters": X }`.
- [ ] Réponse 201 avec le challenge complet.
- [ ] Erreurs de validation : 422 avec code stable.

- [ ] Introduit `platform/clock` (interface `Clock`, implémentation réelle, `FakeClock` avec `Set` et `Advance`) : « début aujourd'hui ou plus tard » en dépend. Le décalage de staging viendra au ticket 073.
- [ ] Introduit le helper `WithTx(ctx, fn)` : validation si `fn` réussit, annulation sinon (y compris sur panique) ; la création multi-tables est son premier usage.
- [ ] Ajoute à `apperr` les codes utilisés ici (`validation_failed`, `profile_incomplete`…).

**Tests** : intégration (création complète, coefficients figés même si le catalogue change ensuite, profil incomplet).
**Revue** : `go-reviewer`, `spec-guardian`, `security-reviewer`.
**Commit** : `feat(challenge): add challenge creation endpoint`
**Statut** : À faire

### SC-API-030 · Lister les challenges : `GET /challenges`
`E4` · Story · Phase 4 · Estimation 3 · Dépend de : 029

**Critères d'acceptation**
- [ ] Paramètres : `scope` (`public` ou `mine`), `status` (`upcoming`, `ongoing`, `finished`, calculé avec l'horloge), `limit`, `cursor`.
- [ ] Pagination par curseur sur (`starts_at`, `id`).
- [ ] `public` : challenges publics non supprimés ; `mine` : ceux dont l'utilisateur est créateur ou participant.
- [ ] Éléments : identifiant, titre, visibilité, dates et fuseau, nombre de participants, mon statut.
- [ ] Le filtrage se fait dans la requête SQL ; un challenge privé n'apparaît jamais dans `public`.

- [ ] Introduit l'enveloppe de pagination `{ "items": [], "next_cursor": null }` et l'encodage du curseur dans `httpx` (premier endpoint de liste).

**Tests** : intégration (visibilité, pagination, statuts avec `FakeClock`).
**Revue** : `go-reviewer`, `security-reviewer`.
**Commit** : `feat(challenge): add challenge listing with cursor pagination`
**Statut** : À faire

### SC-API-031 · Détail d'un challenge : `GET /challenges/{id}`
`E4` · Story · Phase 4 · Estimation 3 · Dépend de : 029, 028

**Critères d'acceptation**
- [ ] Renvoie le challenge, ses catégories, mon rôle et mon statut.
- [ ] Champs calculés par le serveur : `can_join`, `registration_deadline` (fin de la semaine 1), numéro de la semaine courante, `week_ends_at`, statut du challenge.
- [ ] Challenge privé sans accès, inexistant ou supprimé : 404 `challenge_not_found`.

**Tests** : intégration ; bornes (juste avant et juste après la fin de la semaine 1).
**Revue** : `go-reviewer`, `security-reviewer`, `spec-guardian`.
**Commit** : `feat(challenge): add challenge detail endpoint with server-computed flags`
**Statut** : À faire

### SC-API-032 · Modifier titre et description : `PATCH /challenges/{id}`
`E4` · Story · Phase 4 · Estimation 2 · Dépend de : 031

**Critères d'acceptation**
- [ ] Réservé aux créateurs (sinon 403 `forbidden`).
- [ ] Seuls `title` et `description` sont modifiables, à tout moment, y compris après le début.
- [ ] Tout autre champ (catégories, dates, visibilité…) : 422 `field_not_editable`.
- [ ] Mêmes validations de longueur que la création.

- [ ] Ajoute à `policy.go` la règle `CanEdit` (créateurs seulement), avec ses tests « autorisé » et « interdit ».
**Tests** : créateur, participant (interdit), champ interdit.
**Revue** : `go-reviewer`, `spec-guardian`.
**Commit** : `feat(challenge): allow creators to edit title and description`
**Statut** : À faire

### SC-API-033 · Rejoindre un challenge : `POST /challenges/{id}/join`
`E4` · Story · Phase 4 · Estimation 5 · Dépend de : 031

**Critères d'acceptation**
- [ ] Profil complet exigé.
- [ ] Possible **jusqu'à la fin de la semaine 1** (instant exact calculé avec le fuseau) ; ensuite 422 `registration_closed`, pour tout le challenge.
- [ ] Déjà actif : 409 `already_participant` ; a déjà abandonné : 409 `participation_abandoned` (définitif).
- [ ] Challenge public : tout utilisateur connecté. Challenge privé : en attendant les invitations (ticket 066), 403 `forbidden`.
- [ ] Contrôle et écriture dans la même transaction, avec verrou sur le challenge (pas de course avec la suppression ou la limite de la semaine 1).

- [ ] Ajoute à `policy.go` la règle `CanJoin` (public : tout utilisateur connecté ; privé : invité seulement), avec ses tests « autorisé » et « interdit ».
**Tests** : une seconde avant la limite (accepté), à l'instant exact (refusé), course entre deux demandes, abandon puis nouvelle tentative.
**Revue** : `go-reviewer`, `security-reviewer`, `spec-guardian`.
**Commit** : `feat(challenge): add join endpoint with week-1 registration deadline`
**Statut** : À faire

### SC-API-034 · Abandonner : `POST /challenges/{id}/abandon`
`E4` · Story · Phase 4 · Estimation 2 · Dépend de : 033

**Critères d'acceptation**
- [ ] Le participant actif passe au statut `abandoned` (avec `abandoned_at`), éliminé de toutes les catégories, **définitivement**.
- [ ] Un créateur peut abandonner : il reste créateur.
- [ ] Déjà abandonné : réponse idempotente 200 ; challenge terminé : 422 `challenge_ended`.
- [ ] Non participant : 404.

**Tests** : abandon, répétition, après la fin, créateur.
**Revue** : `go-reviewer`, `spec-guardian`.
**Commit** : `feat(challenge): add abandon endpoint`
**Statut** : À faire

### SC-API-035 · Exclure un participant : `DELETE /challenges/{id}/participants/{userId}`
`E4` · Story · Phase 4 · Estimation 2 · Dépend de : 034

**Critères d'acceptation**
- [ ] Réservé aux créateurs.
- [ ] La cible devient `abandoned` : elle apparaît comme « a abandonné », sans mention d'exclusion.
- [ ] 🟡 Un créateur ne peut pas être exclu : 422 `cannot_exclude_creator`.
- [ ] Cible inconnue ou non participante : 404.

- [ ] Ajoute à `policy.go` la règle `CanExclude` (créateurs seulement), avec ses tests.
**Tests** : créateur, participant (interdit), cible créatrice.
**Revue** : `go-reviewer`, `security-reviewer`, `spec-guardian`.
**Commit** : `feat(challenge): allow creators to exclude participants`
**Statut** : À faire

### SC-API-036 · Ajouter un créateur : `POST /challenges/{id}/creators`
`E4` · Story · Phase 4 · Estimation 2 · Dépend de : 034

**Critères d'acceptation**
- [ ] Réservé aux créateurs ; corps : identifiant de l'utilisateur.
- [ ] 🟡 La cible doit être un participant actif ; sinon 422.
- [ ] Idempotent : ajouter un créateur existant renvoie 200.

- [ ] Ajoute à `policy.go` la règle `CanAddCreator` (créateurs seulement), avec ses tests.
**Tests** : créateur, non créateur, cible non participante.
**Revue** : `go-reviewer`, `security-reviewer`.
**Commit** : `feat(challenge): allow creators to add another creator`
**Statut** : À faire

### SC-API-037 · Supprimer un challenge : `DELETE /challenges/{id}`
`E4` · Story · Phase 4 · Estimation 3 · Dépend de : 031

**Critères d'acceptation**
- [ ] Tout créateur peut supprimer le challenge **à tout moment**, même en cours.
- [ ] 🟡 Suppression logique (`deleted_at`) : le challenge disparaît immédiatement pour tous (404) ; une purge définitive viendra selon la politique de conservation.
- [ ] Les activités restent attachées aux utilisateurs (elles sont globales).
- [ ] Le worker ignore les challenges supprimés.

- [ ] Ajoute à `policy.go` la règle `CanDelete` (créateurs seulement), avec ses tests.
**Tests** : suppression en cours, accès ensuite (404), non-créateur (403).
**Revue** : `go-reviewer`, `security-reviewer`, `spec-guardian`.
**Commit** : `feat(challenge): add challenge soft deletion`
**Statut** : À faire

### SC-API-038 · Lister les participants : `GET /challenges/{id}/participants`
`E4` · Story · Phase 4 · Estimation 2 · Dépend de : 031

**Critères d'acceptation**
- [ ] Renvoie pseudo, avatar, statut (actif ou abandonné) et date d'inscription, avec pagination par curseur.
- [ ] Visible selon la politique (challenge privé : participants et invités seulement).
- [ ] Un utilisateur anonymisé s'affiche avec un pseudo neutre prévu par le frontend (champ vide côté API).

- [ ] Ajoute à `policy.go` la règle de visibilité de la liste des participants, avec ses tests.
**Tests** : visibilité, pagination, abandonné, anonymisé.
**Revue** : `go-reviewer`, `security-reviewer`.
**Commit** : `feat(challenge): add participants listing`
**Statut** : À faire

### SC-API-039 · Supprimer son compte : `DELETE /me`
`E4` · Story · Phase 4 · Estimation 5 · Dépend de : 034, 015

**Critères d'acceptation**
- [ ] Anonymisation : pseudo effacé (et avatar, à partir de la V1.1 : le fichier est alors supprimé au mieux, en tâche de fond), `auth_subject` remplacé par une valeur de remplacement, `anonymized_at` renseigné.
- [ ] Toutes ses participations actives passent à `abandoned` (comme un abandon).
- [ ] Ses activités restent, rattachées au compte anonymisé, pour ne pas fausser les résultats.
- [ ] Les requêtes suivantes avec son jeton renvoient 401 `account_deleted`.
- [ ] Une transaction couvre l'anonymisation et les abandons.
- [ ] Après la validation de la transaction, l'utilisateur est **supprimé chez Auth0** (API de gestion). Si l'appel échoue, une ligne `pending_auth0_deletion` est créée (migration incluse dans ce ticket) et la tâche du worker `retry-auth0-deletions` (ticket 057) la rejoue jusqu'au succès.
- [ ] Ce ticket crée le client de l'API de gestion Auth0 et ses variables de configuration (ajoutées à `.env.example`).
- [ ] Les identifiants de l'API de gestion viennent de Secret Manager ; ils n'apparaissent jamais dans les logs.

**Tests** : intégration (compte avec plusieurs challenges, reconnexion).
**Revue** : `go-reviewer`, `security-reviewer`, `spec-guardian`.
**Commit** : `feat(user): add account deletion with anonymization and abandonment`
**Statut** : À faire

---

# E5 — Activités

### SC-API-040 · Documenter la décision D1 : activité globale et verrouillage
`E5` · Task · Phase 5 · Estimation 1 · Dépend de : —

**Description** : consigner la décision D1 dans un ADR, car elle conditionne les tickets 041 à 046, 052 et 062.

**Critères d'acceptation**
- [ ] `docs/adr/0001-activity-scope.md` écrit (contexte, décision, conséquences).
- [ ] Décision : une activité est saisie **une seule fois**, appartient à l'utilisateur, et compte dans **chaque challenge** dont il est participant **actif** et dont la fenêtre contient sa date de début.
- [ ] Règle de verrouillage : une activité ne peut être créée, modifiée ni supprimée si sa date de début tombe dans une **semaine terminée** d'au moins un des challenges où elle compte (challenges supprimés ignorés).
- [ ] Règle de création : une activité qui ne tombe dans la fenêtre d'aucun challenge de l'utilisateur est refusée (`activity_outside_window`). 🟡
- [ ] Conséquence documentée : les semaines diffèrent d'un challenge à l'autre ; une activité peut donc être verrouillée par un challenge et pas par un autre.

**Revue** : `spec-guardian`.
**Commit** : `docs(activity): record ADR for global activities and week locking`
**Statut** : À faire

### SC-API-041 · Migration `activity`
`E5` · Task · Phase 5 · Estimation 3 · Dépend de : 016, 024

**Critères d'acceptation**
- [ ] Table `activity` : `id uuid`, `user_id`, `sport_code` (clé étrangère vers `sport`), `sport_label text` nul, `started_at timestamptz`, `duration_seconds integer` avec `CHECK > 0`, `distance_meters integer` nul avec `CHECK >= 0`, `source text` (défaut `manual`, `CHECK`), `external_id text` nul, `idempotency_key text` nul, `created_at`, `updated_at`, `deleted_at`.
- [ ] `sport_label` est obligatoire quand `sport_code = 'other'` et interdit sinon (`CHECK`), 2 à 40 caractères.
- [ ] Index : `(user_id, started_at) WHERE deleted_at IS NULL` ; unicité partielle `(source, external_id) WHERE external_id IS NOT NULL` ; unicité partielle `(user_id, idempotency_key) WHERE idempotency_key IS NOT NULL`.
- [ ] La vitesse n'est **pas** stockée : elle est calculée.

**Revue** : `migration-reviewer`.
**Commit** : `feat(activity): add activity table migration`
**Statut** : À faire

### SC-API-042 · `activity/rules.go` : validation pure et vitesse calculée
`E5` · Story · Phase 5 · Estimation 3 · Dépend de : 001

**Critères d'acceptation**
- [ ] Durée obligatoire, strictement positive, 24 h au plus (borne technique).
- [ ] Distance obligatoire si le sport produit des km (`distance_required`), interdite pour les sports sans km (`distance_not_allowed`) ; **facultative pour le sport « autre »** (D9) ; strictement positive, 1 000 km au plus (borne technique).
- [ ] Sport « autre » : libellé obligatoire, nettoyé (espaces superflus retirés, 2 à 40 caractères, aucun caractère de contrôle), affiché plus tard comme du texte.
- [ ] Date de début jamais dans le futur (tolérance de 5 minutes).
- [ ] Fonction pure `Speed(distance, duration)` pour la vitesse et l'allure ; la vitesse n'est **jamais** fournie par le client.
- [ ] Aucun plafond de vitesse (décision produit).

**Tests** : table-driven (chaque erreur, bornes exactes, libellé de sport).
**Revue** : `go-reviewer`, `spec-guardian`.
**Commit** : `feat(activity): add pure activity validation and computed speed`
**Statut** : À faire

### SC-API-043 · `activity/policy.go` : auteur et verrouillage de semaine
`E5` · Story · Phase 5 · Estimation 3 · Dépend de : 040, 047

**Critères d'acceptation**
- [ ] Seul l'auteur peut créer, modifier ou supprimer une activité.
- [ ] Fonction pure `IsLocked(now, startedAt, challenges)` : verrouillée si, pour au moins un challenge actif où elle compte, la semaine qui contient `startedAt` est terminée (`now >= fin de semaine`).
- [ ] Le calcul des semaines passe par `scoring/window` (heure locale du challenge).
- [ ] L'**horloge** fait foi : le verrouillage ne dépend pas du passage du worker.

**Tests** : une seconde avant et à l'instant de fin de semaine ; deux challenges aux semaines décalées ; changement d'heure ; challenge supprimé ignoré ; participant abandonné ignoré.
**Revue** : `go-reviewer`, `security-reviewer`, `spec-guardian`.
**Commit** : `feat(activity): add authorization and week-lock policy`
**Statut** : À faire

### SC-API-044 · Enregistrer une activité : `POST /activities`
`E5` · Story · Phase 5 · Estimation 5 · Dépend de : 041, 042, 043, 033

**Description** : **point d'entrée unique** de création d'activité. La V2 réutilisera ce service.

**Critères d'acceptation**
- [ ] `activity.Service.Record(...)` : profil complet ; validation (ticket 042) ; fenêtre ; verrouillage (`week_locked`) ; activité hors de toute fenêtre refusée (`activity_outside_window`).
- [ ] En-tête `Idempotency-Key` : le même envoi répété renvoie la même activité sans doublon.
- [ ] Source `manual` ; la réponse contient la vitesse calculée et la liste des challenges où l'activité compte.
- [ ] Une transaction couvre l'écriture. Aucun point d'extension pour les badges : le ticket 062 (V1.1) l'ajoutera quand il en aura besoin.
- [ ] Aucune donnée personnelle dans les logs.

**Tests** : intégration (création, répétition avec clé, semaine verrouillée, hors fenêtre, deux challenges, sport « autre »).
**Revue** : `go-reviewer`, `security-reviewer`, `spec-guardian`.
**Commit** : `feat(activity): add single-entry activity recording endpoint`
**Statut** : À faire

### SC-API-045 · Modifier ou supprimer une activité : `PATCH` et `DELETE /activities/{id}`
`E5` · Story · Phase 5 · Estimation 3 · Dépend de : 044

**Critères d'acceptation**
- [ ] Auteur seulement ; l'activité d'un autre : 404.
- [ ] Verrouillage vérifié sur la date **d'origine** et sur la **nouvelle** date (`week_locked`).
- [ ] Suppression logique (`deleted_at`).
- [ ] Mêmes validations qu'à la création ; la modification ne retire aucun badge déjà gagné (décision produit).

**Tests** : modification, suppression, déplacement vers une semaine verrouillée, activité d'un autre.
**Revue** : `go-reviewer`, `security-reviewer`, `spec-guardian`.
**Commit** : `feat(activity): add activity update and deletion within unlocked weeks`
**Statut** : À faire

### SC-API-046 · Lister les activités : `GET /me/activities` et détail d'un participant
`E5` · Story · Phase 5 · Estimation 3 · Dépend de : 044, 028

**Critères d'acceptation**
- [ ] `GET /me/activities` : les activités de l'utilisateur, pagination par curseur, filtre optionnel par période.
- [ ] `GET /challenges/{id}/participants/{userId}/activities` : visible par les participants du challenge et ses créateurs ; limité aux activités de la fenêtre du challenge.
- [ ] Un non-participant d'un challenge public ne voit que le classement : 404.
- [ ] Chaque activité expose : sport (ou libellé du sport « autre »), date, durée, distance éventuelle, vitesse calculée, source, `locked`.

- [ ] Ajoute à `policy.go` la règle de visibilité des activités d'un participant, avec ses tests « autorisé » et « interdit ».
**Tests** : visibilité (participant, créateur, non-participant, challenge privé), pagination.
**Revue** : `go-reviewer`, `security-reviewer`, `spec-guardian`.
**Commit** : `feat(activity): add activity listing for self and challenge participants`
**Statut** : À faire

---

# E6 — Scoring et classement

### SC-API-047 · `scoring` : types, fenêtre et semaines
`E6` · Story · Phase 6 · Estimation 5 · Dépend de : 001

**Critères d'acceptation**
- [ ] Package en bibliothèque standard uniquement : `Activity`, `Participant`, `Window` (début, fin, fuseau, instant courant), `Entry`, interface `Scorer`.
- [ ] `Window.Weeks()`, `WeekBounds(n)` (en heure locale via `AddDate`), `WeekOf(t)`.
- [ ] Une activité compte si `startedAt ∈ [début, fin)`.
- [ ] Les participants abandonnés sont **listés mais non classés**.

**Tests** : changement d'heure du 8 mars et du 1er novembre 2026 (`America/Toronto`), première et dernière seconde de chaque semaine, challenge d'une durée de 2 et de 52 semaines.
**Revue** : `go-reviewer`, `spec-guardian`.
**Commit** : `feat(scoring): add window and week computation`
**Statut** : À faire

### SC-API-048 · `scoring/ranks.go` : classement ex aequo
`E6` · Task · Phase 6 · Estimation 2 · Dépend de : 047

**Critères d'acceptation**
- [ ] Rangs 1, 1, 3 en cas d'égalité ; pas de départage arbitraire.
- [ ] Les abandonnés n'ont pas de rang et passent après les classés.

**Tests** : égalité en tête, au milieu, à la fin, tous égaux, liste vide.
**Revue** : `go-reviewer`.
**Commit** : `feat(scoring): add tie-aware ranking`
**Statut** : À faire

### SC-API-049 · `scoring` : catégorie 1, temps cumulé
`E6` · Story · Phase 6 · Estimation 2 · Dépend de : 047, 048

**Critères d'acceptation**
- [ ] Somme des durées de **toutes** les activités, tous sports confondus (sport « autre » compris).
- [ ] Seules les activités de la fenêtre comptent ; participants sans activité à 0.

**Tests** : sports variés, hors fenêtre, égalité, participant sans activité.
**Revue** : `go-reviewer`, `spec-guardian`.
**Commit** : `feat(scoring): add cumulative time scorer`
**Statut** : À faire

### SC-API-050 · `scoring` : catégorie 2, distance cumulée
`E6` · Story · Phase 6 · Estimation 3 · Dépend de : 047, 048

**Critères d'acceptation**
- [ ] Somme des distances multipliées par le coefficient du sport, lu dans les paramètres **figés** de la catégorie.
- [ ] Les sports sans distance (yoga…) ne comptent pas ; le sport « autre » compte (×1) seulement si une distance est saisie (D9).
- [ ] Course, marche, randonnée et natation ×1 ; vélo ×0,3 ; aviron, roller et ski de fond ×0,5.

**Tests** : chaque coefficient, sport sans distance, « autre » avec et sans distance, coefficients figés malgré un changement du catalogue.
**Revue** : `go-reviewer`, `spec-guardian`.
**Commit** : `feat(scoring): add cumulative distance scorer with frozen factors`
**Statut** : À faire

### SC-API-051 · `scoring` : catégorie 3, course avec élimination
`E6` · Story · Phase 6 · Estimation 5 · Dépend de : 047, 048

**Critères d'acceptation**
- [ ] `distance(s)` = cumul des courses de la semaine s (course seule).
- [ ] Minimum de la semaine s+1 = `distance(s) + X` ; si la personne court plus que le minimum, sa distance réelle devient la nouvelle base.
- [ ] Aucune élimination en semaine 1.
- [ ] Une semaine est jugée à l'**instant exact de sa fin** (aucun délai de grâce) ; éliminé si `distance(s+1) < minimum`.
- [ ] Éliminé de **cette catégorie seulement**, définitivement.
- [ ] Classement : les non éliminés d'abord, par km cumulés ; puis les éliminés, dernier éliminé devant, puis km cumulés ; ex aequo si tout est égal.
- [ ] La règle du minimum est isolée dans **une seule fonction**.

**Tests** : exactement le minimum (non éliminé), un mètre en dessous (éliminé), dépassement du minimum (nouvelle base), semaine 1, semaine sans course, plusieurs courses dans la semaine, deux personnes aux bases différentes qui finissent (le plus de km gagne), égalité sur la semaine d'élimination, instant exact de fin de semaine.
**Revue** : `go-reviewer`, `spec-guardian`.
**Commit** : `feat(scoring): add run elimination scorer`
**Statut** : À faire

### SC-API-052 · Module `leaderboard` : assemblage
`E6` · Story · Phase 6 · Estimation 5 · Dépend de : 049, 050, 051, 044, 031

**Critères d'acceptation**
- [ ] Charge le challenge, ses catégories, ses participants et, en **une requête**, les activités des participants actifs dans la fenêtre.
- [ ] Choisit le scorer d'après le type de chaque catégorie et renvoie un classement par catégorie.
- [ ] Les participants abandonnés sont inclus avec la mention d'abandon, sans rang.
- [ ] Repose sur une petite interface définie côté `leaderboard` (aucun accès direct au repository d'un autre module).

**Tests** : avec faux dépôts (unitaires) et avec la base (intégration, plusieurs catégories, abandonné).
**Revue** : `go-reviewer`, `spec-guardian`.
**Commit** : `feat(leaderboard): add leaderboard assembly service`
**Statut** : À faire

### SC-API-053 · `GET /challenges/{id}/leaderboard` et statuts en direct
`E6` · Story · Phase 6 · Estimation 5 · Dépend de : 052

**Critères d'acceptation**
- [ ] Réponse par catégorie : rang, utilisateur (identifiant, pseudo, avatar), valeur, mention d'abandon, semaine d'élimination.
- [ ] Catégorie 3, statut en direct : `reached` si la distance de la semaine atteint le minimum ; sinon `at_risk` s'il reste 24 h ou moins avant la fin de la semaine ; sinon `in_progress` ; `eliminated` pour un éliminé ; aucun statut en semaine 1 (D4 ✅).
- [ ] Pour chaque personne : minimum de la semaine, distance déjà courue, fin de la semaine.
- [ ] Le seuil de 24 h est un paramètre de configuration.
- [ ] Heure de calcul (`computed_at`) dans la réponse.
- [ ] Visibilité selon la politique : un challenge privé n'est lisible que par ses participants et invités ; un non-participant d'un challenge public voit le classement.

**Tests** : statuts à 25 h, 24 h et 23 h de la fin ; semaine 1 ; éliminé ; visibilité ; challenge privé (404).
**Revue** : `go-reviewer`, `security-reviewer`, `spec-guardian`.
**Commit** : `feat(leaderboard): add leaderboard endpoint with live category 3 statuses`
**Statut** : À faire

---

# E7 — Clôtures et worker

### SC-API-054 · Migrations `week_closure` et `elimination`
`E7` · Task · Phase 7 · Estimation 2 · Dépend de : 026

**Critères d'acceptation**
- [ ] `week_closure (challenge_id, week_number, closed_at)`, clé (challenge, semaine).
- [ ] `elimination (challenge_id, category_id, user_id, week_number, distance_meters, required_meters)`, clé (catégorie, utilisateur).
- [ ] Clés étrangères sans suppression en cascade des résultats.
- [ ] La table `result_snapshot` est créée par le ticket 056, qui s'en sert.

**Revue** : `migration-reviewer`.
**Commit** : `feat(closure): add week closure and elimination tables`
**Statut** : À faire

### SC-API-055 · Tâche `close-weeks`
`E7` · Story · Phase 7 · Estimation 5 · Dépend de : 054, 051

**Critères d'acceptation**
- [ ] Crée `cmd/worker` avec la sous-commande `close-weeks` (même configuration et même horloge que l'API ; compatible avec un job Cloud Run), journalise le nombre d'éléments traités, code de sortie non nul en cas d'erreur ; cible `make worker TASK=close-weeks`.
- [ ] Pour chaque challenge non supprimé et non terminé, chaque semaine dont la fin est atteinte et qui n'a pas de `week_closure` : calcule les éliminations de la catégorie 3 avec le scorer, enregistre les lignes `elimination` et la `week_closure` dans **une transaction**.
- [ ] **Idempotente** : relancée, elle ne change rien.
- [ ] Le résultat ne dépend pas du moment où la tâche s'exécute (les semaines terminées sont verrouillées).
- [ ] Cohérence : un test vérifie que les éliminations enregistrées égalent celles du scorer.
- [ ] Ignore les challenges supprimés et les participants abandonnés.

**Tests** : `FakeClock`, instant exact de fin, changement d'heure, double exécution, retard d'une heure du worker.
**Revue** : `go-reviewer`, `spec-guardian`.
**Commit** : `feat(closure): add idempotent week closing task`
**Statut** : À faire

### SC-API-056 · Tâche `finalize-challenges`
`E7` · Story · Phase 7 · Estimation 5 · Dépend de : 055, 052

**Critères d'acceptation**
- [ ] Migration `result_snapshot (challenge_id, category_id, payload jsonb, created_at)`, clé (catégorie), sans suppression en cascade des résultats.
- [ ] Sous-commande `finalize-challenges` ajoutée à `cmd/worker`.
- [ ] Pour chaque challenge dont la fin est atteinte sans snapshot : enregistre le classement final de chaque catégorie dans `result_snapshot`.
- [ ] Idempotente ; aucun délai de grâce.
- [ ] Après la fin, plus aucune activité ne peut être ajoutée au challenge.

**Tests** : fin de challenge, double exécution, challenge supprimé.
**Revue** : `go-reviewer`, `spec-guardian`.
**Commit** : `feat(closure): add challenge finalization task with result snapshot`
**Statut** : À faire

### SC-API-057 · Tâche `retry-auth0-deletions`
`E7` · Task · Phase 7 · Estimation 2 · Dépend de : 039, 055

**Description** : rejoue les suppressions Auth0 en échec (décision D7). Le binaire `cmd/worker` existe depuis le ticket 055.

**Critères d'acceptation**
- [ ] Sous-commande `retry-auth0-deletions` : rejoue chaque ligne `pending_auth0_deletion` jusqu'au succès, puis la supprime.
- [ ] Idempotente ; journalise le nombre d'éléments traités ; code de sortie non nul en cas d'erreur.
- [ ] Les identifiants de gestion Auth0 ne sont jamais journalisés.

**Tests** : succès, échec puis reprise, double exécution.
**Revue** : `go-reviewer`, `security-reviewer`.
**Commit** : `feat(worker): add retry task for pending Auth0 deletions`
**Statut** : À faire

### SC-API-058 · Classement figé et verrouillage par l'horloge
`E7` · Task · Phase 7 · Estimation 3 · Dépend de : 056, 044

**Critères d'acceptation**
- [ ] Pour un challenge terminé, le classement est lu depuis `result_snapshot`.
- [ ] Test : une activité dans une semaine terminée est refusée **même si le worker n'a pas encore tourné**.
- [ ] Test : après la fin du challenge, le classement ne change plus.

**Tests** : intégration avec `FakeClock`.
**Revue** : `go-reviewer`, `spec-guardian`.
**Commit** : `feat(leaderboard): serve frozen results and verify clock-based locking`
**Statut** : À faire

---

# E8 — Invitations (badges reportés en V1.1)

### SC-API-059 · Migrations des badges
`E8` · Task · Phase 8 · Estimation 2 · Dépend de : 026

**Critères d'acceptation**
- [ ] `badge_definition` : challenge, nom, description, `rule jsonb`, créateur, `created_at`.
- [ ] `badge_award` : badge, utilisateur, `awarded_at`, activité déclencheuse nulle ; **unicité (badge, utilisateur)** ; index par utilisateur.
- [ ] La limite de 50 badges par challenge est appliquée par le service.

**Revue** : `migration-reviewer`.
**Commit** : `feat(badge): add badge definition and award tables`
**Statut** : Reporté (V1.1)

### SC-API-060 · `badge/rules.go` : évaluation pure
`E8` · Story · Phase 8 · Estimation 5 · Dépend de : 047

**Critères d'acceptation**
- [ ] Règle JSON : `{ "measure": "count|distance|duration", "scope": "challenge|week|single_activity|consecutive_weeks", "sport": "any|<code>", "threshold": nombre, "weeks": entier }`.
- [ ] Mesures : nombre d'activités, distance **réelle** (sans coefficient, D8 ✅), durée.
- [ ] Fonction pure d'évaluation : atteint ou non, et à quelle activité.
- [ ] Validation de la règle (seuil positif, sport connu ou `any`, `weeks` seulement pour `consecutive_weeks`) ; erreur `invalid_badge_rule`.
- [ ] Les portées `week`, `single_activity` et `consecutive_weeks` sont prises en charge sans changer le modèle.

**Tests** : badges à 5, 10 et 20 activités ; 100 km cumulés ; 10 heures cumulées ; 10 km sur une seule course ; 3 séances dans la semaine ; 4 semaines de suite ; filtre de sport ; seuil exact.
**Revue** : `go-reviewer`, `spec-guardian`.
**Commit** : `feat(badge): add pure badge rule validation and evaluation`
**Statut** : Reporté (V1.1)

### SC-API-061 · Service de badges : création, rétroactivité, limite
`E8` · Story · Phase 8 · Estimation 5 · Dépend de : 059, 060, 028

**Critères d'acceptation**
- [ ] Création réservée aux créateurs ; limite de **50 badges par challenge** (`badge_limit_reached`).
- [ ] À la création, le badge est évalué aussi sur les activités déjà enregistrées de chaque participant actif.
- [ ] Modification et suppression réservées aux créateurs, **seulement si personne n'a gagné le badge** (`badge_already_awarded`).
- [ ] Attribution unique par badge et utilisateur (idempotente).

- [ ] Ajoute à `policy.go` les règles de gestion des badges (créateurs seulement), avec leurs tests.
**Tests** : limite, rétroactivité, badge déjà gagné, droits.
**Revue** : `go-reviewer`, `security-reviewer`, `spec-guardian`.
**Commit** : `feat(badge): add badge management with retroactive evaluation`
**Statut** : Reporté (V1.1)

### SC-API-062 · Évaluation des badges après chaque activité
`E8` · Story · Phase 8 · Estimation 5 · Dépend de : 061, 044

**Critères d'acceptation**
- [ ] Ce ticket crée l'interface `BadgeEvaluator` et son branchement dans `activity.Service` (rien n'existait avant), appelée par `activity.Service` après l'enregistrement ou la modification d'une activité, **dans la même transaction**.
- [ ] Évalue les badges des challenges où l'activité compte et attribue immédiatement les badges atteints, sans doublon.
- [ ] Un badge gagné est **conservé** si l'activité change ou disparaît.
- [ ] Au plus 50 règles évaluées par challenge : pas de dégradation notable.

**Tests** : atteinte au moment de l'enregistrement, aucune attribution en double, conservation après suppression de l'activité, deux challenges.
**Revue** : `go-reviewer`, `spec-guardian`.
**Commit** : `feat(badge): award badges immediately after activity changes`
**Statut** : Reporté (V1.1)

### SC-API-063 · Routes des badges
`E8` · Story · Phase 8 · Estimation 3 · Dépend de : 061, 062

**Critères d'acceptation**
- [ ] `GET /me/badges`, `GET /challenges/{id}/badges` (définitions, gagné ou non, date), `POST /challenges/{id}/badges`, `PATCH` et `DELETE /challenges/{id}/badges/{badgeId}`.
- [ ] Droits selon la matrice du README §5.7 ; challenge privé : 404 pour un non-membre.

**Tests** : droits (créateur, participant, extérieur), erreurs à code stable.
**Revue** : `go-reviewer`, `security-reviewer`, `spec-guardian`.
**Commit** : `feat(badge): add badge endpoints`
**Statut** : Reporté (V1.1)

### SC-API-064 · Badges « plusieurs semaines de suite » dans `close-weeks`
`E8` · Story · Phase 8 · Estimation 3 · Dépend de : 062, 055

**Critères d'acceptation**
- [ ] À chaque clôture de semaine, évalue les badges de portée `consecutive_weeks`.
- [ ] Idempotent ; l'attribution reste unique.

**Tests** : séquence réussie, séquence interrompue, double exécution.
**Revue** : `go-reviewer`, `spec-guardian`.
**Commit** : `feat(closure): evaluate consecutive-weeks badges at week closing`
**Statut** : Reporté (V1.1)

### SC-API-065 · Invitations : migration et service
`E8` · Story · Phase 8 · Estimation 5 · Dépend de : 026, 028

**Critères d'acceptation**
- [ ] Table `invitation` : challenge, invité par, jeton **haché** (jamais en clair), expiration, `created_at`, `revoked_at`.
- [ ] Jeton aléatoire de 32 octets, non devinable ; seul son hachage est stocké.
- [ ] Création réservée aux créateurs, et aux participants si `members_can_invite` est vrai.
- [ ] 🟡 Lien réutilisable jusqu'à la fin de la semaine 1 (date limite d'inscription).

- [ ] Ajoute à `policy.go` la règle `CanInvite` (créateurs ; participants si `members_can_invite`), avec ses tests.
**Tests** : jeton invalide, expiré, révoqué ; droits de création.
**Revue** : `migration-reviewer`, `go-reviewer`, `security-reviewer`.
**Commit** : `feat(invitation): add invitation table and service`
**Statut** : À faire

### SC-API-066 · Routes d'invitation et rejoindre un challenge privé
`E8` · Story · Phase 8 · Estimation 3 · Dépend de : 065, 033

**Critères d'acceptation**
- [ ] `POST /challenges/{id}/invitations`, `GET /invitations/{token}` (aperçu limité : titre et dates), `POST /invitations/{token}/accept`.
- [ ] Jeton inconnu, expiré ou révoqué : 404 `invitation_invalid`.
- [ ] Accepter une invitation rejoint le challenge privé, sous les mêmes règles que le ticket 033 (date limite, abandon définitif).
- [ ] Le ticket 033 est complété : un challenge privé n'accepte plus de 403 systématique, seulement l'invitation valide.

**Tests** : rejoindre avec invitation valide, invalide, après la date limite, par un ancien abandonné.
**Revue** : `go-reviewer`, `security-reviewer`, `spec-guardian`.
**Commit** : `feat(invitation): add invitation endpoints and private challenge joining`
**Statut** : À faire

---

# E9 — Sécurité avant exposition, déploiement et exploitation

### SC-API-022 · Limitation de débit : par IP et par utilisateur
`E9` · Task · Phase 9 · Estimation 5 · Dépend de : 020

**Description** : protection à mettre en place **avant toute exposition publique**, une fois les routes principales en place.

**Critères d'acceptation**
- [ ] `RateLimitIP` : seau à jetons en mémoire par adresse IP (nettoyage des entrées inactives) ; au-delà, 429 `rate_limited` avec `Retry-After` ; placé avant `Authenticate`.
- [ ] Proxys de confiance déclarés (`TRUSTED_PROXIES`) pour lire la vraie IP derrière Cloud Run, sans pouvoir être falsifiée.
- [ ] `RateLimitUser` : limite par utilisateur sur les routes d'écriture (`POST`, `PATCH`, `DELETE`) ; s'ajoute à la limite par IP.
- [ ] Valeurs en configuration (`RATE_LIMIT_IP_RPS`, `RATE_LIMIT_IP_BURST`, `RATE_LIMIT_USER_RPS`, `RATE_LIMIT_USER_BURST`), ajoutées à `.env.example`.

**Tests** : dépassement par IP et par utilisateur, deux utilisateurs indépendants, `X-Forwarded-For` falsifié.
**Revue** : `go-reviewer`, `security-reviewer`.
**Commit** : `feat(httpx): add per-IP and per-user rate limiting`
**Statut** : À faire

### SC-API-067 · Dockerfile multi-stage
`E9` · Task · Phase 9 · Estimation 2 · Dépend de : 006, 055

**Critères d'acceptation**
- [ ] Image de build Go puis image minimale (binaire statique, utilisateur non root).
- [ ] Une seule image, deux commandes : `api` et `worker`.
- [ ] Aucun secret dans l'image ; taille raisonnable.
- [ ] La CI (ticket 068) construit l'image à chaque pull request.

**Tests** : `docker build` et démarrage local.
**Revue** : `security-reviewer`.
**Commit** : `feat(deploy): add multi-stage Dockerfile`
**Statut** : À faire

### SC-API-069 · Terraform : base, projets et stockage (staging)
`E9` · Ops · Phase 9 · Estimation 5 · Dépend de : 068

**Critères d'acceptation**
- [ ] `deploy/terraform/modules` : Cloud SQL (PostgreSQL, sauvegardes, accès privé), Secret Manager, Artifact Registry (Cloud Storage et son CORS viendront en V1.1 avec l'upload d'avatar).
- [ ] `envs/staging` ; `envs/prod` préparé mais non appliqué.
- [ ] Région Montréal (`northamerica-northeast1`) ; un projet GCP par environnement.
- [ ] V1.1 : CORS du bucket d'avatars pour les origines web (envoi direct depuis le navigateur), avec l'upload d'avatar.
- [ ] **Alerte de budget** dès le premier jour.
- [ ] `terraform plan` seulement : l'application se fait sur demande explicite.

**Revue** : `security-reviewer`.
**Commit** : `feat(deploy): add Terraform modules for database and storage`
**Statut** : À faire

### SC-API-070 · Terraform : Cloud Run, Scheduler et secrets
`E9` · Ops · Phase 9 · Estimation 5 · Dépend de : 069, 067

**Critères d'acceptation**
- [ ] Service Cloud Run pour l'API ; job Cloud Run pour le worker.
- [ ] Cloud Scheduler lance `close-weeks` et `finalize-challenges` **toutes les heures**.
- [ ] Secrets (base, Auth0, clés de gestion Auth0) injectés depuis Secret Manager.
- [ ] Comptes de service à droits minimaux.

**Revue** : `security-reviewer`.
**Commit** : `feat(deploy): add Terraform for Cloud Run, Scheduler and secrets`
**Statut** : À faire

### SC-API-071 · Pipeline de migrations
`E9` · Task · Phase 9 · Estimation 3 · Dépend de : 070

**Critères d'acceptation**
- [ ] Les migrations s'appliquent par un job dédié **avant** le déploiement de la nouvelle version (jamais au démarrage de l'API).
- [ ] Migration « sale » (`dirty`) : le pipeline s'arrête et prévient ; aucune version n'est forcée automatiquement.
- [ ] Procédure de retour arrière documentée.

**Revue** : `migration-reviewer`, `security-reviewer`.
**Commit** : `chore(ci): add migration job before deployment`
**Statut** : À faire

### SC-API-072 · Observabilité et alertes
`E9` · Task · Phase 9 · Estimation 3 · Dépend de : 070

**Critères d'acceptation**
- [ ] Logs lus par Cloud Logging, avec identifiant de requête.
- [ ] Error Reporting actif ; alertes sur les erreurs 5xx, l'échec du worker et le dépassement de budget.
- [ ] Suivi des tâches planifiées (une alerte si `close-weeks` ne s'est pas exécutée depuis plus de 3 heures).

**Revue** : `security-reviewer`.
**Commit** : `feat(deploy): add monitoring and alerting`
**Statut** : À faire

### SC-API-073 · Déploiement staging, horloge réglable et tests de fumée
`E9` · Ops · Phase 9 · Estimation 5 · Dépend de : 071, 072

**Critères d'acceptation**
- [ ] Ce ticket ajoute à `platform/clock` l'implémentation à décalage (`CLOCK_OFFSET`, ajoutée à `Config` et à `.env.example`) ; la configuration est **refusée** et le démarrage échoue quand `ENV=production`, avec test.
- [ ] Déploiement en staging **sur demande explicite**.
- [ ] `CLOCK_OFFSET` réglable en staging pour avancer le temps et vérifier une clôture de semaine en quelques minutes (jamais en production).
- [ ] Tests de fumée : connexion, création d'un challenge, saisie d'une activité, classement, abandon, suppression.
- [ ] Scénario complet de 3 semaines avec horloge avancée : élimination, badges, résultats figés.

**Revue** : `spec-guardian`, `security-reviewer`.
**Commit** : `docs(deploy): document staging deployment and clock offset testing`
**Statut** : À faire

### SC-API-074 · Runbook et liste de contrôle de mise en production
`E9` · Task · Phase 9 · Estimation 3 · Dépend de : 073

**Critères d'acceptation**
- [ ] `docs/runbook.md` : déploiement, retour arrière, migration en échec, relance du worker, rotation des secrets.
- [ ] Liste de contrôle avant ouverture au public : politique de confidentialité en français et en anglais (mention de la visibilité des activités), accord de traitement avec Auth0, suppression de compte testée, sauvegardes testées, quotas et budget, validation juridique de la loi 25.
- [ ] Contacts et procédure d'incident (registre des incidents).

**Revue** : `security-reviewer`.
**Commit** : `docs: add runbook and production readiness checklist`
**Statut** : À faire
