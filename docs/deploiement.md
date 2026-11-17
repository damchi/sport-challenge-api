# Infrastructure et déploiement continu sur GCP

Il n'y a qu'un environnement déployé : la **production**, dans le projet GCP `sport-challenge-prod`, région Montréal (`northamerica-northeast1`). Son infrastructure a été **configurée à la main dans la console GCP** ; ce document la décrit. Il n'y a pas de staging aujourd'hui : il pourra être recréé plus tard, sur le modèle de la production.

Quand une pull request (une branche créée depuis `main`) est fusionnée dans `main` et que la CI est verte, l'API et le worker sont construits, poussés vers Artifact Registry et déployés sur Cloud Run, après approbation. Le workflow est `.github/workflows/deploy.yml`, qui utilise l'action locale `.github/actions/deploy`.

> **Le workflow ne crée ni ne modifie l'infrastructure.** Il publie une image et met à jour l'image de ressources Cloud Run qui existent déjà. Toute autre modification se fait à la main, dans la console ou avec `gcloud`, sur demande explicite, en passant toujours `--project=sport-challenge-prod`.

## Infrastructure de la production

Aucun secret ici : les valeurs sensibles sont dans Secret Manager, seuls leurs noms sont donnés.

| Ressource | Nom | Réglages |
|---|---|---|
| Service Cloud Run (API) | `sc-api` | entrée publique (`all`), 1 vCPU, 512 Mio, compte `sc-run`, connexion à `sc-db` par le connecteur Cloud SQL |
| Job de migrations | `sc-migrate` | commande `migrate up`, compte `sc-run`, connecteur Cloud SQL |
| Jobs du worker | `sc-worker-close-weeks`, `sc-worker-finalize-challenges`, `sc-worker-retry-auth0-deletions` | commande `worker <tâche>`, compte `sc-run`, connecteur Cloud SQL |
| Planifications Cloud Scheduler | `close-weeks` (`0 * * * *`), `finalize-challenges` (`10 * * * *`), `retry-auth0-deletions` (`20 * * * *`) | fuseau `America/Toronto`, `POST` sur l'API Cloud Run `…/jobs/<job>:run`, jeton OAuth du compte `sc-scheduler`, délai de 180 s |
| Base de données | Cloud SQL `sc-db` (connexion `sport-challenge-prod:northamerica-northeast1:sc-db`) | PostgreSQL 16, `db-f1-micro`, zonale ; IP publique **sans aucun réseau autorisé** (seul le connecteur Cloud SQL, authentifié par IAM, y entre), connexions chiffrées obligatoires (`ENCRYPTED_ONLY`) ; sauvegarde quotidienne à 00:00 UTC, 7 conservées, **stockées à Montréal** (`northamerica-northeast1`), restauration à un instant donné (journaux de transactions dans Cloud Storage, emplacement non vérifié) ; une sauvegarde à la demande prend **son propre** emplacement : toujours `--location=northamerica-northeast1`, sinon elle part dans la multirégion `us` ; protection contre la suppression |
| Secrets (Secret Manager) | `database-url-ca`, `auth0-mgmt-client-secret-ca` | réplication gérée, à Montréal seulement (la réplication d'un secret ne se change pas après sa création : d'où le suffixe `-ca`) ; les anciens `database-url` et `auth0-mgmt-client-secret` (réplication automatique) ont été supprimés le 2026-11-17 ; `sc-run` les lit grâce à son rôle sur le projet, **temporairement** : SC-API-102 (#169) le limite à ces deux secrets ; injectés comme variables `DATABASE_URL` et `AUTH0_MGMT_CLIENT_SECRET` dans l'API et les jobs du worker, `DATABASE_URL` seulement dans `sc-migrate` |
| Hors de Montréal (état du 2026-11-17) | — | journaux d'audit de la plateforme GCP (espace `_Required`, mondial, sans données des utilisateurs) ; journaux écrits avant le 2026-11-16, dont les adresses IP des clients, dans l'ancien espace `_Default` (mondial) jusqu'à leur expiration vers le 2026-12-16 ; les deux sauvegardes prises dans `us` le 2026-11-16, gardées par décision de l'utilisateur (l'automatique expire d'elle-même, celle à la demande `1794873476304` ne s'efface qu'à la main) ; Error Reporting, service mondial |
| Journaux (Cloud Logging) | espace `default-montreal`, récepteur `_Default` | les journaux du projet (requêtes de Cloud Run avec l'adresse IP du client, journaux de l'API) sont écrits à Montréal, 30 jours. Error Reporting, service mondial, garde ses propres regroupements d'erreurs : son emplacement n'est pas vérifié. L'espace `_Required` (journaux d'audit d'administration, 400 jours) reste en `global`, comme l'ancien espace `_Default` (vidé après 30 jours, plus rien n'y est écrit) : le projet n'a pas d'organisation, donc pas d'emplacement par défaut réglable |
| Images | Artifact Registry `sport-challenge` (Docker) | image `…/sport-challenge/api`, taguée `<sha court>` et `<version>` |
| Fédération GitHub | pool Workload Identity `github-prod`, fournisseur `github-provider` | émetteur `https://token.actions.githubusercontent.com` ; condition : dépôt `damchi/sport-challenge-api` **et** branche `main` |
| Budget | « sport challenge », sur le compte de facturation du projet | 20 CAD par mois, **sur tout le compte de facturation** (pas filtré sur `sport-challenge-prod` : les autres projets du compte comptent aussi) ; avertissements à 50 %, 90 % et 100 %, envoyés par courriel aux administrateurs du compte de facturation. Un budget ne bloque aucune dépense : il prévient seulement |
| Alertes Cloud Monitoring | canal « Production : courriel » ; politiques « API : erreurs 5xx », « Job Cloud Run en échec », « close-weeks : aucune exécution réussie depuis 3 heures » | courriel vers une adresse lue (gardée dans la console, jamais dans le dépôt) ; détail et fichiers de politique dans « Alertes » ci-dessous |

Lire le budget (l'appel est facturé à `sport-challenge-prod` grâce à `--billing-project`, sinon gcloud prendrait le projet par défaut du poste) :

```
gcloud billing budgets list --billing-account=<compte de facturation> --billing-project=sport-challenge-prod
```

Le compte de facturation se lit avec `gcloud billing projects describe sport-challenge-prod --format='value(billingAccountName)'`.

Comptes de service et droits (tous au niveau du projet) :

| Compte | Rôle dans l'application | Droits |
|---|---|---|
| `sc-run` | exécute l'API, le worker et les migrations | `roles/cloudsql.client`, `roles/secretmanager.secretAccessor` |
| `sc-scheduler` | lance les jobs du worker | `roles/run.invoker` |
| `sc-deploy` | utilisé par le workflow de déploiement (fédération GitHub) | `roles/artifactregistry.writer`, `roles/run.admin`, `roles/iam.serviceAccountUser` ; pris par les jetons du dépôt (`roles/iam.workloadIdentityUser`) |

Les variables d'environnement de l'API et des jobs sont celles que lit `internal/platform/config` (`ENV=production`, `PORT` posé par Cloud Run pour l'API, `LOG_LEVEL`, `AUTH0_DOMAIN`, `AUTH0_AUDIENCE`, `AUTH0_MGMT_CLIENT_ID`, `CORS_ALLOWED_ORIGINS`, `TRUSTED_PROXIES`, plus les deux secrets). `CLOCK_OFFSET` n'est accepté qu'avec `ENV=local` : la production le refuse.

### Connexion à la base

L'API, le worker et les migrations joignent `sc-db` **uniquement par le connecteur Cloud SQL**, jamais par son adresse IP (vérifié le 2026-11-17) :

- L'API et les quatre jobs déclarent le connecteur dans l'annotation `run.googleapis.com/cloudsql-instances: sport-challenge-prod:northamerica-northeast1:sc-db`. Cloud Run ouvre alors un socket Unix local `/cloudsql/sport-challenge-prod:northamerica-northeast1:sc-db`.
- La dernière version du secret `database-url-ca` passe par ce socket (`host=/cloudsql/…`), sans adresse IP ni `sslmode`. pgx ne chiffre pas un socket Unix, qui reste local au conteneur : c'est le connecteur qui chiffre la liaison jusqu'à `sc-db` (TLS, certificat de l'instance vérifié) et qui autorise le compte `sc-run` par IAM (`roles/cloudsql.client`).
- `sc-db` a une IP publique, mais **aucun réseau autorisé** et `ENCRYPTED_ONLY` : une connexion directe à cette IP est refusée.

Une nouvelle version de `database-url-ca` (rotation du mot de passe, `docs/runbook.md`) doit garder le socket, par exemple : `postgres://<utilisateur>:<mot de passe encodé>@/<base>?host=/cloudsql/sport-challenge-prod:northamerica-northeast1:sc-db`.

Vérifier la forme **sans afficher la valeur** : la valeur reste dans un pipe, seuls oui ou non s'affichent.

```
gcloud secrets versions access latest --secret=database-url-ca --project=sport-challenge-prod \
  | grep -q '/cloudsql/sport-challenge-prod:northamerica-northeast1:sc-db' && echo socket || echo "PAS le socket"
gcloud run services describe sc-api --region=northamerica-northeast1 --project=sport-challenge-prod \
  --format='value(spec.template.metadata.annotations."run.googleapis.com/cloudsql-instances")'
gcloud sql instances describe sc-db --project=sport-challenge-prod \
  --format='value(settings.ipConfiguration.sslMode,settings.ipConfiguration.authorizedNetworks.len())'
```

## Alertes

Trois politiques Cloud Monitoring, créées le 2026-11-17, envoient un courriel par le canal « Production : courriel ». Chacune porte une courte documentation (que lire, que faire) qui renvoie à `docs/runbook.md`. Un incident se ferme seul 30 minutes après le retour à la normale.

| Politique | Condition | Runbook |
|---|---|---|
| « API : erreurs 5xx » | service `sc-api`, `run.googleapis.com/request_count` avec `response_code_class = 5xx` : plus de 5 réponses sur 5 minutes | §1, §2 |
| « Job Cloud Run en échec » | tout job du projet (`sc-migrate`, `sc-worker-*`), `run.googleapis.com/job/completed_execution_count` avec `result = failed` : dès une exécution en échec, un incident par job | §3, §4 |
| « close-weeks : aucune exécution réussie depuis 3 heures » | `sc-worker-close-weeks`, même métrique avec `result = succeeded` : absente pendant 3 heures. La condition ne s'arme qu'après un premier succès | §4 |

Chaque alerte a été déclenchée une fois le 2026-11-17 pour vérifier la réception du courriel : une exécution de `sc-worker-finalize-challenges` avec un argument invalide (échec de job), la condition 5xx basculée un moment sur 4xx avec un seuil à 0, la durée d'absence ramenée un moment à 10 minutes ; les deux politiques ont ensuite retrouvé leurs réglages ci-dessous.

Recréer les alertes (composant `beta` de gcloud) : créer le canal, puis chaque politique depuis son fichier, après avoir remplacé `<canal>` par l'identifiant du canal. Le canal se crée de préférence dans la console (Monitoring > Alerting > Edit notification channels) : en ligne de commande, l'adresse reste dans l'historique du shell. Ne jamais coller la sortie de `channels describe`, qui contient l'adresse, dans une issue ou une pull request. Les fichiers nomment les ressources avec le préfixe `sc` (`sc-api`, `sc-worker-close-weeks`) : si `CLOUD_RUN_NAME_PREFIX` change, adapter les filtres, sinon les alertes ne voient plus rien, sans erreur.

```
gcloud beta monitoring channels create --project=sport-challenge-prod --type=email \
  --display-name="Production : courriel" --channel-labels=email_address=<adresse>
gcloud beta monitoring channels list --project=sport-challenge-prod --format='value(name,displayName)'
gcloud beta monitoring policies create --project=sport-challenge-prod --policy-from-file=<fichier>.yaml
gcloud beta monitoring policies list --project=sport-challenge-prod --format='value(name,displayName)'
```

`api-5xx.yaml` :

```yaml
displayName: "API : erreurs 5xx"
combiner: OR
conditions:
  - displayName: "sc-api : plus de 5 réponses 5xx en 5 minutes"
    conditionThreshold:
      filter: >-
        resource.type = "cloud_run_revision" AND resource.labels.service_name = "sc-api"
        AND metric.type = "run.googleapis.com/request_count"
        AND metric.labels.response_code_class = "5xx"
      aggregations:
        - alignmentPeriod: 300s
          perSeriesAligner: ALIGN_SUM
          crossSeriesReducer: REDUCE_SUM
      comparison: COMPARISON_GT
      thresholdValue: 5
      duration: 0s
      trigger:
        count: 1
alertStrategy:
  autoClose: 1800s
documentation:
  mimeType: text/markdown
  content: |-
    L'API (service Cloud Run sc-api) a renvoyé plus de 5 réponses 5xx en 5 minutes.
    Lire : Error Reporting et les journaux de sc-api (severity>=ERROR, request_id de la réponse).
    Faire : si l'erreur suit un déploiement, retour arrière (runbook §2) ; sinon vérifier /readyz et la base (runbook §1).
    Runbook : https://github.com/damchi/sport-challenge-api/blob/main/docs/runbook.md
notificationChannels:
  - projects/sport-challenge-prod/notificationChannels/<canal>
```

`job-failed.yaml` :

```yaml
displayName: "Job Cloud Run en échec"
combiner: OR
conditions:
  - displayName: "Une exécution en échec (sc-migrate, sc-worker-*)"
    conditionThreshold:
      filter: >-
        resource.type = "cloud_run_job"
        AND metric.type = "run.googleapis.com/job/completed_execution_count"
        AND metric.labels.result = "failed"
      aggregations:
        - alignmentPeriod: 300s
          perSeriesAligner: ALIGN_SUM
          crossSeriesReducer: REDUCE_SUM
          groupByFields:
            - resource.labels.job_name
      comparison: COMPARISON_GT
      thresholdValue: 0
      duration: 0s
      trigger:
        count: 1
alertStrategy:
  autoClose: 1800s
documentation:
  mimeType: text/markdown
  content: |-
    Une exécution d'un job Cloud Run a échoué (le nom du job est dans l'incident).
    Lire : Cloud Run > Jobs > le job > l'exécution en échec, et ses journaux.
    Faire : sc-migrate -> runbook §3 (migration en échec, rien n'est déployé) ; sc-worker-* -> runbook §4 (relance du worker, chaque tâche est idempotente).
    Runbook : https://github.com/damchi/sport-challenge-api/blob/main/docs/runbook.md
notificationChannels:
  - projects/sport-challenge-prod/notificationChannels/<canal>
```

`close-weeks-absent.yaml` :

```yaml
displayName: "close-weeks : aucune exécution réussie depuis 3 heures"
combiner: OR
conditions:
  - displayName: "sc-worker-close-weeks : pas de succès depuis 3 heures"
    conditionAbsent:
      filter: >-
        resource.type = "cloud_run_job" AND resource.labels.job_name = "sc-worker-close-weeks"
        AND metric.type = "run.googleapis.com/job/completed_execution_count"
        AND metric.labels.result = "succeeded"
      aggregations:
        - alignmentPeriod: 300s
          perSeriesAligner: ALIGN_SUM
          crossSeriesReducer: REDUCE_SUM
      duration: 10800s
      trigger:
        count: 1
alertStrategy:
  autoClose: 1800s
documentation:
  mimeType: text/markdown
  content: |-
    Le job sc-worker-close-weeks (lancé chaque heure par Cloud Scheduler) n'a pas réussi depuis 3 heures : les semaines ne se clôturent plus.
    Lire : Cloud Scheduler > close-weeks (dernière exécution, statut) et Cloud Run > Jobs > sc-worker-close-weeks.
    Faire : runbook §4 (relance du worker ; la tâche est idempotente).
    Runbook : https://github.com/damchi/sport-challenge-api/blob/main/docs/runbook.md
notificationChannels:
  - projects/sport-challenge-prod/notificationChannels/<canal>
```

## Ce qui se passe

| Événement | Production |
|---|---|
| Fusion dans `main`, CI verte, workflow `Release` terminé | le commit a reçu un tag de version `v*` (le workflow `Release` en pose un à chaque fusion) ; part **après l'approbation** de l'Environment `production` |
| `workflow_dispatch` (retour arrière) | redéploie un commit ou un tag de `main`, avec la même approbation |

Le workflow ne s'exécute jamais sur une pull request. Un tag de version est créé par `release.yml` (`docs/release.md`) ; comme un tag posé avec le jeton d'un workflow ne déclenche pas d'autre workflow, c'est la fin du workflow `Release` qui lance le déploiement, et celui-ci regarde si le commit porte un tag.

Étapes d'un déploiement (action `deploy`) :

1. Authentification à GCP par **Workload Identity Federation** : le jeton OIDC du job GitHub est échangé contre les droits du compte `sc-deploy`. Aucune clé JSON, aucun secret.
2. Construction de l'image avec la version, le commit et la date (`deploy/Dockerfile`), tagguée `<sha court>` et `<version>` (jamais `latest`), poussée vers Artifact Registry. Hors d'un commit tagué, la version est celle de `git describe` (`v0.3.0-2-gabc1234`).
3. **Migrations** : mise à jour de l'image du job `<préfixe>-migrate`, puis exécution avec `--wait`. Si le job échoue (migration en échec, base `dirty`), le déploiement s'arrête là (voir `docs/migrations.md`).
4. Mise à jour de l'image du service `<préfixe>-api` et des jobs `<préfixe>-worker-close-weeks`, `<préfixe>-worker-finalize-challenges` et `<préfixe>-worker-retry-auth0-deletions`, avec l'image tagguée par le SHA court. Le préfixe vaut `sc` ; la variable `CLOUD_RUN_NAME_PREFIX` de l'Environment le remplace si besoin.
5. **Test de fumée** : la révision qui sert tout le trafic doit être la dernière créée, prête, et utiliser l'image du commit déployé, puis `GET <api>/readyz` doit répondre (une douzaine d'essais, 10 s d'écart) ; sinon le workflow échoue. Sur l'URL `run.app`, Cloud Run répond lui-même à `/healthz` (404) : la requête n'atteint pas l'API.

Un seul déploiement à la fois (`concurrency`), et un déploiement en cours n'est jamais annulé. Les permissions sont minimales : `contents: read` partout, `id-token: write` seulement dans le job de déploiement. Les actions sont épinglées par SHA.

Une version taguée part en production après l'approbation, même si `main` a avancé depuis. Ces règles sont dans `.github/scripts/plan-deploy.sh`, testé par la CI. Après l'approbation, le job de production saute une version si une plus récente a été publiée entre-temps (`.github/scripts/newer-versions.sh`, testé aussi) : jamais une version plus ancienne après une plus récente.

Le code de déploiement (l'action) vient toujours de `main`, y compris pour un retour arrière : le commit à déployer n'est que ce dont l'image est construite.

## Ajouter une tâche du worker

Chaque job du worker doit exister avant qu'un déploiement le mette à jour : le job d'une nouvelle tâche, et sa planification Cloud Scheduler, se créent dans le projet **avant** la fusion du code qui ajoute la tâche à `WORKER_TASKS` (`.github/actions/deploy/action.yml`), sinon l'étape 4 échoue. Le plus sûr est de copier un job existant, par exemple pour `retry-auth0-deletions` :

```
cd "$(mktemp -d)"   # job.yaml décrit la configuration du job : jamais dans le dépôt
P=sport-challenge-prod; R=northamerica-northeast1
gcloud run jobs describe sc-worker-close-weeks --project=$P --region=$R --format=export > close-weeks.yaml
sed 's/sc-worker-close-weeks/sc-worker-retry-auth0-deletions/; s/- close-weeks$/- retry-auth0-deletions/' close-weeks.yaml > job.yaml
grep -nE 'name: sc-worker|- worker|- retry-auth0|close-weeks' job.yaml
gcloud run jobs replace job.yaml --project=$P --region=$R
gcloud scheduler jobs create http retry-auth0-deletions --project=$P --location=$R \
  --schedule="20 * * * *" --time-zone="America/Toronto" --attempt-deadline=180s --http-method=POST \
  --uri="https://run.googleapis.com/v2/projects/$P/locations/$R/jobs/sc-worker-retry-auth0-deletions:run" \
  --oauth-service-account-email="sc-scheduler@$P.iam.gserviceaccount.com"
gcloud run jobs execute sc-worker-retry-auth0-deletions --project=$P --region=$R --wait
```

- Avant le `replace`, le `grep` doit montrer le nouveau nom et `- retry-auth0-deletions` comme dernier argument, et plus aucun `close-weeks`.
- Les droits (lancement par `sc-scheduler`, mise à jour par `sc-deploy`) sont donnés au niveau du projet : le nouveau job en hérite, rien à copier.
- Les planifications sont dans le fuseau `America/Toronto` ; pour une tâche horaire, le fuseau ne change rien.
- Les lignes terminées par `\` se collent d'un bloc : un espace après le `\` coupe la commande.

## Réglages GitHub

1. **Environment GitHub** `production` (Settings > Environments), avec ces variables (pas des secrets : aucune n'est sensible) :

   | Variable | Valeur |
   |---|---|
   | `GCP_PROJECT_ID` | `sport-challenge-prod` |
   | `GCP_REGION` | `northamerica-northeast1` |
   | `GCP_WORKLOAD_IDENTITY_PROVIDER` | `projects/<numéro du projet>/locations/global/workloadIdentityPools/github-prod/providers/github-provider` |
   | `GCP_DEPLOY_SERVICE_ACCOUNT` | `sc-deploy@sport-challenge-prod.iam.gserviceaccount.com` |
   | `CLOUD_RUN_NAME_PREFIX` | facultatif : `sc` par défaut |

   **Deployment branches = Selected branches, `main` seulement**, et **Required reviewers** (une ou deux personnes). Sans reviewers, le déploiement en production partirait sans approbation. Côté GCP, le fournisseur Workload Identity refuse un jeton qui ne vient pas de `main`, mais il ne vérifie **ni l'Environment** `production` **ni l'identifiant numérique du dépôt** : tout workflow de `main` qui demande `id-token: write` peut prendre `sc-deploy`. L'approbation repose donc sur les règles GitHub (protection de `main`, reviewers de l'Environment). Idéalement, protéger aussi les tags `v*` (Settings > Rules) pour que seuls les mainteneurs en posent.
2. **Variable du dépôt** (Settings > Secrets and variables > Actions > Variables) : `DEPLOY_PRODUCTION=true` quand l'Environment `production` a ses reviewers. Tant qu'elle ne vaut pas `true`, le job est ignoré.
   ```
   gh variable set DEPLOY_PRODUCTION --body true   # après avoir créé l'Environment production et ses reviewers
   ```
   **Sans cette variable, rien n'est déployé** : le workflow `Deploy` se termine en succès avec le job `production` ignoré.

## Retour arrière

Onglet Actions > `Deploy` > Run workflow : donner un **tag de version ou un SHA** (de `main`) dont l'image est encore dans Artifact Registry. Le workflow redéploie cette image **sans la reconstruire et sans migrations** : les migrations d'une image plus ancienne ne connaissent pas les versions plus récentes de la base. Il faut donc que le schéma actuel reste compatible avec l'ancien code (ce que vise chaque migration), sinon corriger en avant ; voir `docs/migrations.md`. Le test de fumée vérifie ensuite que la révision servie utilise bien l'image redéployée. La même approbation s'applique.

Le plus rapide reste de revenir à la révision précédente dans Cloud Run (`docs/migrations.md`, section Retour arrière) ; ce workflow sert à revenir à une version précise par un chemin tracé.

## Retrouver ce qui tourne

`GET <api>/healthz` donne la version et le commit (`docs/release.md`). Le déploiement lui-même est visible dans l'onglet Environments du dépôt (qui a approuvé, quand).
