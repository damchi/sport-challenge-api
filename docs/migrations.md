# Migrations en déploiement

Les migrations (golang-migrate, dossier `migrations/`) s'appliquent par un **job Cloud Run dédié**, `sport-challenge-migrate`, lancé **avant** le déploiement de la nouvelle version. L'API ne touche jamais au schéma en démarrant.

Qui peut lancer `down` et `force` : les personnes qui ont le droit de lancer le job avec des arguments (rôle Cloud Run Developer sur le job), donc à limiter, en prod, à deux personnes ; ces exécutions sont tracées dans les journaux d'audit. Ni l'API, ni le worker, ni Cloud Scheduler ne peuvent lancer ce job. Le pipeline de déploiement (`docs/deploiement.md`) lance `migrate up` avec son propre compte de service, distinct de celui du job ; son script ne lance jamais `down` ni `force`. Ce n'est qu'une convention du script : le rôle `run.developer` que ce compte a sur le job permet aussi de le lancer avec des arguments, donc l'accès au compte de déploiement (Environments GitHub restreints à `main`, approbation en production) est ce qui l'en empêche.

Le job utilise la même image que l'API et le worker, avec la commande `migrate` : les fichiers SQL sont embarqués dans le binaire, donc le schéma correspond toujours à la version du code. Il n'a accès qu'au secret `DATABASE_URL`, et une migration en échec n'est jamais relancée automatiquement.

| Commande | Effet |
|---|---|
| `migrate up` | applique les migrations en attente (rien à faire : code de sortie 0) |
| `migrate down <n>` | annule les `n` dernières migrations (`n` obligatoire, de 1 à 3 : au-delà, restaurer une sauvegarde) |
| `migrate force <version>` | fixe la version après un échec : **à la main seulement** |

> Les commandes ci-dessous visent un environnement (`<projet>` : son projet GCP). Elles modifient la base : jamais sur la prod sans accord.

## Ordre d'un déploiement

1. Mettre l'image du job à jour, puis l'exécuter en attendant sa fin :

   ```
   gcloud run jobs update sport-challenge-migrate --project=<projet> --region=northamerica-northeast1 --image=<image>
   gcloud run jobs execute sport-challenge-migrate --project=<projet> --region=northamerica-northeast1 --wait
   ```

   `--wait` rend un code de sortie non nul si la migration échoue : **le pipeline s'arrête là et le déploiement n'a pas lieu**. L'échec est visible dans l'exécution du pipeline (et, avec le ticket d'observabilité, dans une alerte).
2. Seulement si le job a réussi : déployer l'API et le worker.

## Migration « sale » (`dirty`)

Une migration qui échoue en route laisse la base marquée `dirty` : `up` et `down` refusent alors de s'exécuter (`database is dirty at version N`). **Rien n'est forcé automatiquement.**

1. Lire les journaux de l'exécution (Cloud Logging, job `sport-challenge-migrate`) pour connaître la migration en cause.
2. Regarder l'état réel du schéma : ce que la migration a appliqué avant d'échouer. Chaque fichier est exécuté d'un bloc, mais vérifier plutôt que supposer.
3. Remettre le schéma dans l'état d'une version connue, à la main (`force` ne répare rien : il change seulement la version notée), puis indiquer **la dernière version dont la migration s'est entièrement appliquée** :

   ```
   gcloud run jobs execute sport-challenge-migrate --project=<projet> --region=northamerica-northeast1 --wait --args=migrate,force,<version>
   ```

4. Corriger la migration, puis relancer `migrate up`. Une migration qu'aucun environnement n'a appliquée en entier peut se corriger sur place ; dès qu'elle a été appliquée quelque part, la correction va **dans une nouvelle migration** (une migration appliquée ne se modifie jamais).

## Retour arrière

Du plus sûr au plus risqué :

1. **Revenir à la révision précédente de l'API et du worker** (Cloud Run garde les révisions). Cela suffit tant que la migration appliquée est compatible avec l'ancien code, c'est-à-dire ajoute sans retirer ni renommer ; c'est ce qu'il faut viser pour chaque migration.
2. **Corriger en avant** : une nouvelle migration qui répare ou annule l'effet de la précédente. C'est le chemin normal.
3. **Annuler la dernière migration** avec son fichier `.down.sql` :

   ```
   gcloud run jobs execute sport-challenge-migrate --project=<projet> --region=northamerica-northeast1 --wait --args=migrate,down,1
   ```

   Un `.down.sql` supprime souvent des colonnes ou des tables, donc des données. Avant : lancer une sauvegarde à la demande (`gcloud sql backups create --instance=sport-challenge --project=<projet>`) et relire le fichier.
4. **Restaurer la base** à un instant précédant la migration (récupération à un instant donné, 7 jours de journaux), en dernier recours : `gcloud sql instances clone` vers une nouvelle instance, puis bascule à décider avec l'équipe.

## À vérifier au premier passage sur staging

- Le premier `migrate up` crée toutes les tables : vérifier que l'utilisateur `sport` a le droit de créer dans le schéma `public` sur Cloud SQL (PostgreSQL 15 et plus ont retiré ce droit par défaut). S'il manque, il se donne une fois, à la main, puis le job réussit.

## Limite connue

L'API et le job de migration utilisent les mêmes identifiants de base de données : l'API tourne donc avec un rôle qui a les droits de modifier le schéma. Un durcissement possible, plus tard : deux rôles PostgreSQL (un pour les migrations, un limité aux lectures et écritures pour l'API) et deux secrets.
