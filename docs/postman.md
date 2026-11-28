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
| `adminToken` | un troisième compte, distinct de A et B, qui a le rôle Auth0 « admin » (double authentification) : le jeton de A doit être sans la permission `admin`, pour les refus des routes `/admin/*` |
| `disposableToken`, `otherDisposableToken` | deux comptes jetables C et D, pour le seul dossier « Destructif » : sans eux, ce dossier échoue sans rien supprimer |

Pour obtenir un jeton : se connecter au frontend local avec le compte, ouvrir les outils de développement (onglet Réseau), choisir un appel à l'API, puis copier la valeur de l'en-tête `Authorization` sans le mot `Bearer`.

- Coller le jeton dans la colonne **Current value** de l'environnement, jamais dans **Initial value** : seule la valeur initiale est exportée et synchronisée. **Persist** (ou **Persist all**) recopie les valeurs courantes dans les valeurs initiales : ne jamais l'utiliser sur cet environnement. Le type `secret` masque la valeur à l'écran, sans l'empêcher d'être exportée.
- Un jeton dure 15 minutes ; au-delà, l'API répond 401 `token_expired` et il faut le recopier. Un jeton du frontend local porte l'audience locale : l'API de production le refuse. Effacer les jetons (et le presse-papiers) après usage, surtout celui de l'admin.
- **Ne jamais committer un jeton** : `postman/local.postman_environment.json` garde ses jetons vides (seul `baseUrl` a une valeur), et la collection ne se remplace jamais par un export de Postman (ses variables de collection contiendraient des identifiants locaux et un jeton d'invitation, valable jusqu'à la fin de la semaine 1, effacé par la collection dès qu'il ne sert plus : ne jamais utiliser **Persist** sur elles, et les vider après une exécution) : la modifier à la main et relire `git diff` avant de committer.

## Lancer

- L'API limite chaque utilisateur à 2 requêtes par seconde (rafale de 10) : sans délai, une exécution d'affilée reçoit des 429 `rate_limited`. Mettre un délai de 600 ms entre les requêtes : **Delay** dans le Runner, `--delay-request 600` avec newman.
- Dans Postman : **Run collection** (Runner), dossiers dans l'ordre. Les requêtes s'enchaînent : une création range l'identifiant renvoyé dans une variable de collection, réutilisée ensuite.
- Ou en ligne de commande avec newman, sans rien installer dans le dépôt. Les jetons vont dans une copie de l'environnement ignorée par git (`postman/*.local.postman_environment.json`), jamais sur la ligne de commande (historique du shell, `ps`) :

  ```sh
  cp postman/local.postman_environment.json postman/mine.local.postman_environment.json
  # y coller les jetons, puis :
  npx newman run postman/sport-challenge.postman_collection.json -e postman/mine.local.postman_environment.json --delay-request 600
  ```

  Pour noter le résultat dans une pull request, n'en coller que le résumé (le tableau final). `--verbose`, `--export-environment`, `--export-collection` et les rapports HTML ou JSON écrivent l'en-tête `Authorization`, les jetons ou le corps des requêtes (dont le jeton d'invitation).

### Le dossier « Destructif »

Il supprime deux comptes, **irréversiblement** : C se supprime lui-même (`DELETE /me`), l'admin supprime D. À lancer seul, en dernier, après les autres dossiers (il lui faut `token`, `otherToken` et `adminToken`), jamais avec le compte d'une vraie personne.

- Créer deux comptes Auth0 jetables (une adresse de test chacun), s'y connecter une fois au frontend local pour créer leur compte local, puis coller leurs jetons dans `disposableToken` et `otherDisposableToken`. Le tenant Auth0 est celui de la production (`docs/auth0-setup.md`) : ces comptes y existent comme les autres.
- Une identité supprimée ne recrée jamais de compte local : chaque exécution du dossier demande deux nouveaux comptes.
- En local, la suppression chez Auth0 échoue (l'application machine à machine locale n'a aucun droit) et reste en attente de reprise : c'est attendu. Supprimer ensuite les deux comptes à la main dans la console Auth0.
- Sans ces deux jetons, le dossier échoue sans rien supprimer : lancer toute la collection sans eux est sans danger. Le dossier relit d'abord les identifiants de A, B et de l'admin (il lui faut donc aussi `otherToken`), et chaque suppression est sautée si l'un d'eux manque ou si son compte n'est pas un compte jetable (ni A, ni B, ni l'admin).
- Récupérer les jetons de C, D, A et de l'admin juste avant de lancer le dossier : ils durent 15 minutes.
- Pour C et D, utiliser des adresses qui reçoivent le code de connexion (boîte de test, ou alias `+…`) ; l'envoi passe par le fournisseur d'e-mail de la production. Ne jamais s'y connecter sur l'app de production.
- Dans la console Auth0 (celle de la production), chercher chaque compte par son adresse de test exacte et vérifier son identifiant (`sub`) avant de le supprimer.
- Les deux demandes de suppression restent en attente dans la base locale : en local, `make worker TASK=retry-auth0-deletions` les rejoue sans pouvoir aboutir.

## Dossiers

| Dossier | Routes |
|---|---|
| Santé | `GET /health`, `GET /ready` (sans jeton) |
| Compte | `PATCH /api/v1/me` (complète le profil, exigé pour créer un défi ou saisir une activité ; le prénom et le nom du compte A deviennent « Test Postman »), `GET /api/v1/me`, et le refus sans jeton |
| Sports | `GET /api/v1/sports` |
| Défis | le profil de B complété (exigé pour rejoindre) ; un défi principal public, CrossFit avec élimination aux burpees, gardé pour les dossiers suivants (`challengeId`) ; un défi privé (404 pour B, modifié, supprimé) ; un défi où B rejoint, est nommé créateur puis abandonne ; un défi où B est exclu, puis ne peut plus rejoindre ; les refus 400, 403, 404, 409 et 422 |
| Invitations | une invitation au défi principal, ouverte puis acceptée par B (B y devient participant actif, pour les dossiers suivants), le 409 d'une seconde acceptation, le 404 d'un jeton inconnu et le 403 d'une invitation par B (le défi principal ne laisse pas inviter les participants) |
| Activités | une séance CrossFit avec burpees, saisie avec une clé d'idempotence, rejouée (200), remplacée, gardée pour les dossiers suivants (`activityId`) ; une course saisie puis supprimée ; les refus 400 (sport inconnu), 404 (modification et suppression de l'activité d'un autre) et 422 (`unknown_exercise`) ; les lectures de l'utilisateur. Deux exécutions de la collection doivent être espacées de 5 minutes : la séance va de 6 à 2 minutes avant l'exécution et la course de 7 à 6 minutes avant, et deux activités ne se chevauchent jamais (`activity_overlap`). Éviter aussi de la lancer entre 00 h 00 et 00 h 10 UTC : la séance est alors ramenée au début du jour, dans la fenêtre du défi (la course, elle, tombe la veille et ne compte dans aucun défi, sans effet sur les tests) |
| Lectures d'un défi | le classement du défi principal (temps cumulé, élimination aux burpees, A et B classés), son fil d'activités et les activités de A vus par B ; le 404 des points sans catégorie Points ; un défi public que B voit sans l'avoir rejoint, où A participe (404 `not_found` pour B sur son fil et sur les activités de A), supprimé en fin de dossier |
| Admin : défis | avec `adminToken` (compte qui a le rôle « admin », double authentification) sur un défi privé de A créé pour l'occasion : tableau de bord, recherche, détail ; modification (refusée sans `reason`, puis `dry_run`, puis pour de bon) ; B ajouté (409 la seconde fois), exclu, réintégré, nommé créateur puis retiré ; le refus `last_creator` sur A ; suppression (le défi supprimé trouvé dans la liste) puis restauration, chacune précédée de son `dry_run`, visibles dans les actions récentes ; chaque route du dossier avec `token`, sans la permission, en `dry_run` : 404 `not_found` ; enfin le défi supprimé par A |
| Admin : comptes | avec `adminToken` : la liste et la recherche des comptes (dans le corps, jamais dans l'URL), B lu, renommé, bloqué puis débloqué (puis il relit son profil, et retrouve son nom en fin de dossier), le 403 d'un admin qui se bloque ; les défis de A ; une course ajoutée à A juste après sa séance, dans la même fenêtre et sans chevauchement (aperçu puis pour de bon, avec une clé d'idempotence), puis supprimée ; le 422 `unknown_exercise` (en aperçu) ; les répétitions de la séance de A corrigées (35 puis 40 burpees), visibles dans ses activités et dans le journal, qui donne aussi les actions sur B, sur la course et la restauration du dossier précédent ; chaque route du dossier avec `token`, sans la permission : 404 `not_found` (en aperçu, ou sur un compte inexistant) |
| Destructif (à lancer seul) | C, seul créateur d'un défi en cours, refusé (422 `last_creator`) puis supprimé (204), son jeton refusé ensuite (401 `account_deleted`) ; D de même, supprimé par l'admin (404 sans la permission), puis anonymisé (suppression Auth0 en attente), trouvé par la recherche avant et plus après, et 404 `user_not_found` une seconde fois |

Avant de committer un changement de la collection, vérifier que ses scripts sont du JavaScript valide (les tests sont des chaînes entre apostrophes : jamais d'apostrophe dans un nom de test) :

```sh
f=$(mktemp).js && jq -r '.. | objects | select(has("exec")) | .exec[]' postman/sport-challenge.postman_collection.json > "$f" && node --check "$f"; rm -f "$f"
```
