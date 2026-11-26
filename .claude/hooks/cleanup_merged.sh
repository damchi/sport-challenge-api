#!/bin/sh
# Hook SessionStart : supprime en local et sur GitHub les branches dont la pull request est fusionnée.
# Ne bloque jamais le démarrage de la session (erreurs ignorées, message seulement si une branche est supprimée).
cd "${CLAUDE_PROJECT_DIR:-.}" || exit 0
python3 -B .claude/scripts/tasks.py cleanup --quiet 2>/dev/null || true
exit 0
