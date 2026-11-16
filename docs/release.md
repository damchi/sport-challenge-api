# Versions et informations de build

Chaque build est identifiable : une **version sémantique** (`vMAJOR.MINOR.PATCH`), le **commit** exact dont il vient, et une image Docker taguée en conséquence. Chaque fusion sur `main` (CI verte) publie une nouvelle version : le **correctif** monte d'un cran, **quels que soient les messages de commit**.

> Les tags et les releases sont créés par le workflow `release.yml`. On ne pose pas de tag à la main (ni moi, Claude) sans demande explicite.

## Comment la version est calculée

Après une CI verte sur `main`, le workflow `.github/workflows/release.yml` prend le dernier tag `v*` et monte le **correctif** (`v1.2.3` → `v1.2.4`, `.github/scripts/next-version.sh`). Les messages de commit n'entrent pas en compte : ils sont trop ambigus pour décider d'un déploiement.

- **Première version** : `v0.1.0`, quand aucun tag n'existe.
- **Version mineure ou majeure** : se décide à la main. Avant de fusionner, ou sur le sommet de `main` avant la fin de la CI, créer la release voulue (`gh release create v0.2.0 --target <sha> --generate-notes`) : un commit qui porte déjà un tag `v*` n'est pas publié deux fois, et c'est ce tag que le déploiement envoie en production.
- Un commit qui porte déjà un tag n'est pas publié deux fois.

Les règles sont testées par `.github/scripts/next-version_test.sh`, lancé par la CI.

## Ce que fait le workflow

1. Calcule la version suivante (voir ci-dessus).
2. **Construit l'image** avec la version, le commit et sa date (`make docker-build` fait de même en local), tagguée `<version>` et `<sha court>`, **jamais `latest`**. Si le build échoue, aucun tag n'est créé. Cette image n'est pas poussée ici : le déploiement continu (`docs/deploiement.md`), qui s'authentifie à GCP, reconstruit et pousse celle qu'il déploie.
3. **Crée le tag et la release GitHub**, dont les notes sont générées automatiquement à partir des pull requests fusionnées. Il n'y a pas de `CHANGELOG.md` : les notes de release en tiennent lieu.

## Retrouver ce qui tourne

- **L'API en marche** : `GET <url>/healthz` (sans jeton) répond `{"status":"ok","version":"v1.2.3","commit":"<sha complet>"}`. Une version `dev` et un commit `unknown` indiquent un build qui ne vient pas d'une release.
- **Les journaux** : la ligne `server started` de l'API et la ligne `worker started` de chaque exécution du worker portent `version` et `commit`.
- **L'image** : les étiquettes `org.opencontainers.image.version`, `.revision` (le commit) et `.created` : `docker image inspect <image>`.
- **Le code** : `git show <sha>` ou `git checkout v1.2.3`.

Sur staging comme en production, c'est donc `/healthz` qui dit quel commit tourne, sans accès à GCP.

## À savoir

- Un tag créé par le workflow avec son jeton **ne déclenche pas d'autre workflow** (règle de GitHub). Le déploiement en production d'une version s'enchaîne donc par un `workflow_run` sur `Release` (`.github/workflows/deploy.yml`, voir `docs/deploiement.md`).
- Pour rejouer une release qui a échoué avant le tag, relancer le workflow depuis l'onglet Actions. Une version publiée par erreur se supprime à la main (release puis tag), sur demande explicite : ne jamais déplacer un tag existant.
