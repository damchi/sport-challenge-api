---
name: next-task
description: Prend la prochaine issue GitHub du backlog du backend Sport Challenge, l'implémente, committe, pousse et vérifie les commentaires de la pull request. À utiliser quand on dit « prochain ticket », « next task », « prends le ticket SC-API-0xx », « on continue le backlog » ou « quelle est la suite ».
---

# Traiter un ticket du backlog

Le backlog est dans les **issues GitHub** (tickets `SC-API-001` à `SC-API-075`, numérotation non consécutive : les tickets listés dans « Tickets fusionnés » de `TASKS.md` n'existent plus). **Un ticket = un commit.** Ne traite jamais deux tickets d'affilée sans que la pull request du premier soit fusionnée ou que l'utilisateur te le demande.

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
- **Branche** (une par ticket, créée automatiquement) :
  1. `git status` : si l'arbre de travail n'est pas propre, **arrête-toi** et demande quoi faire (le travail non committé n'est jamais écrasé).
  2. Si l'utilisateur est déjà sur une branche de ce ticket (`sc-api-0xx-…`), reprends-la.
  3. Sinon : `git switch main`, `git pull --ff-only`, puis `git switch -c <identifiant en minuscules>-<résumé court du titre>` (ex. `sc-api-002-makefile-lint`) : minuscules, tirets, 40 caractères au plus pour le résumé.
  4. Ne pars jamais d'une autre branche de ticket non fusionnée sans l'accord de l'utilisateur.
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

## 5. Clôturer : commit, push et revue de la pull request

- Si **tous** les critères d'acceptation sont remplis : `python3 .claude/scripts/tasks.py tick <numéro>` coche les cases dans l'issue. S'il en reste un non rempli, ne lance pas `tick` : dis lequel et pourquoi.
- Ne ferme pas l'issue et ne change pas son statut : elle se ferme toute seule à la fusion de la pull request qui contient `Closes #<numéro>`.
- **Commit et push sans demander l'accord** de l'utilisateur, une fois les vérifications du §4 passées : `git add` des seuls fichiers du ticket, un commit cohérent avec le **message de commit** du ticket (adapté si besoin), terminé par `Closes #<numéro>` puis `Refs: SC-API-0xx` en pied, puis push de la branche du ticket. Jamais de push forcé, de `--no-verify`, de reset destructif ni de nettoyage forcé du dépôt. Ne pousse jamais sur `main`.
- La pull request s'ouvre sur demande de l'utilisateur : propose le titre et la description (avec `Closes #<numéro>`). Ne fusionne jamais.
- **Commentaires de la pull request** : pour chaque pull request du ticket, lis tous les commentaires avant de dire que le travail est terminé, puis après chaque push : `gh pr view <numéro> --comments`, `gh api repos/{owner}/{repo}/pulls/<numéro>/comments` (commentaires en ligne des relectures) et `gh pr checks <numéro>`. Traite chaque point : corrige-le (nouveau commit sur la même branche, poussé) ou explique pourquoi tu ne le suis pas. Un point qui change une règle produit (🟡 ou ❓) ou qui sort du périmètre du ticket se demande à l'utilisateur.
- Présente : la liste des fichiers modifiés, un résumé du diff, le résultat des vérifications, les commits poussés, l'état des commentaires de la pull request et les points ouverts éventuels.
- Termine en indiquant que le ticket suivant sera disponible une fois cette pull request fusionnée (ses dépendances seront alors fermées), et ses éventuelles décisions en attente. L'utilisateur peut aussi demander explicitement un autre ticket.

## Rappels

- Anglais pour le code, les commentaires et les messages de commit ; français pour la documentation.
- Jamais de `time.Now()` dans la logique métier : passe par `platform/clock`.
- Jamais d'autorisation dans un handler ou un middleware : dans le service, avec les fonctions de `policy.go`.
- Jamais de `terraform apply` ni de déploiement sans demande explicite.
