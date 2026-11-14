# Staging : déploiement, horloge décalée et tests de fumée

Staging est l'environnement où l'on vérifie, avant la production, qu'une version se déploie et que les règles qui dépendent du temps (fin de semaine, élimination, résultats figés) se comportent comme prévu, sans attendre des semaines.

> **Jamais de déploiement ni de `terraform apply` sans demande explicite.** Les commandes ci-dessous modifient l'environnement réel : à lancer une étape à la fois, après accord.

L'infrastructure est décrite dans `docs/terraform.md` (premier `apply`, ordre des étapes) et les migrations dans `docs/migrations.md`. Ce document couvre ce qui s'y ajoute : publier une version, décaler l'horloge, et vérifier le tout.

## Publier une version

Droits nécessaires pour la personne qui publie : écrire dans Artifact Registry (`roles/artifactregistry.writer`) et mettre à jour et exécuter les services et jobs Cloud Run (`roles/run.developer`).

1. Construire l'image depuis la branche à tester et la pousser dans Artifact Registry (une seule image : API, worker et migrations) :

   ```
   gcloud auth configure-docker northamerica-northeast1-docker.pkg.dev
   make docker-build
   docker tag sport-challenge-api:local <image_repository>/api:<tag>
   docker push <image_repository>/api:<tag>
   ```

   `<image_repository>` est la sortie `image_repository` de Terraform.
