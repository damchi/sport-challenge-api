# Collection Postman

La collection `postman/sport-challenge.postman_collection.json` appelle l'API locale. Chaque requête porte ses tests. Elle sert à vérifier l'API à la main ; elle ne tourne pas en CI, faute de jeton utilisateur (décisions du 2026-11-27, SC-API-227).

## Préparer

1. Lancer la base et l'API locales : `make docker-up`, `make migrate-up`, puis `make run`.
2. Dans Postman, importer `postman/sport-challenge.postman_collection.json` et `postman/local.postman_environment.json`, puis choisir l'environnement « Sport Challenge local ».
3. Vérifier `baseUrl` : `http://localhost:8080`, ou le `PORT` du `.env`.

## Les jetons

L'API n'accepte que l'access token d'un utilisateur Auth0 : aucune application machine à machine n'y a accès (`docs/auth0-setup.md`). La collection lit donc des jetons collés à la main.

| Variable | Compte |
|---|---|
| `token` | l'utilisateur A, celui qui crée les défis |
| `otherToken` | l'utilisateur B, pour les invitations et l'exclusion |
| `adminToken` | un compte qui a le rôle Auth0 « admin » (double authentification) |

Pour obtenir un jeton : se connecter au frontend local avec le compte, ouvrir les outils de développement (onglet Réseau), choisir un appel à l'API, puis copier la valeur de l'en-tête `Authorization` sans le mot `Bearer`.

- Coller le jeton dans la colonne **Current value** de l'environnement, jamais dans **Initial value** : seule la valeur initiale est exportée et synchronisée. **Persist** (ou **Persist all**) recopie les valeurs courantes dans les valeurs initiales : ne jamais l'utiliser sur cet environnement. Le type `secret` masque la valeur à l'écran, sans l'empêcher d'être exportée.
- Un jeton dure 15 minutes ; au-delà, l'API répond 401 `token_expired` et il faut le recopier. Un jeton du frontend local porte l'audience locale : l'API de production le refuse. Effacer les jetons (et le presse-papiers) après usage, surtout celui de l'admin.
- **Ne jamais committer un jeton** : `postman/local.postman_environment.json` garde ses jetons vides (seul `baseUrl` a une valeur), et la collection ne se remplace jamais par un export de Postman (ses variables de collection contiendraient des identifiants locaux) : la modifier à la main et relire `git diff` avant de committer.

## Lancer

- Dans Postman : **Run collection** (Runner), dossiers dans l'ordre. Les requêtes s'enchaînent : une création range l'identifiant renvoyé dans une variable de collection, réutilisée ensuite.
- Ou en ligne de commande avec newman, sans rien installer dans le dépôt. Les jetons vont dans une copie de l'environnement ignorée par git (`postman/*.local.postman_environment.json`), jamais sur la ligne de commande (historique du shell, `ps`) :

  ```sh
  cp postman/local.postman_environment.json postman/mine.local.postman_environment.json
  # y coller les jetons, puis :
  npx newman run postman/sport-challenge.postman_collection.json -e postman/mine.local.postman_environment.json
  ```

  Pour noter le résultat dans une pull request, n'en coller que le résumé (le tableau final). `--verbose`, `--export-environment` et les rapports HTML ou JSON écrivent l'en-tête `Authorization` ou les jetons.

Les requêtes destructives (suppression de compte) seront dans un dossier à part, à lancer en dernier, avec des comptes jetables.

## Dossiers

| Dossier | Routes |
|---|---|
| Santé | `GET /health`, `GET /ready` (sans jeton) |
| Compte | `PATCH /api/v1/me` (complète le profil, exigé pour créer un défi ou saisir une activité ; le prénom et le nom du compte A deviennent « Test Postman »), `GET /api/v1/me`, et le refus sans jeton |
| Sports | `GET /api/v1/sports` |
