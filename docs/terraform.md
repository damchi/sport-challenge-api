# Terraform : infrastructure GCP

Cette procédure décrit l'infrastructure de base de chaque environnement (`deploy/terraform/envs/staging` et `prod`) : réseau privé, Cloud SQL (PostgreSQL 16), Secret Manager, Artifact Registry, alerte de budget, puis l'API sur Cloud Run, le worker (jobs Cloud Run) et Cloud Scheduler. Le stockage des avatars (Cloud Storage et son CORS) viendra avec la V1.1.

> **Jamais de `terraform apply` sans demande explicite.** Ce qui suit s'arrête au `plan`.

## Ce qui est créé

| Module | Ressources |
|---|---|
| `network` | réseau VPC dédié, sous-réseau de Cloud Run, plage d'adresses et connexion privée aux services Google |
| `cloud-sql` | instance PostgreSQL 16, base `sport_challenge`, utilisateur `sport` (mot de passe généré), IP privée seule, TLS obligatoire, sauvegardes quotidiennes et récupération à un instant donné (7 jours) |
| `secrets` | secrets `database-url` et `auth0-mgmt-client-secret` (conteneurs vides, stockés dans la région de l'environnement) |
| `artifact-registry` | dépôt Docker `sport-challenge` |
| `cloud-run` | service de l'API (public, le JWT est vérifié par l'API), un job Cloud Run par tâche du worker, deux comptes de service (API et worker) |
| `cloud-run` (migrations) | job `sport-challenge-migrate` (`migrate up`), avec son compte de service qui ne lit que `DATABASE_URL` : voir `docs/migrations.md` |
| `scheduler` | une planification Cloud Scheduler par tâche, et un compte de service qui ne peut que lancer ces jobs |
| `monitoring` | canal d'alerte par courriel et trois alertes : erreurs 5xx de l'API, échec d'un job (worker ou migrations), `close-weeks` sans succès depuis 3 heures |
| `budget` | budget mensuel du projet, alertes à 50 %, 90 % et 100 % des dépenses, par courriel |