2. Appliquer les migrations **avant** le déploiement, comme décrit dans `docs/migrations.md` (mise à jour de l'image du job, puis exécution avec `--wait`). Si le job échoue, on s'arrête.
3. Déployer l'API et les jobs du worker avec la nouvelle image (Terraform ignore l'image après la création, c'est la publication qui la change) :

   ```
   gcloud run services update sport-challenge-api --project=<projet> --region=northamerica-northeast1 --image=<image>
   gcloud run jobs update sport-challenge-worker-close-weeks --project=<projet> --region=northamerica-northeast1 --image=<image>
   gcloud run jobs update sport-challenge-worker-finalize-challenges --project=<projet> --region=northamerica-northeast1 --image=<image>
   ```

4. Vérifier que `GET <api_url>/healthz` et `/readyz` répondent 200.

## Horloge décalée (`CLOCK_OFFSET`)

`CLOCK_OFFSET` avance l'horloge de l'API et du worker d'une durée fixe (durée Go : `24h`, `168h`, `90m` ; pas de jours `d`). Les semaines d'un challenge se terminent alors plus tôt, selon l'horloge du serveur.

- **Staging seulement** : l'environnement de production n'a pas cette variable, et l'API comme le worker **refusent de démarrer** si `CLOCK_OFFSET` est défini avec `ENV=production`.
- **Réglage** : variable Terraform `clock_offset` de `envs/staging` (dans `terraform.tfvars`), puis `terraform apply` (sur demande explicite). Le plan ne doit changer que les variables d'environnement du service et des jobs. Pour revenir à l'heure réelle : retirer la variable et réappliquer.
- **Ce qui est décalé** : tout ce qui passe par l'horloge de l'application (semaine en cours, verrouillage d'une semaine terminée, clôtures, gel des résultats, validité des invitations). **Pas décalé** : la validité des jetons Auth0, les dates que la base écrit elle-même, et le déclenchement de Cloud Scheduler (il tourne à l'heure réelle). Pour juger une semaine tout de suite, lancer la tâche à la main :

  ```
  gcloud run jobs execute sport-challenge-worker-close-weeks --project=<projet> --region=northamerica-northeast1 --wait
  gcloud run jobs execute sport-challenge-worker-finalize-challenges --project=<projet> --region=northamerica-northeast1 --wait
  ```

- Un journal `clock moved forward` (niveau `WARNING`, avec la durée) est écrit au démarrage tant qu'un décalage est actif.
- Les tâches du worker sont idempotentes : relancer une tâche déjà faite ne change rien.

## Tests de fumée

À rejouer sur staging après chaque déploiement. Les corps de requête sont décrits par `api/openapi.yaml` (opérations citées entre parenthèses). Le jeton est celui d'un **compte de test** obtenu par une vraie connexion (le frontend de staging, ou le flux Authorization Code de la SPA) : l'application de test machine à machine d'Auth0 est volontairement supprimée (`docs/auth0-setup.md`). Un jeton vaut 15 minutes ; ne pas le laisser dans l'historique du shell (le saisir sans l'écrire en clair dans un fichier partagé).

```
API=<api_url>; TOKEN=<jeton d'accès du compte de test>
curl -sS -H "Authorization: Bearer $TOKEN" "$API/api/v1/me"
```

| # | Vérification | Appel | Attendu |
|---|---|---|---|
| 1 | Connexion | `GET /api/v1/me` (`getMe`) | 200, le profil du compte (créé au premier appel) ; sans jeton : 401 |
| 2 | Création d'un challenge | `POST /api/v1/challenges` (`createChallenge`) | 201, les 3 catégories, le compte est créateur et participant |
| 3 | Saisie d'une activité | `POST /api/v1/activities` (`createActivity`, avec `Idempotency-Key`) | 201 ; le même appel rejoué rend la même activité |
| 4 | Classement | `GET /api/v1/challenges/{id}/leaderboard` (`getLeaderboard`) | 200, l'activité compte dans les 3 catégories |
| 5 | Abandon | `POST /api/v1/challenges/{id}/abandon` (`abandonChallenge`) | 2xx ; le compte apparaît « a abandonné » dans le classement |
| 6 | Suppression | `DELETE /api/v1/challenges/{id}` (`deleteChallenge`) | 2xx ; le challenge est ensuite introuvable (404) |

Vérifier aussi, une fois : une ligne `ERROR` provoquée volontairement apparaît dans Error Reporting avec le bon `service`, et le journal d'une requête porte `request_id` (`docs/terraform.md`, section Observabilité).

## Scénario de 3 semaines avec horloge avancée

Il vérifie, sur staging, ce que les tests d'intégration vérifient déjà en base avec une horloge simulée (`internal/closure/service_integration_test.go` : élimination semaine par semaine, tâche relancée sans effet, abandon, gel des trois classements, classement servi depuis les résultats figés).

1. Avec un compte de test, créer un challenge de **3 semaines** qui commence aujourd'hui, avec la catégorie à élimination (incrément `X`). Deux comptes de test le rejoignent : A et B.
2. Semaine 1 : A et B saisissent une course (pas d'élimination en semaine 1).
3. Régler `clock_offset = "168h"` (une semaine) et appliquer : le serveur est au début de la semaine 2. Attendu : une activité datée de la semaine 1 est **refusée** (semaine verrouillée) dès que l'horloge a passé la fin de la semaine, avant même le passage du worker (c'est l'horloge qui fait foi). Lancer ensuite `close-weeks` à la main : la semaine 1 est close, sans élimination.
4. Toujours à `168h`, A et B saisissent leur course de la semaine 2, datée de quelques minutes avant « maintenant » pour le serveur : A court au moins la distance de la semaine 1 plus `X`, B moins. Régler `336h`, appliquer, lancer `close-weeks`. Attendu : B est éliminé de la catégorie à élimination (et de celle-là seulement), A reste en course ; le classement affiche B éliminé en semaine 2.
5. Régler `504h` n'a de sens qu'après la semaine 3 : à `336h` (début de la semaine 3), A saisit sa course de la semaine 3, au moins la distance de la semaine 2 plus `X`. Puis régler `504h` (la fin du challenge), appliquer, lancer `close-weeks` puis `finalize-challenges`. Attendu : le challenge est « terminé », les trois classements sont figés (A premier de la catégorie à élimination, B éliminé en semaine 2) ; une seconde exécution des deux tâches ne change rien.
6. Retirer `clock_offset` et réappliquer, puis supprimer le challenge de test.

Les badges ne font pas partie du scénario : ils sont reportés après la V1 (V1.1).

## Après les tests

Avec une horloge avancée, les challenges et activités de test portent des dates qui ne correspondent plus à l'heure réelle, et les résultats figés sont définitifs : ne pas réutiliser ces données une fois l'horloge revenue à l'heure réelle.

- Retirer `clock_offset` (l'horloge redevient réelle) et vérifier que le journal `clock moved forward` n'apparaît plus.
- Supprimer les challenges et comptes de test.
