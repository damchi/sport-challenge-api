# Déploiement continu sur GCP

Quand une pull request (une branche créée depuis `main`) est fusionnée dans `main` et que la CI est verte, l'API et le worker sont construits, poussés vers Artifact Registry et déployés en **production** sur Cloud Run, après approbation. Le staging n'est plus déployé automatiquement : on le lance à la main. Le workflow est `.github/workflows/deploy.yml` ; les étapes d'un environnement sont dans le workflow réutilisable `.github/workflows/deploy-environment.yml` (appelé une fois pour staging, une fois pour production), qui utilise l'action locale `.github/actions/deploy`.

> **Le workflow ne lance jamais `terraform apply`.** L'infrastructure (dont le compte de déploiement décrit plus bas) s'applique à la main, sur demande explicite. Le workflow ne fait que publier une image et mettre à jour Cloud Run.

## Ce qui se passe

| Événement | Staging | Production |
|---|---|---|
| Fusion dans `main`, CI verte, workflow `Release` terminé | non | le commit a reçu un tag de version `v*` (le workflow `Release` en pose un à chaque fusion) ; part **après l'approbation** de l'Environment `production` |
| `workflow_dispatch` | déploie un commit ou un tag de `main` (retour arrière, ou essai) | idem, avec la même approbation |

Le workflow ne s'exécute jamais sur une pull request. Un tag de version est créé par `release.yml` (`docs/release.md`) ; comme un tag posé avec le jeton d'un workflow ne déclenche pas d'autre workflow, c'est la fin du workflow `Release` qui lance le déploiement, et celui-ci regarde si le commit porte un tag.

Étapes d'un déploiement (action `deploy`) :

1. Authentification à GCP par **Workload Identity Federation** : le jeton OIDC du job GitHub est échangé contre les droits du compte de service de déploiement. Aucune clé JSON, aucun secret.
2. Construction de l'image avec la version, le commit et la date (`deploy/Dockerfile`), tagguée `<sha court>` et `<version>` (jamais `latest`), poussée vers Artifact Registry. Hors d'un commit tagué, la version est celle de `git describe` (`v0.3.0-2-gabc1234`).
3. **Migrations** : mise à jour de l'image du job `sport-challenge-migrate`, puis exécution avec `--wait`. Si le job échoue (migration en échec, base `dirty`), le déploiement s'arrête là (voir `docs/migrations.md`).
4. Mise à jour de l'image du service `sport-challenge-api` et des jobs `sport-challenge-worker-close-weeks` et `sport-challenge-worker-finalize-challenges`, avec l'image tagguée par le SHA court.
5. **Test de fumée** : `GET <api>/healthz` doit répondre avec le commit déployé (une douzaine d'essais, 10 s d'écart) ; sinon le workflow échoue.

Un seul déploiement à la fois par environnement (`concurrency`), et un déploiement en cours n'est jamais annulé. Les permissions sont minimales : `contents: read` partout, `id-token: write` seulement dans les jobs de déploiement. Les actions sont épinglées par SHA.

Une version taguée part en production après l'approbation, même si `main` a avancé depuis. Ces règles sont dans `.github/scripts/plan-deploy.sh`, testé par la CI. Après l'approbation, le job de production saute une version si une plus récente a été publiée entre-temps (`.github/scripts/newer-versions.sh`, testé aussi) : jamais une version plus ancienne après une plus récente.

Le code de déploiement (l'action) vient toujours de `main`, y compris pour un retour arrière : le commit à déployer n'est que ce dont l'image est construite. Chaque environnement a son propre registre : l'image est construite pour lui à partir du commit, et c'est le commit qui fait foi, pas le condensat de l'image.

## Mise en place d'un environnement

À faire une fois par environnement, **après** le premier `terraform apply` de `docs/terraform.md` (sur demande explicite).

1. **Terraform** : le module `deploy-identity` crée le pool et le fournisseur Workload Identity (seul ce dépôt est accepté, par son nom et son identifiant numérique, variable `github_repository_id`), le compte de service `sport-challenge-deploy` et ses droits : écrire dans le dépôt Artifact Registry, `roles/run.developer` sur le service et sur les jobs (dont les migrations), et l'usage des comptes de service d'exécution. Seul un job du dépôt, sur `main`, qui tourne dans l'Environment GitHub de l'environnement (`staging` ou `production`) peut prendre ce compte. La variable `github_repository` donne le dépôt (par défaut `damchi/sport-challenge-api`).

   ```
   terraform output workload_identity_provider
   terraform output deploy_service_account
   ```

2. **Environment GitHub** (Settings > Environments) du même nom que l'environnement (`staging`, `production`), avec ces variables (pas des secrets : aucune n'est sensible) :

   | Variable | Valeur |
   |---|---|
   | `GCP_PROJECT_ID` | le projet GCP de l'environnement |
   | `GCP_REGION` | `northamerica-northeast1` |
   | `GCP_WORKLOAD_IDENTITY_PROVIDER` | sortie `workload_identity_provider` |
   | `GCP_DEPLOY_SERVICE_ACCOUNT` | sortie `deploy_service_account` |

   Pour les deux Environments : **Deployment branches = Selected branches, `main` seulement**. Pour `production` : **Required reviewers** (une ou deux personnes). Sans reviewers, le déploiement en production partirait sans approbation. Côté GCP, le fournisseur Workload Identity refuse de toute façon un jeton qui ne vient pas de `main`. Idéalement, protéger aussi les tags `v*` (Settings > Rules) pour que seuls les mainteneurs en posent.
3. **Variables du dépôt** (Settings > Secrets and variables > Actions > Variables) : `DEPLOY_STAGING=true` quand l'environnement staging est prêt (il ne sert qu'aux déploiements manuels), `DEPLOY_PRODUCTION=true` quand l'Environment `production` a ses reviewers. Tant qu'elles ne valent pas `true`, le job correspondant est ignoré : une fusion n'échoue pas avant que l'infrastructure existe.
   ```
   gh variable set DEPLOY_STAGING --body true
   gh variable set DEPLOY_PRODUCTION --body true   # après avoir créé l'Environment production et ses reviewers
   ```
   **Sans ces variables, rien n'est déployé** : le workflow `Deploy` se termine en succès avec les jobs `staging` et `production` ignorés.

## Retour arrière

Onglet Actions > `Deploy` > Run workflow : choisir l'environnement et un **tag de version ou un SHA** (de `main`) dont l'image est encore dans Artifact Registry. Le workflow redéploie cette image **sans la reconstruire et sans migrations** : les migrations d'une image plus ancienne ne connaissent pas les versions plus récentes de la base. Il faut donc que le schéma actuel reste compatible avec l'ancien code (ce que vise chaque migration), sinon corriger en avant ; voir `docs/migrations.md`. Le test de fumée vérifie ensuite que le commit redéployé est bien celui qui répond. En production, la même approbation s'applique.

Le plus rapide reste de revenir à la révision précédente dans Cloud Run (`docs/migrations.md`, section Retour arrière) ; ce workflow sert à revenir à une version précise par un chemin tracé.

## Retrouver ce qui tourne

`GET <api>/healthz` donne la version et le commit (`docs/release.md`). Le déploiement lui-même est visible dans l'onglet Environments du dépôt (qui a approuvé, quand).