Le secret `database-url` reçoit sa valeur de Terraform (URL de connexion construite avec l'IP privée). La valeur de `auth0-mgmt-client-secret` s'ajoute à la main : elle ne doit pas passer par le state.

## Cloud Run et Cloud Scheduler

- **Image** : une seule, pour l'API (commande par défaut `api`) et le worker (`worker <tâche>`). Elle est passée par la variable `image` et n'est utilisée qu'à la création : Terraform ignore ensuite ses changements, car c'est le pipeline de déploiement qui publie les versions suivantes (un `apply` ne revient donc pas en arrière).
- **Secrets** : `DATABASE_URL` et `AUTH0_MGMT_CLIENT_SECRET` viennent de Secret Manager. Le domaine, l'audience et l'identifiant client Auth0 ne sont pas secrets : ce sont des variables d'environnement ordinaires.
- **Comptes de service** : un par charge (API, worker, Scheduler), sans aucun rôle sur le projet. L'API et le worker lisent seulement les deux secrets ci-dessus ; le compte du Scheduler ne peut que lancer les jobs du worker.
- **Réseau** : sortie directe vers le VPC pour les adresses privées seulement (Cloud SQL) ; Auth0 est joint par Internet.
- **Planification** : `close-weeks` à chaque heure pile et `finalize-challenges` dix minutes plus tard, en UTC. La tâche `retry-auth0-deletions` n'est pas planifiée : le ticket ne la demande pas.
- **`trusted_proxies`** : sans valeur par défaut. Vérifier sur staging l'adresse que Cloud Run montre au conteneur avant de s'y fier, sinon la limitation de débit par IP compterait tous les clients ensemble.

## Observabilité et alertes

- **Journaux** : l'API, le worker et la commande `migrate` écrivent des lignes JSON que Cloud Logging lit (`severity`, `message`, `time`). Les lignes de requête et d'erreur portent `request_id`, le même que celui renvoyé au client.
- **Error Reporting** : l'API du service est activée par Terraform. Chaque ligne de gravité `ERROR` porte le marqueur d'Error Reporting et le nom du programme (`api`, `worker`, `migrate`) : les erreurs sont regroupées sans avoir besoin d'une trace de pile. Aucune donnée personnelle dans ces lignes (règle générale des journaux).
- **Alertes**, toutes envoyées à l'adresse `alert_email` :
  - plus de 5 réponses 5xx de l'API en 5 minutes (seuil réglable, `api_5xx_threshold`) ;
  - l'échec d'une exécution d'un job Cloud Run, donc d'une tâche du worker ou d'une migration ;
  - `close-weeks` sans exécution réussie depuis plus de 3 heures (elle tourne chaque heure) ;
  - le budget (50 %, 90 % et 100 % des dépenses), par le même canal.
- Une alerte d'absence ne se déclenche qu'une fois que la tâche a réussi au moins une fois : après un déploiement, elle s'arme au premier succès de `close-weeks`.

## Choix

- **Un projet GCP par environnement**, créé à la main (il faut un compte de facturation) ; Terraform ne fait que le configurer et activer ses API.
- **Région** : Montréal (`northamerica-northeast1`) pour toutes les ressources.
- **Staging** : `db-f1-micro`, sans haute disponibilité, suppression possible. **Prod** : `db-custom-1-3840`, haute disponibilité régionale, protection contre la suppression ; ces valeurs se changent par variables, et rien n'est appliqué.
- **State** : un bucket GCS par environnement (versionné, accès privé), créé à la main une fois. Le state contient le mot de passe de la base : l'accès au bucket est donc aussi sensible que le secret : il doit rester privé, versionné et limité à une ou deux personnes qui administrent l'infrastructure.
- **Budget** : montant, devise, compte de facturation et courriel n'ont pas de valeur par défaut ; ils vont dans `terraform.tfvars`, ignoré par git.

## Mise en place d'un environnement

À faire une fois, à la main (exemple pour staging) :

```
gcloud projects create <project_id>
gcloud billing projects link <project_id> --billing-account=<billing_account_id>
gcloud storage buckets create gs://<project_id>-tfstate --project=<project_id> \
  --location=northamerica-northeast1 --uniform-bucket-level-access --public-access-prevention
gcloud storage buckets update gs://<project_id>-tfstate --versioning
```

Le compte qui lance Terraform doit pouvoir administrer le projet et gérer le budget du compte de facturation (rôle « Billing Account Costs Manager » ou « Billing Account Administrator »). Authentification locale : `gcloud auth application-default login`.

## Plan

```
cd deploy/terraform/envs/staging
cp terraform.tfvars.example terraform.tfvars   # puis remplir les valeurs
terraform init -backend-config=bucket=<project_id>-tfstate
terraform plan
```

Avant le premier `apply` (jamais sans demande explicite), Cloud Run exige que l'image et la valeur du secret Auth0 existent déjà. Dans l'ordre :

1. `terraform apply -target=module.artifact_registry -target=module.secrets`, puis pousser l'image dans le dépôt ;
2. ajouter la valeur de `auth0-mgmt-client-secret` (commande ci-dessous) ;
3. `terraform apply` pour le reste.

Sans accès à GCP, `terraform init -backend=false && terraform validate` vérifie la syntaxe et la cohérence des modules.

L'application (`terraform apply`) n'est faite que sur demande explicite. La valeur du secret Auth0 s'ajoute à l'étape 2 ci-dessus :

```
printf '%s' "<client secret>" | gcloud secrets versions add auth0-mgmt-client-secret --data-file=- --project=<project_id>
```

## Si le premier `apply` échoue

- L'activation d'une API peut mettre quelques minutes à se propager : relancer l'`apply`.
- Le budget exige que la devise soit celle du compte de facturation.
