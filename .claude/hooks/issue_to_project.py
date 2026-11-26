#!/usr/bin/env python3
"""Hook PostToolUse (outil Bash) : toute issue créée avec `gh issue create` rejoint le GitHub Project.

Lit les URL d'issue dans la sortie de la commande et lance `tasks.py add <numéro>` pour chacune
(ajout au Project « Sport Challenge », statut « Todo »). Le résultat est renvoyé à Claude comme contexte.
"""
import json
import os
import re
import subprocess
import sys

try:
    data = json.load(sys.stdin)
except Exception:
    sys.exit(0)

cmd = (data.get("tool_input") or {}).get("command", "") or ""
if data.get("tool_name") != "Bash" or not re.search(r"\bgh\s+issue\s+create\b", cmd):
    sys.exit(0)

output = json.dumps(data.get("tool_response") or "")
numbers = sorted(set(re.findall(r"https://github\.com/[\w.-]+/[\w.-]+/issues/(\d+)", output)), key=int)
if not numbers:
    sys.exit(0)

root = os.environ.get("CLAUDE_PROJECT_DIR", ".")
script = os.path.join(root, ".claude", "scripts", "tasks.py")
lines = []
for n in numbers:
    p = subprocess.run([sys.executable, "-B", script, "add", n], capture_output=True, text=True, cwd=root)
    lines.append((p.stdout or p.stderr).strip() or f"#{n} : ajout au Project sans réponse (code {p.returncode}).")

print(json.dumps({
    "hookSpecificOutput": {
        "hookEventName": "PostToolUse",
        "additionalContext": "Project GitHub : " + " | ".join(lines),
    }
}))
