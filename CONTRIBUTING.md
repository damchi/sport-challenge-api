# Contribuer

## Branches et pull requests

- Une branche par ticket, créée depuis `main` à jour : `sc-api-0xx-<résumé>` (voir le skill `next-task`).
- Toute modification de `main` passe par une pull request, fusionnée seulement quand la CI est verte.

## Intégration continue

Le workflow `.github/workflows/ci.yml` s'exécute sur chaque pull request et sur chaque push vers `main` : `make fmt` (aucune différence attendue), `make openapi-lint`, `make generate` (le code généré doit être identique à celui du dépôt), `make lint`, `make test`. Les étapes s'ajoutent au fil des tickets (migrations, tests d'intégration, build de l'image).

## Ajouter ou modifier une route

L'API est **spec-first** : le contrat `api/openapi.yaml` est la source, le code de routage est généré à partir de lui. Il n'y a donc aucun `router.GET(...)` écrit à la main.

1. **Contrat** : décris la route dans `api/openapi.yaml` (chemin, méthode, `operationId`, paramètres, schémas de requête et de réponse, erreurs possibles, sécurité). Vérifie avec `make openapi-lint` ; `make doc` affiche le contrat dans Swagger UI sur http://localhost:8082.
2. **Génération** : `make generate` réécrit `internal/api/api.gen.go` (configuration dans `api/oapi-codegen.yaml`). Ce fichier contient l'enregistrement des routes Gin, les types de requête et de réponse, et l'interface `StrictServerInterface` avec une méthode par `operationId`. Il est versionné et **jamais modifié à la main** : la CI échoue s'il ne correspond plus au contrat.
3. **Handler** : implémente la nouvelle méthode de l'interface dans le module concerné (exemple : `internal/health/handler.go` pour `GET /healthz`). Le handler reçoit un objet de requête typé et renvoie une réponse typée (par exemple `api.GetHealthz200JSONResponse`) : il ne manipule ni `gin.Context` ni code HTTP écrit à la main, et seules les réponses prévues par le contrat compilent. Tant qu'une méthode de l'interface n'est pas implémentée, le code ne compile pas.
4. **Câblage** : `cmd/api/main.go` passe l'implémentation à `api.NewStrictHandlerWithOptions` puis à `api.RegisterHandlersWithOptions`, qui enregistre toutes les routes du contrat sur le routeur. Les gestionnaires d'erreur par défaut d'oapi-codegen (qui renvoient le texte de l'erreur au client) y sont remplacés par ceux de `internal/platform/httpx` : ne les retire pas.
5. **Tests** : le test de la route appelle `apitest.ValidateResponse` (`internal/api/apitest`), qui vérifie que la réponse réelle (statut, en-têtes, corps) correspond au contrat.

Chemin d'une requête : middlewares (`RequestID`, `Logger`, `Recovery`) → route générée → lecture des paramètres → handler strict généré → ton handler → réponse typée écrite par le code généré. Avec `LOG_LEVEL=debug`, l'API journalise au démarrage la liste des routes enregistrées.

## Protection de `main` (réglage GitHub, à faire une fois)

Dans **Settings → Rules → Rulesets → New branch ruleset**, cible `main`, statut **Active** :

1. **Bypass list** : vide (même l'administrateur ne contourne pas les règles).
2. **Require a pull request before merging** : activé, avec **Required approvals** à `0` (un développeur seul doit pouvoir fusionner sa propre pull request).
3. **Require status checks to pass** : activé, avec le contrôle `fmt, lint, test` et la source **GitHub Actions** (il apparaît après la première exécution de la CI) ; cocher **Require branches to be up to date before merging**.
4. **Block force pushes** et **Restrict deletions** : activés.

Dans **Settings → Actions → General** :

- **Workflow permissions** : *Read repository contents and packages permissions* (jeton en lecture seule par défaut).
- **Fork pull request workflows** : exiger une approbation avant d'exécuter les workflows des contributeurs externes.
