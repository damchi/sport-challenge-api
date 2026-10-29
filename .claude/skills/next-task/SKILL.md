---
name: next-task
description: Prend la prochaine issue GitHub du backlog du backend Sport Challenge, l'implémente et prépare le commit. À utiliser quand on dit « prochain ticket », « next task », « prends le ticket SC-API-0xx », « on continue le backlog » ou « quelle est la suite ».
---

# Traiter un ticket du backlog

Le backlog est dans les **issues GitHub** (tickets `SC-API-001` à `SC-API-075`, numérotation non consécutive : les tickets listés dans « Tickets fusionnés » de `TASKS.md` n'existent plus). **Un ticket = un commit.** Ne traite jamais deux tickets d'affilée sans que l'utilisateur ait validé le commit du premier.

## 1. Choisir le ticket

Les tickets sont des **issues GitHub** (importées depuis `TASKS.md`). Le statut vit dans l'issue, jamais dans `TASKS.md`.

- Si l'utilisateur donne un identifiant, prends celui-ci : `gh issue view <numéro ou identifiant>` (le numéro d'issue est dans le titre de `python3 .claude/scripts/tasks.py next` ; sinon `gh issue list --search "SC-API-0xx in:title" --state all`).
- Sinon, lance `python3 .claude/scripts/tasks.py next` : il renvoie la première issue **ouverte** dont **toutes** les dépendances sont **fermées**, hors étiquettes `v1.1` (reporté), `blocked`, `needs-decision` et `en-cours`.
- Si la commande affiche une ligne `EN COURS`, propose de reprendre ce ticket avant d'en ouvrir un autre.
- Si aucun ticket n'est disponible, explique pourquoi (dépendances encore ouvertes, décisions attendues) ; ne contourne jamais une dépendance sans demande explicite.
- Un ticket `blocked` ou `needs-decision` n'est jamais pris : explique ce qui manque et demande la décision.
- Lis l'issue en entier avec `gh issue view <numéro>` : description, critères d'acceptation, tests, revue, message de commit.

## 2. Se préparer

- Relis les sections du `README.md` citées par le ticket et les règles de `CLAUDE.md`.
- Si un critère s'appuie sur un 🟡 ou un ❓ non tranché, ou si le ticket contredit le README : **demande avant d'écrire**.
- Marque le ticket en cours : `python3 .claude/scripts/tasks.py start <numéro>` (étiquette `en-cours`, assigné à l'utilisateur, statut « In Progress » du Project).
- Annonce un court plan.

## 3. Implémenter

- Suis les critères d'acceptation point par point et suis leur avancement pendant le travail.
- Pour `scoring`, les règles de temps et les règles de badges : **écris d'abord les tests**.
- **Pas de code d'avance** : crée seulement ce que le ticket utilise (fonction, type, helper, package, variable de configuration, cible `make`). Quand un besoin n'était pas prévu, ajoute-le au ticket en cours et signale-le dans le résumé.
- Ne modifie pas de fichier hors du périmètre du ticket. Si tu découvres un travail nécessaire hors périmètre, note-le (nouveau ticket proposé) sans le faire.
- Respecte la définition de terminé de `TASKS.md` (section Conventions).

## 4. Vérifier

- `make fmt && make lint && make test` ; ajoute `make test-integration` si le ticket touche au SQL ou aux migrations.
- Migration : vérifie `make migrate-up` → `make migrate-down` → `make migrate-up` sur la base locale.
- Lance les agents indiqués dans le ticket (champ **Revue**) et corrige les points bloquants.

## 5. Clôturer, sans committer

- Si **tous** les critères d'acceptation sont remplis : `python3 .claude/scripts/tasks.py tick <numéro>` coche les cases dans l'issue. S'il en reste un non rempli, ne lance pas `tick` : dis lequel et pourquoi.
- Ne ferme pas l'issue et ne change pas son statut : elle se ferme toute seule à la fusion de la pull request qui contient `Closes #<numéro>`.
- Présente : la liste des fichiers modifiés, un résumé du diff, le résultat des vérifications, les points ouverts éventuels, et le **message de commit** du ticket (adapté si besoin), terminé par `Closes #<numéro>` puis `Refs: SC-API-0xx` en pied.
- **N'exécute ni `git commit`, ni `git push`, ni `gh pr create`.** Attends l'accord explicite de l'utilisateur. Tu peux utiliser `git add`, `git status` et `git diff`. Propose le titre et la description de la pull request (avec `Closes #<numéro>`) pour qu'il l'ouvre ou te le demande.
- Termine en indiquant que le ticket suivant sera disponible une fois cette pull request fusionnée (ses dépendances seront alors fermées), et ses éventuelles décisions en attente. L'utilisateur peut aussi demander explicitement un autre ticket.

## Rappels

- Anglais pour le code, les commentaires et les messages de commit ; français pour la documentation.
- Jamais de `time.Now()` dans la logique métier : passe par `platform/clock`.
- Jamais d'autorisation dans un handler ou un middleware : dans le service, avec les fonctions de `policy.go`.
- Jamais de `terraform apply` ni de déploiement sans demande explicite.
