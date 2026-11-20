# Infrastructure et déploiement continu sur GCP

Il n'y a qu'un environnement déployé : la **production**, dans le projet GCP `sport-challenge-prod`, région Montréal (`northamerica-northeast1`). Son infrastructure a été **configurée à la main dans la console GCP** ; ce document la décrit. Il n'y a pas de staging aujourd'hui : il pourra être recréé plus tard, sur le modèle de la production.

Quand une pull request (une branche créée depuis `main`) est fusionnée dans `main` et que la CI est verte, l'API et le worker sont construits, poussés vers Artifact Registry et déployés sur Cloud Run, après approbation. Le workflow est `.github/workflows/deploy.yml`, qui utilise l'action locale `.github/actions/deploy`.

> **Le workflow ne crée ni ne modifie l'infrastructure.** Il publie une image et met à jour l'image de ressources Cloud Run qui existent déjà. Toute autre modification se fait à la main, dans la console ou avec `gcloud`, sur demande explicite, en passant toujours `--project=sport-challenge-prod`.

## Infrastructure de la production

Aucun secret ici : les valeurs sensibles sont dans Secret Manager, seuls leurs noms sont donnés.

| Ressource | Nom | Réglages |
|---|---|---|
| Service Cloud Run (API) | `sc-api` | entrée publique (`all`), 1 vCPU, 512 Mio, concurrence 80, 0 à 3 instances (maximum fixé au niveau du service, toutes révisions comprises), délai de requête 300 s, compte `sc-run`, connexion à `sc-db` par le connecteur Cloud SQL |
| Job de migrations | `sc-migrate` | commande `migrate up`, compte `sc-run`, connecteur Cloud SQL |
| Jobs du worker | `sc-worker-close-weeks`, `sc-worker-finalize-challenges`, `sc-worker-retry-auth0-deletions` | commande `worker <tâche>`, compte `sc-run`, connecteur Cloud SQL |
| Planifications Cloud Scheduler | `close-weeks` (`0 * * * *`), `finalize-challenges` (`10 * * * *`), `retry-auth0-deletions` (`20 * * * *`) | fuseau `America/Toronto`, `POST` sur l'API Cloud Run `…/jobs/<job>:run`, jeton OAuth du compte `sc-scheduler`, délai de 180 s |
| Base de données | Cloud SQL `sc-db` (connexion `sport-challenge-prod:northamerica-northeast1:sc-db`) | PostgreSQL 16, `db-f1-micro`, zonale ; IP publique **sans aucun réseau autorisé** (seul le connecteur Cloud SQL, authentifié par IAM, y entre), connexions chiffrées obligatoires (`ENCRYPTED_ONLY`) ; sauvegarde quotidienne à 00:00 UTC, 7 conservées, **stockées à Montréal** (`northamerica-northeast1`), restauration à un instant donné (journaux de transactions dans Cloud Storage, emplacement non vérifié) ; une sauvegarde à la demande prend **son propre** emplacement : toujours `--location=northamerica-northeast1`, sinon elle part dans la multirégion `us` ; protection contre la suppression |
| Secrets (Secret Manager) | `database-url-ca`, `auth0-mgmt-client-secret-ca` | réplication gérée, à Montréal seulement (la réplication d'un secret ne se change pas après sa création : d'où le suffixe `-ca`) ; les anciens `database-url` et `auth0-mgmt-client-secret` (réplication automatique) ont été supprimés le 2026-11-17 ; `sc-run` les lit grâce à `roles/secretmanager.secretAccessor` donné sur chacun des deux (et sur rien d'autre, depuis le 2026-11-17) ; injectés comme variables `DATABASE_URL` et `AUTH0_MGMT_CLIENT_SECRET` dans l'API et les jobs du worker, `DATABASE_URL` seulement dans `sc-migrate` |
| Hors de Montréal (état du 2026-11-17) | — | journaux d'audit de la plateforme GCP (espace `_Required`, mondial, sans données des utilisateurs) ; journaux écrits avant le 2026-11-16, dont les adresses IP des clients, dans l'ancien espace `_Default` (mondial) jusqu'à leur expiration vers le 2026-12-16 ; les deux sauvegardes prises dans `us` le 2026-11-16, gardées par décision de l'utilisateur (l'automatique expire d'elle-même, celle à la demande `1794873476304` ne s'efface qu'à la main) ; Error Reporting, service mondial |
| Journaux (Cloud Logging) | espace `default-montreal`, récepteur `_Default` | les journaux du projet (requêtes de Cloud Run avec l'adresse IP du client, journaux de l'API) sont écrits à Montréal, 30 jours. Error Reporting, service mondial, garde ses propres regroupements d'erreurs : son emplacement n'est pas vérifié. L'espace `_Required` (journaux d'audit d'administration, 400 jours) reste en `global`, comme l'ancien espace `_Default` (vidé après 30 jours, plus rien n'y est écrit) : le projet n'a pas d'organisation, donc pas d'emplacement par défaut réglable |
| Images | Artifact Registry `sport-challenge` (Docker) | image `…/sport-challenge/api`, taguée `<sha court>` et `<version>` |
| Fédération GitHub | pool Workload Identity `github-prod`, fournisseur `github-provider` | émetteur `https://token.actions.githubusercontent.com` ; condition : identifiant numérique du dépôt (`1438448292`), nom `damchi/sport-challenge-api`, branche `main` **et** Environment `production` ; `sc-deploy` n'est lié qu'au sujet `repo:damchi@38217786/sport-challenge-api@1438448292:environment:production` (le dépôt utilise les sujets OIDC **immuables** de GitHub : propriétaire et dépôt suivis de leur identifiant numérique ; `gh api repos/damchi/sport-challenge-api/actions/oidc/customization/sub` le montre) |
| Budget | « sport challenge », sur le compte de facturation du projet | 20 CAD par mois, **sur tout le compte de facturation** (pas filtré sur `sport-challenge-prod` : les autres projets du compte comptent aussi) ; avertissements à 50 %, 90 % et 100 %, envoyés par courriel aux administrateurs du compte de facturation. Un budget ne bloque aucune dépense : il prévient seulement |
| Alertes Cloud Monitoring | canal « Production : courriel » ; politiques « API : erreurs 5xx », « Job Cloud Run en échec », « close-weeks : aucune exécution réussie depuis 3 heures », « retry-auth0-deletions : aucune exécution réussie depuis 6 heures », « API injoignable » ; vérification de disponibilité « API : /ready » | courriel vers une adresse lue (gardée dans la console, jamais dans le dépôt) ; détail et fichiers de politique dans « Alertes » ci-dessous |

Lire le budget (l'appel est facturé à `sport-challenge-prod` grâce à `--billing-project`, sinon gcloud prendrait le projet par défaut du poste) :

```
gcloud billing budgets list --billing-account=<compte de facturation> --billing-project=sport-challenge-prod
```

Le compte de facturation se lit avec `gcloud billing projects describe sport-challenge-prod --format='value(billingAccountName)'`.

Comptes de service et droits, donnés ressource par ressource depuis le 2026-11-17 (SC-API-102) ; seul `roles/cloudsql.client` reste au niveau du projet, faute de liaison par instance :

| Compte | Rôle dans l'application | Droits |
|---|---|---|
| `sc-run` | exécute l'API, le worker et les migrations | `roles/cloudsql.client` (projet) ; `roles/secretmanager.secretAccessor` sur `database-url-ca` et `auth0-mgmt-client-secret-ca` seulement |
| `sc-scheduler` | lance les jobs du worker | `roles/run.invoker` sur les trois jobs `sc-worker-*` seulement (pas sur `sc-migrate`) |
| `sc-deploy` | utilisé par le workflow de déploiement (fédération GitHub) | `roles/run.developer` sur `sc-api`, `sc-migrate` et les trois `sc-worker-*` ; `roles/iam.serviceAccountUser` sur `sc-run` seulement ; `roles/artifactregistry.writer` sur le dépôt `sport-challenge` ; pris seulement par les jetons du job de l'Environment `production` (`roles/iam.workloadIdentityUser`, sujet ci-dessus) |

Les variables d'environnement de l'API et des jobs sont celles que lit `internal/platform/config` (`ENV=production`, `PORT` posé par Cloud Run pour l'API, `LOG_LEVEL`, `AUTH0_DOMAIN`, `AUTH0_AUDIENCE`, `AUTH0_MGMT_CLIENT_ID`, `CORS_ALLOWED_ORIGINS`, `TRUSTED_PROXIES`, plus les deux secrets). `CLOCK_OFFSET` n'est accepté qu'avec `ENV=local` : la production le refuse. Le tenant Auth0 est unique, partagé avec le développement local (`docs/auth0-setup.md`) : `AUTH0_DOMAIN` vaut `sport-challenge-prod.ca.auth0.com` et `AUTH0_AUDIENCE` `https://api.sport-challenge`, sur l'API et les quatre jobs, avec `AUTH0_MGMT_CLIENT_ID` et le secret `auth0-mgmt-client-secret-ca` de l'application `Sport Challenge API (management)` de ce tenant. `AUTH0_MGMT_DOMAIN` (domaine du tenant pour l'API de gestion d'Auth0) n'est pas posée : elle vaut alors `AUTH0_DOMAIN`. Elle s'ajoutera à l'API et aux jobs du worker avec le domaine personnalisé Auth0 (`docs/auth0-setup.md`, étape 7).

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

### Connexions à la base : budget

Relevé le 2026-11-20 (lecture seule) :

| Valeur | Production | Source |
|---|---|---|
| `max_connections` de `sc-db` | **25** : aucun drapeau, valeur par défaut de Cloud SQL pour `db-f1-micro` ; 3 sont réservées aux superutilisateurs, il en reste **22** pour `sc-run` | `gcloud sql instances describe sc-db … --format='yaml(settings.tier,settings.databaseFlags)'` |
| Instances de `sc-api` | 3 au plus, concurrence 80 | `gcloud run services describe sc-api … --format='yaml(metadata.annotations,spec.template.spec.containerConcurrency)'` (`run.googleapis.com/maxScale`) |
| Jobs | `sc-migrate` et les trois `sc-worker-*` : parallélisme 1, une tâche ; les trois planifications sont décalées (:00, :10, :20) | `gcloud run jobs describe <job> … --format='value(spec.template.spec.parallelism,spec.template.spec.taskCount)'` |
| Paramètres de `DATABASE_URL` hors secret | socket du connecteur (`host=/cloudsql/…`), voir ci-dessus ; un éventuel `pool_max_conns` est **ignoré** : la taille vient du code | vérification sans afficher la valeur ci-dessous |

La taille de chaque pool est une constante du binaire, pas un réglage de déploiement :

| Binaire | Connexions | Où |
|---|---|---|
| API | 4 par instance × 3 instances = **12** | `poolSize`, `cmd/api/main.go` |
| Worker | 2 par job × 3 jobs (s'ils se chevauchaient tous) = **6** | `poolSize`, `cmd/worker/main.go` : une tâche enchaîne ses requêtes, une connexion suffit, la seconde est une marge |
| Migrations | **1** (golang-migrate garde une seule connexion) | `SetMaxOpenConns(1)`, `cmd/migrate/main.go` |
| **Total** | **19** sur 22 | il reste 3 connexions pour une console `psql` ou Cloud SQL Studio |

Règle : (pool de l'API × instances maximales) + (pool du worker × jobs) + migrations < connexions disponibles. **Toute hausse** du maximum d'instances, d'un pool ou un changement de gamme de `sc-db` refait ce calcul avant d'être appliquée (un `db-g1-small` passe à 50).

Quand le pool est plein, une requête attend une connexion, mais pas sans limite : chaque requête de l'API a une échéance de 25 s (`requestTimeout`, `cmd/api/main.go`, sous le `WriteTimeout` de 30 s), après quoi l'attente s'arrête et la requête répond `500 internal_error`. Après la suppression d'un compte, l'appel à Auth0 et le retrait de la suppression en attente se détachent de la requête (`context.WithoutCancel`, `internal/user/service.go`) pour aller au bout même si le client part, mais restent bornés : 10 s pour l'appel à Auth0, 5 s pour le retrait, attente du pool comprise (`pendingRemovalTimeout`). Un retrait hors délai est journalisé en avertissement et la suppression répond quand même ; le worker `retry-auth0-deletions` rejoue alors une suppression déjà faite, sans effet. Les jobs du worker n'ont pas d'échéance propre : une tâche seule ne peut pas épuiser son pool, et Cloud Run arrête un job à son délai d'exécution.

Vérifier les paramètres du pool dans le secret **sans afficher la valeur** (seuls leurs noms s'affichent) :

```
gcloud secrets versions access latest --secret=database-url-ca --project=sport-challenge-prod \
  | grep -o 'pool_[a-z_]*' || echo "aucun paramètre pool_"
```

## Alertes

Cinq politiques Cloud Monitoring, créées le 2026-11-17 (la cinquième le 2026-11-20), envoient un courriel par le canal « Production : courriel ». Chacune porte une courte documentation (que lire, que faire) qui renvoie à `docs/runbook.md`. Un incident se ferme seul 30 minutes après le retour à la normale.

| Politique | Condition | Runbook |
|---|---|---|
| « API : erreurs 5xx » | service `sc-api`, `run.googleapis.com/request_count` avec `response_code_class = 5xx` : plus de 5 réponses sur 5 minutes | §1, §2 |
| « Job Cloud Run en échec » | tout job du projet (`sc-migrate`, `sc-worker-*`), `run.googleapis.com/job/completed_execution_count` avec `result = failed` : dès une exécution en échec, un incident par job | §3, §4 |
| « close-weeks : aucune exécution réussie depuis 3 heures » | `sc-worker-close-weeks`, même métrique avec `result = succeeded` : absente pendant 3 heures. La condition ne s'arme qu'après un premier succès | §4 |
| « retry-auth0-deletions : aucune exécution réussie depuis 6 heures » | `sc-worker-retry-auth0-deletions`, même métrique avec `result = succeeded` : absente pendant 6 heures, soit six exécutions horaires manquées ou en échec d'affilée. Couvre la tâche qui ne tourne plus (planification absente, en pause ou cassée : aucune exécution, donc aucun échec) et l'échec permanent (secret Auth0 révoqué, droits retirés), dont l'incident « Job Cloud Run en échec » se ferme et se rouvre chaque heure. La condition ne s'arme qu'après un premier succès. Ajoutée le 2026-11-20 (SC-API-127) | §4, §7 |
| « API injoignable » | vérification de disponibilité « API : /ready » (`GET /ready` de `sc-api` en HTTPS, 200 attendu en 10 s, toutes les 5 minutes, depuis les régions par défaut de Cloud Monitoring), `monitoring.googleapis.com/uptime_check/check_passed` : en échec depuis plus d'une région pendant 10 minutes. Couvre l'API injoignable (aucune requête n'arrive, donc aucun 5xx) et la base injoignable (`/ready` répond 503) | §1, §2 |

Chaque alerte a été déclenchée une fois le 2026-11-17 pour vérifier la réception du courriel : une exécution de `sc-worker-finalize-challenges` avec un argument invalide (échec de job), la condition 5xx basculée un moment sur 4xx avec un seuil à 0, la durée d'absence ramenée un moment à 10 minutes ; les deux politiques ont ensuite retrouvé leurs réglages ci-dessous. « API injoignable » a été déclenchée le même jour en faisant pointer la vérification un moment vers un chemin inexistant (404), puis remise sur son chemin. Le 2026-11-17 aussi (SC-API-106), la sonde est passée de `/readyz` à `/ready` (Cloud Run intercepte `/healthz` et peut intercepter d'autres chemins finissant par `z`) : pour qu'aucune fausse alerte ne parte entre le déploiement et le changement de la vérification, celle-ci (même identifiant) est passée sur `/ready` sous le nom « API : /ready » **avant** la fusion, en acceptant 200 ou 401 (la route inconnue répondait 401), puis n'a plus accepté que 200 une fois la nouvelle version déployée. Une API réellement en panne (pas de réponse, 5xx, 503) alertait pendant toute la transition.

« retry-auth0-deletions : aucune exécution réussie depuis 6 heures » a été créée le 2026-11-20 avec une absence ramenée à 10 minutes : le courriel est arrivé, puis la durée est revenue à 6 heures. La politique s'est armée sur les succès des heures précédentes, sans attendre l'exécution suivante.

Recréer les alertes (composant `beta` de gcloud) : créer le canal, puis chaque politique depuis son fichier, après avoir remplacé `<canal>` par l'identifiant du canal. Le canal se crée de préférence dans la console (Monitoring > Alerting > Edit notification channels) : en ligne de commande, l'adresse reste dans l'historique du shell. Ne jamais coller la sortie de `channels describe`, qui contient l'adresse, dans une issue ou une pull request. Les fichiers nomment les ressources avec le préfixe `sc` (`sc-api`, `sc-worker-close-weeks`) : si `CLOUD_RUN_NAME_PREFIX` change, adapter les filtres, sinon les alertes ne voient plus rien, sans erreur.

```
gcloud beta monitoring channels create --project=sport-challenge-prod --type=email \
  --display-name="Production : courriel" --channel-labels=email_address=<adresse>
gcloud beta monitoring channels list --project=sport-challenge-prod --format='value(name,displayName)'
gcloud beta monitoring policies create --project=sport-challenge-prod --policy-from-file=<fichier>.yaml
gcloud beta monitoring policies list --project=sport-challenge-prod --format='value(name,displayName)'
# Vérification de disponibilité : son identifiant (<check_id>) va dans le filtre de api-unreachable.yaml
gcloud monitoring uptime create "API : /ready" --project=sport-challenge-prod --resource-type=uptime-url \
  --resource-labels=host=<hôte run.app de sc-api>,project_id=sport-challenge-prod --protocol=https --path=/ready \
  --request-method=get --port=443 --validate-ssl=true --status-codes=200 --period=5 --timeout=10
gcloud monitoring uptime list-configs --project=sport-challenge-prod --format='value(name,displayName)'
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
    Faire : si l'erreur suit un déploiement, retour arrière (runbook §2) ; sinon vérifier /ready et la base (runbook §1).
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

`retry-auth0-deletions-absent.yaml` :

```yaml
displayName: "retry-auth0-deletions : aucune exécution réussie depuis 6 heures"
combiner: OR
conditions:
  - displayName: "sc-worker-retry-auth0-deletions : pas de succès depuis 6 heures"
    conditionAbsent:
      filter: >-
        resource.type = "cloud_run_job" AND resource.labels.job_name = "sc-worker-retry-auth0-deletions"
        AND metric.type = "run.googleapis.com/job/completed_execution_count"
        AND metric.labels.result = "succeeded"
      aggregations:
        - alignmentPeriod: 300s
          perSeriesAligner: ALIGN_SUM
          crossSeriesReducer: REDUCE_SUM
      duration: 21600s
      trigger:
        count: 1
alertStrategy:
  autoClose: 1800s
documentation:
  mimeType: text/markdown
  content: |-
    Le job sc-worker-retry-auth0-deletions (lancé chaque heure par Cloud Scheduler) n'a pas réussi depuis 6 heures : les comptes supprimés dans l'API restent chez Auth0 (identité et e-mail), l'effacement promis n'est pas fait.
    Lire : Cloud Scheduler > retry-auth0-deletions (dernière exécution, statut) et Cloud Run > Jobs > sc-worker-retry-auth0-deletions (journaux, champ failed).
    Faire : runbook §4 (secret Auth0, planification, relance ; la tâche est idempotente) ; si des identités sont restées chez Auth0, noter l'incident au registre (runbook §7).
    Runbook : https://github.com/damchi/sport-challenge-api/blob/main/docs/runbook.md
notificationChannels:
  - projects/sport-challenge-prod/notificationChannels/<canal>
```

`api-unreachable.yaml` :

```yaml
displayName: "API injoignable"
combiner: OR
conditions:
  - displayName: "/ready échoue depuis plusieurs régions pendant 10 minutes"
    conditionThreshold:
      filter: >-
        resource.type = "uptime_url"
        AND metric.type = "monitoring.googleapis.com/uptime_check/check_passed"
        AND metric.labels.check_id = "<check_id>"
      aggregations:
        - alignmentPeriod: 1200s
          perSeriesAligner: ALIGN_NEXT_OLDER
          crossSeriesReducer: REDUCE_COUNT_FALSE
          groupByFields:
            - resource.label.*
      comparison: COMPARISON_GT
      thresholdValue: 1
      duration: 600s
      trigger:
        count: 1
alertStrategy:
  autoClose: 1800s
documentation:
  mimeType: text/markdown
  content: |-
    La vérification de disponibilité « API : /ready » échoue depuis plusieurs régions depuis 10 minutes : l'API sc-api ne répond plus, ou sa base ne répond plus (/ready renvoie 503).
    Lire : Cloud Run > sc-api (révisions, trafic, journaux au démarrage) ; Monitoring > Uptime checks (code reçu, régions).
    Faire : si la panne suit un déploiement, retour arrière (runbook §2) ; sinon vérifier la base sc-db et la dernière révision (runbook §1).
    Runbook : https://github.com/damchi/sport-challenge-api/blob/main/docs/runbook.md
notificationChannels:
  - projects/sport-challenge-prod/notificationChannels/<canal>
```

## Ce qui se passe

| Événement | Production |
|---|---|
| Fusion dans `main`, CI verte, workflow `Release` terminé | le commit a reçu un tag de version `v*` (le workflow `Release` en pose un à chaque fusion qui change du code ; une fusion de documentation seule n'en reçoit pas, et le job `production` est alors sauté, sans approbation) ; part **après l'approbation** de l'Environment `production` |
| `workflow_dispatch` (retour arrière) | redéploie un commit ou un tag de `main`, avec la même approbation |

Le workflow ne s'exécute jamais sur une pull request. Un tag de version est créé par `release.yml` (`docs/release.md`) ; comme un tag posé avec le jeton d'un workflow ne déclenche pas d'autre workflow, c'est la fin du workflow `Release` qui lance le déploiement, et celui-ci regarde si le commit porte un tag.

Étapes d'un déploiement (action `deploy`) :

1. Authentification à GCP par **Workload Identity Federation** : le jeton OIDC du job GitHub est échangé contre les droits du compte `sc-deploy`. Aucune clé JSON, aucun secret.
2. Construction de l'image avec la version, le commit et la date (`deploy/Dockerfile`), tagguée `<sha court>` et `<version>` (jamais `latest`), poussée vers Artifact Registry. Hors d'un commit tagué, la version est celle de `git describe` (`v0.3.0-2-gabc1234`).
3. **Migrations** : mise à jour de l'image du job `<préfixe>-migrate`, puis exécution avec `--wait`. Si le job échoue (migration en échec, base `dirty`), le déploiement s'arrête là (voir `docs/migrations.md`).
4. Mise à jour de l'image du service `<préfixe>-api` et des jobs `<préfixe>-worker-close-weeks`, `<préfixe>-worker-finalize-challenges` et `<préfixe>-worker-retry-auth0-deletions`, avec l'image tagguée par le SHA court. Le préfixe vaut `sc` ; la variable `CLOUD_RUN_NAME_PREFIX` de l'Environment le remplace si besoin.
5. **Test de fumée** : la révision qui sert tout le trafic doit être la dernière créée, prête, et utiliser l'image du commit déployé, puis `GET <api>/ready` doit répondre (une douzaine d'essais, 10 s d'écart) ; sinon le workflow échoue. Sur l'URL `run.app`, Cloud Run répond lui-même à `/healthz` (404) : la requête n'atteint pas l'API, d'où les sondes `/health` et `/ready`, sans « z » final.

Un seul déploiement à la fois (`concurrency`), et un déploiement en cours n'est jamais annulé. Les permissions sont minimales : `contents: read` partout, `id-token: write` seulement dans le job de déploiement. Les actions sont épinglées par SHA.

Une version taguée part en production après l'approbation, même si `main` a avancé depuis. Ces règles sont dans `.github/scripts/plan-deploy.sh`, testé par la CI. Après l'approbation, le job de production saute une version si une plus récente a été publiée entre-temps (`.github/scripts/newer-versions.sh`, testé aussi) : jamais une version plus ancienne après une plus récente.

Le code de déploiement (l'action) vient toujours de `main`, y compris pour un retour arrière : le commit à déployer n'est que ce dont l'image est construite.

### Droits : les refaire

Commandes appliquées le 2026-11-17 (SC-API-102). Ordre à respecter pour ne jamais casser le déploiement : ne rien changer pendant un déploiement ; ajouter le droit sur la ressource, **attendre quelques minutes** (propagation des droits IAM), puis seulement retirer celui du projet. Les jetons d'accès déjà émis restent valides jusqu'à une heure après un retrait. Le sujet de la liaison doit être exactement celui des jetons (sujet immuable, voir le tableau) : le 2026-11-17, une première liaison au format `repo:damchi/sport-challenge-api:environment:production` ne correspondait à aucun jeton, et un déploiement en cours a échoué au push de l'image, sans rien déployer. Les retraits ci-dessous ne visent que les rôles listés, sans condition (`--condition=None`) : la vérification finale montre tout autre rôle resté sur le projet.

```
P=sport-challenge-prod; R=northamerica-northeast1; PN=<numéro du projet>
POOL=projects/$PN/locations/global/workloadIdentityPools/github-prod
DEPLOY=serviceAccount:sc-deploy@$P.iam.gserviceaccount.com
RUN=serviceAccount:sc-run@$P.iam.gserviceaccount.com
SCH=serviceAccount:sc-scheduler@$P.iam.gserviceaccount.com

# Fédération : le seul job de l'Environment production, sur main, de ce dépôt
gcloud iam workload-identity-pools providers update-oidc github-provider --workload-identity-pool=github-prod \
  --location=global --project=$P --attribute-condition="assertion.repository_id == '1438448292' && \
assertion.repository == 'damchi/sport-challenge-api' && assertion.ref == 'refs/heads/main' && \
assertion.environment == 'production'"
gcloud iam service-accounts add-iam-policy-binding sc-deploy@$P.iam.gserviceaccount.com --project=$P \
  --role=roles/iam.workloadIdentityUser \
  --member="principal://iam.googleapis.com/$POOL/subject/repo:damchi@38217786/sport-challenge-api@1438448292:environment:production"

# sc-deploy
gcloud run services add-iam-policy-binding sc-api --project=$P --region=$R --member=$DEPLOY --role=roles/run.developer
for j in sc-migrate sc-worker-close-weeks sc-worker-finalize-challenges sc-worker-retry-auth0-deletions; do
  gcloud run jobs add-iam-policy-binding $j --project=$P --region=$R --member=$DEPLOY --role=roles/run.developer
done
gcloud iam service-accounts add-iam-policy-binding sc-run@$P.iam.gserviceaccount.com --project=$P \
  --member=$DEPLOY --role=roles/iam.serviceAccountUser
gcloud artifacts repositories add-iam-policy-binding sport-challenge --location=$R --project=$P \
  --member=$DEPLOY --role=roles/artifactregistry.writer

# sc-run : les deux secrets seulement
for s in database-url-ca auth0-mgmt-client-secret-ca; do
  gcloud secrets add-iam-policy-binding $s --project=$P --member=$RUN --role=roles/secretmanager.secretAccessor
done

# sc-scheduler : les trois jobs du worker seulement
for j in sc-worker-close-weeks sc-worker-finalize-challenges sc-worker-retry-auth0-deletions; do
  gcloud run jobs add-iam-policy-binding $j --project=$P --region=$R --member=$SCH --role=roles/run.invoker
done

# Puis seulement : retirer les droits du projet (et l'ancienne liaison au dépôt entier)
gcloud iam service-accounts remove-iam-policy-binding sc-deploy@$P.iam.gserviceaccount.com --project=$P \
  --role=roles/iam.workloadIdentityUser \
  --member="principalSet://iam.googleapis.com/$POOL/attribute.repository/damchi/sport-challenge-api"
for r in roles/run.admin roles/iam.serviceAccountUser roles/artifactregistry.writer; do
  gcloud projects remove-iam-policy-binding $P --member=$DEPLOY --role=$r --condition=None
done
gcloud projects remove-iam-policy-binding $P --member=$RUN --role=roles/secretmanager.secretAccessor --condition=None
gcloud projects remove-iam-policy-binding $P --member=$SCH --role=roles/run.invoker --condition=None
```

Vérifier :

- `gcloud projects get-iam-policy $P` ne montre plus, pour ces trois comptes, que `roles/cloudsql.client` de `sc-run` ;
- un déploiement passe encore : `gh workflow run deploy.yml --ref main -f ref=<tag déjà en production>` (retour arrière sur la même version : mise à jour de l'API et des jobs, lecture de la révision, test de fumée), puis le premier déploiement complet après une fusion (push de l'image, `sc-migrate`) ;
- chaque planification lance encore son job : `gcloud scheduler jobs run <tâche> --project=$P --location=$R`, puis la dernière exécution du job est réussie.

Ce que `sc-deploy` peut faire avec ces droits : `roles/run.developer` met à jour, lance (y compris avec d'autres arguments) et supprime le service et les jobs, mais ne change pas leur IAM (`sc-deploy` ne peut ni rendre l'API publique ni se donner d'autres droits) ; avec `iam.serviceAccountUser` sur `sc-run`, il peut faire tourner n'importe quelle image sous l'identité de `sc-run` (les deux secrets, la base). C'est le pouvoir normal d'un compte de déploiement : la protection est l'accès au compte (fédération limitée au job de l'Environment `production`, reviewers de l'Environment). `roles/artifactregistry.writer` pousse des images mais n'en supprime pas.

## Ajouter une tâche du worker

Chaque job du worker doit exister avant qu'un déploiement le mette à jour : le job d'une nouvelle tâche, et sa planification Cloud Scheduler, se créent dans le projet **avant** la fusion du code qui ajoute la tâche à `WORKER_TASKS` (`.github/actions/deploy/action.yml`), sinon l'étape 4 échoue. Le plus sûr est de copier un job existant, par exemple pour `retry-auth0-deletions` :

```
cd "$(mktemp -d)"   # job.yaml décrit la configuration du job : jamais dans le dépôt
P=sport-challenge-prod; R=northamerica-northeast1
gcloud run jobs describe sc-worker-close-weeks --project=$P --region=$R --format=export > close-weeks.yaml
sed 's/sc-worker-close-weeks/sc-worker-retry-auth0-deletions/; s/- close-weeks$/- retry-auth0-deletions/' close-weeks.yaml > job.yaml
grep -nE 'name: sc-worker|- worker|- retry-auth0|close-weeks' job.yaml
gcloud run jobs replace job.yaml --project=$P --region=$R
# Droits donnés job par job : replace ne les copie pas
gcloud run jobs add-iam-policy-binding sc-worker-retry-auth0-deletions --project=$P --region=$R \
  --member=serviceAccount:sc-scheduler@$P.iam.gserviceaccount.com --role=roles/run.invoker
gcloud run jobs add-iam-policy-binding sc-worker-retry-auth0-deletions --project=$P --region=$R \
  --member=serviceAccount:sc-deploy@$P.iam.gserviceaccount.com --role=roles/run.developer
gcloud scheduler jobs create http retry-auth0-deletions --project=$P --location=$R \
  --schedule="20 * * * *" --time-zone="America/Toronto" --attempt-deadline=180s --http-method=POST \
  --uri="https://run.googleapis.com/v2/projects/$P/locations/$R/jobs/sc-worker-retry-auth0-deletions:run" \
  --oauth-service-account-email="sc-scheduler@$P.iam.gserviceaccount.com"
gcloud run jobs execute sc-worker-retry-auth0-deletions --project=$P --region=$R --wait
```

- Avant le `replace`, le `grep` doit montrer le nouveau nom et `- retry-auth0-deletions` comme dernier argument, et plus aucun `close-weeks`.
- Les droits sont donnés job par job : `replace` ne les copie pas, d'où les deux `add-iam-policy-binding` juste après lui ; sans eux, la planification et le déploiement échouent (403).
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

   **Deployment branches = Selected branches, `main` seulement**, et **Required reviewers** (une ou deux personnes). Sans reviewers, le déploiement en production partirait sans approbation. Côté GCP, le fournisseur Workload Identity n'accepte que le jeton d'un job de l'Environment `production`, sur `main`, venant de ce dépôt (identifiant numérique compris : un dépôt recréé sous le même nom est refusé) ; un autre workflow de `main` qui demande `id-token: write` ne peut plus prendre `sc-deploy`. L'approbation elle-même repose sur les reviewers de l'Environment, réglés côté GitHub : décocher **Allow administrators to bypass configured protection rules** et, si possible, cocher **Prevent self-review**, sinon un administrateur approuve seul. Idéalement, protéger aussi les tags `v*` (Settings > Rules) pour que seuls les mainteneurs en posent.
2. **Variable du dépôt** (Settings > Secrets and variables > Actions > Variables) : `DEPLOY_PRODUCTION=true` quand l'Environment `production` a ses reviewers. Tant qu'elle ne vaut pas `true`, le job est ignoré.
   ```
   gh variable set DEPLOY_PRODUCTION --body true   # après avoir créé l'Environment production et ses reviewers
   ```
   **Sans cette variable, rien n'est déployé** : le workflow `Deploy` se termine en succès avec le job `production` ignoré.

## Retour arrière

Onglet Actions > `Deploy` > Run workflow : donner un **tag de version ou un SHA** (de `main`) dont l'image est encore dans Artifact Registry. Le workflow redéploie cette image **sans la reconstruire et sans migrations** : les migrations d'une image plus ancienne ne connaissent pas les versions plus récentes de la base. Il faut donc que le schéma actuel reste compatible avec l'ancien code (ce que vise chaque migration), sinon corriger en avant ; voir `docs/migrations.md`. Le test de fumée vérifie ensuite que la révision servie utilise bien l'image redéployée. La même approbation s'applique.

Le plus rapide reste de revenir à la révision précédente dans Cloud Run (`docs/migrations.md`, section Retour arrière) ; ce workflow sert à revenir à une version précise par un chemin tracé.

## Retrouver ce qui tourne

`GET <api>/health` donne la version et le commit (`docs/release.md`). Le déploiement lui-même est visible dans l'onglet Environments du dépôt (qui a approuvé, quand).
