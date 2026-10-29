# Contribuer

## Branches et pull requests

- Une branche par ticket, créée depuis `main` à jour : `sc-api-0xx-<résumé>` (voir le skill `next-task`).
- Toute modification de `main` passe par une pull request, fusionnée seulement quand la CI est verte.

## Intégration continue

Le workflow `.github/workflows/ci.yml` s'exécute sur chaque pull request et sur chaque push vers `main` : `make fmt` (aucune différence attendue), `make lint`, `make test`. Les étapes s'ajoutent au fil des tickets (migrations, tests d'intégration, build de l'image).

## Protection de `main` (réglage GitHub, à faire une fois)

Dans **Settings → Rules → Rulesets → New branch ruleset**, cible `main`, statut **Active** :

1. **Bypass list** : vide (même l'administrateur ne contourne pas les règles).
2. **Require a pull request before merging** : activé, avec **Required approvals** à `0` (un développeur seul doit pouvoir fusionner sa propre pull request).
3. **Require status checks to pass** : activé, avec le contrôle `fmt, lint, test` et la source **GitHub Actions** (il apparaît après la première exécution de la CI) ; cocher **Require branches to be up to date before merging**.
4. **Block force pushes** et **Restrict deletions** : activés.

Dans **Settings → Actions → General** :

- **Workflow permissions** : *Read repository contents and packages permissions* (jeton en lecture seule par défaut).
- **Fork pull request workflows** : exiger une approbation avant d'exécuter les workflows des contributeurs externes.
