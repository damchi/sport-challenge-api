# Versions et informations de build

Chaque build est identifiable : une **version sémantique** (`vMAJOR.MINOR.PATCH`), le **commit** exact dont il vient, et une image Docker taguée en conséquence. La version n'est jamais choisie à la main : elle est **calculée à chaque fusion sur `main`**, d'après les messages de commit.

> Les tags et les releases sont créés par le workflow `release.yml`, jamais à la main, ni par moi (Claude) : on ne pose pas de tag sans demande explicite.

## Comment la version est calculée

Après une CI verte sur `main`, le workflow `.github/workflows/release.yml` lit les messages de commit depuis le dernier tag `v*` et applique les règles des commits conventionnels (`.github/scripts/next-version.sh`) :

| Message de commit | Effet |
|---|---|
| `feat(...)` : une nouvelle fonction | version **mineure** (`v1.2.3` → `v1.3.0`) |
| `fix(...)` : une correction | **correctif** (`v1.2.3` → `v1.2.4`) |
| `feat(...)!:` ou `fix!:` (un `!` avant les deux-points), ou une ligne `BREAKING CHANGE: ...` dans le corps | version **majeure** (`v1.2.3` → `v2.0.0`) |
| `docs`, `chore`, `ci`, `test`, `refactor`, `perf`, les fusions de pull request… | aucune version |

- La plus haute des règles l'emporte quand plusieurs commits s'accumulent.
- **Avant la 1.0** (version majeure 0), un changement cassant ne monte que la version mineure (`v0.4.1` → `v0.5.0`).
- **Première version** : `v0.1.0`, au premier `feat` ou `fix` quand aucun tag n'existe.
- Sans commit qui appelle une version, rien n'est publié. Un commit qui porte déjà un tag n'est pas publié deux fois.

Les règles sont testées par `.github/scripts/next-version_test.sh`, lancé par la CI.

## Ce que fait le workflow

1. Calcule la version suivante (voir ci-dessus).
2. **Construit l'image** avec la version, le commit et sa date (`make docker-build` fait de même en local), tagguée `<version>` et `<sha court>`, **jamais `latest`**. Si le build échoue, aucun tag n'est créé. Cette image n'est pas poussée ici : le déploiement continu (`docs/deploiement.md`), qui s'authentifie à GCP, reconstruit et pousse celle qu'il déploie.
3. **Crée le tag et la release GitHub**, dont les notes sont générées automatiquement à partir des pull requests fusionnées. Il n'y a pas de `CHANGELOG.md` : les notes de release en tiennent lieu.

Pour que la version soit bien calculée, **les messages de commit suivent la convention** : `type(portée): résumé` en anglais, par exemple `feat(activity): add speed filter` ou `fix(worker): retry the Auth0 deletion`. Un `!` ou une ligne `BREAKING CHANGE:` signale un changement cassant.

## Retrouver ce qui tourne

- **L'API en marche** : `GET <url>/healthz` (sans jeton) répond `{"status":"ok","version":"v1.2.3","commit":"<sha complet>"}`. Une version `dev` et un commit `unknown` indiquent un build qui ne vient pas d'une release.
- **Les journaux** : la ligne `server started` de l'API et la ligne `worker started` de chaque exécution du worker portent `version` et `commit`.
- **L'image** : les étiquettes `org.opencontainers.image.version`, `.revision` (le commit) et `.created` : `docker image inspect <image>`.
- **Le code** : `git show <sha>` ou `git checkout v1.2.3`.

Sur staging comme en production, c'est donc `/healthz` qui dit quel commit tourne, sans accès à GCP.

## À savoir

- Un tag créé par le workflow avec son jeton **ne déclenche pas d'autre workflow** (règle de GitHub). Le déploiement en production d'une version s'enchaîne donc par un `workflow_run` sur `Release` (`.github/workflows/deploy.yml`, voir `docs/deploiement.md`).
- Pour rejouer une release qui a échoué avant le tag, relancer le workflow depuis l'onglet Actions. Une version publiée par erreur se supprime à la main (release puis tag), sur demande explicite : ne jamais déplacer un tag existant.
