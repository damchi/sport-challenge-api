# Terraform : infrastructure GCP

Cette procédure décrit l'infrastructure de base de chaque environnement (`deploy/terraform/envs/staging` et `prod`) : réseau privé, Cloud SQL (PostgreSQL 16), Secret Manager, Artifact Registry et alerte de budget. Cloud Run, Cloud Scheduler et les comptes de service viendront avec le ticket SC-API-070 ; le stockage des avatars (Cloud Storage et son CORS) avec la V1.1.

> **Jamais de `terraform apply` sans demande explicite.** Ce qui suit s'arrête au `plan`.

## Ce qui est créé

| Module | Ressources |
|---|---|
| `network` | réseau VPC dédié, plage d'adresses et connexion privée aux services Google |
| `cloud-sql` | instance PostgreSQL 16, base `sport_challenge`, utilisateur `sport` (mot de passe généré), IP privée seule, TLS obligatoire, sauvegardes quotidiennes et récupération à un instant donné (7 jours) |
| `secrets` | secrets `database-url` et `auth0-mgmt-client-secret` (conteneurs vides, stockés dans la région de l'environnement) |
| `artifact-registry` | dépôt Docker `sport-challenge` |
| `budget` | budget mensuel du projet, alertes à 50 %, 90 % et 100 % des dépenses, par courriel |

Le secret `database-url` reçoit sa valeur de Terraform (URL de connexion construite avec l'IP privée). La valeur de `auth0-mgmt-client-secret` s'ajoute à la main : elle ne doit pas passer par le state.

## Choix

- **Un projet GCP par environnement**, créé à la main (il faut un compte de facturation) ; Terraform ne fait que le configurer et activer ses API.
- **Région** : Montréal (`northamerica-northeast1`) pour toutes les ressources.
- **Staging** : `db-f1-micro`, sans haute disponibilité, suppression possible. **Prod** : `db-custom-1-3840`, haute disponibilité régionale, protection contre la suppression ; ces valeurs se changent par variables, et rien n'est appliqué.
- **State** : un bucket GCS par environnement (versionné, accès privé), créé à la main une fois. Le state contient le mot de passe de la base : l'accès au bucket est donc aussi sensible que le secret.
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

Sans accès à GCP, `terraform init -backend=false && terraform validate` vérifie la syntaxe et la cohérence des modules.

L'application (`terraform apply`) n'est faite que sur demande explicite. Après un premier `apply`, ajouter la valeur du secret Auth0 :

```
printf '%s' "<client secret>" | gcloud secrets versions add auth0-mgmt-client-secret --data-file=- --project=<project_id>
```

## Si le premier `apply` échoue

- L'activation d'une API peut mettre quelques minutes à se propager : relancer l'`apply`.
- Le budget exige que la devise soit celle du compte de facturation.
