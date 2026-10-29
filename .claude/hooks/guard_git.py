#!/usr/bin/env python3
"""Hook PreToolUse (outil Bash) : aucune écriture git ni action de dépôt distant sans validation.

- commit, push, merge, rebase, tag, cherry-pick, revert, am, reset, gh pr create/merge… -> demande de validation
- push forcé, reset --hard, clean, --no-verify -> refusé
Détecte aussi les variantes : `git -C dir commit`, `git -c k=v push`, commandes chaînées (&&, ;, |), sous-shell.
Sans sortie = on laisse les règles de permissions décider.
"""
import json
import re
import sys

try:
    data = json.load(sys.stdin)
except Exception:
    sys.exit(0)

if data.get("tool_name") != "Bash":
    sys.exit(0)

cmd = (data.get("tool_input") or {}).get("command", "") or ""

# `git` suivi d'options globales éventuelles, puis d'un sous-commande
GIT = r"\bgit(?:\s+(?:-C\s+\S+|-c\s+\S+|--[\w-]+(?:=\S+)?|-[A-Za-z]))*\s+"
WRITE_SUBCOMMANDS = r"(commit|push|merge|rebase|tag|cherry-pick|revert|am|reset|stash\s+(?:drop|clear)|branch\s+-[dD]|worktree\s+remove)\b"
GH = r"\bgh\s+(?:pr\s+(?:create|merge|close|ready)|release\s+(?:create|delete)|repo\s+(?:delete|create)|workflow\s+run)\b"


def emit(decision, reason):
    print(json.dumps({
        "hookSpecificOutput": {
            "hookEventName": "PreToolUse",
            "permissionDecision": decision,
            "permissionDecisionReason": reason,
        }
    }))
    sys.exit(0)


# --- Refus nets ---
if re.search(GIT + r"push\b.*(?:\s--force\b|\s--force-with-lease\b|\s-f\b|\s\+\S+)", cmd):
    emit("deny", "Push forcé interdit sur ce projet.")
if re.search(GIT + r"reset\b.*--hard", cmd) or re.search(GIT + r"clean\b", cmd):
    emit("deny", "git reset --hard et git clean sont interdits : ils détruisent du travail non commité.")
if re.search(GIT + r"(?:commit|push|merge|rebase|am|cherry-pick)\b.*--no-verify", cmd):
    emit("deny", "Contourner les vérifications avec --no-verify est interdit.")

# --- Validation obligatoire ---
if re.search(GIT + WRITE_SUBCOMMANDS, cmd) or re.search(GH, cmd):
    emit(
        "ask",
        "Action git ou dépôt distant : l'utilisateur doit valider explicitement "
        "(fichiers concernés et message de commit à relire avant).",
    )

sys.exit(0)
