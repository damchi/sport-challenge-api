#!/usr/bin/env python3
"""Backlog Sport Challenge dans GitHub : import de TASKS.md puis suivi des tickets.

Le script utilise la CLI `gh` (connecte-toi d'abord : `gh auth login`, puis `gh auth refresh -s project`).
Il est identique dans les dépôts backend et frontend ; le préfixe des tickets (SC-API ou SC-WEB)
est lu dans TASKS.md. À lancer depuis la racine du dépôt.

Commandes
  import [--dry-run] [--done 001,002] [--project-title T] [--no-project]
        Crée une issue par ticket de TASKS.md, avec ses étiquettes, et les ajoute au GitHub Project.
        Sûr à relancer : un ticket déjà présent (même identifiant dans le titre) n'est jamais recréé.
  next  Affiche le prochain ticket à traiter (ouvert, dépendances fermées, ni reporté ni en cours).
  start TICKET
        Marque le ticket « en cours » (étiquette, assigné à toi, statut du Project).
  tick  TICKET
        Coche les critères d'acceptation du ticket (à lancer quand ils sont tous remplis).

TICKET s'écrit `12`, `#12`, `SC-API-006` ou `006`.
"""
import argparse
import json
import re
import subprocess
import sys
import tempfile
import time
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
TASKS = ROOT / "TASKS.md"
DEFAULT_PROJECT = "Sport Challenge"

HEAD = re.compile(r"^### (SC-[A-Z]+-\d+) · (.+)$")
ID_IN_TITLE = re.compile(r"^(SC-[A-Z]+)-(\d+) · ")
BLOCK_END = re.compile(r"^(#{1,3} |---\s*$)")
SKIP_LABELS = {"v1.1", "blocked", "needs-decision", "en-cours"}
EPIC_COLORS = ["5319e7", "0e8a16", "1d76db", "d93f0b", "fbca04", "006b75", "b60205", "0052cc", "c5def5", "bfd4f2"]


class Ticket:
    def __init__(self, tid, title):
        self.id = tid
        self.title = f"{tid} · {title}"
        self.epic = ""
        self.type = ""
        self.phase = ""
        self.points = ""
        self.deps = []
        self.backend = ""
        self.deferred = False
        self.body = ""

    @property
    def labels(self):
        labels = [self.epic, f"type:{self.type}", f"points:{self.points}"]
        if self.phase:
            labels.append(f"phase:{self.phase}")
        if self.deferred:
            labels.append("v1.1")
        return [label for label in labels if label and not label.endswith(":")]


def parse_tasks(path):
    lines = path.read_text(encoding="utf-8").splitlines()
    tickets, i = [], 0
    while i < len(lines):
        m = HEAD.match(lines[i])
        if not m:
            i += 1
            continue
        prefix = m.group(1).rsplit("-", 1)[0]
        t = Ticket(m.group(1), m.group(2))
        i += 1
        block = []
        while i < len(lines) and not BLOCK_END.match(lines[i]):
            block.append(lines[i])
            i += 1
        while block and not block[0].strip():
            block.pop(0)
        meta = block.pop(0) if block else ""
        t.epic = (re.search(r"`([A-Z]\d+)`", meta) or [None, ""])[1]
        t.type = (re.search(r"` · (\w+)", meta) or [None, ""])[1].lower()
        t.phase = (re.search(r"Phase (\w+)", meta) or [None, ""])[1]
        t.points = (re.search(r"Estimation (\d+)", meta) or [None, ""])[1]
        deps = re.search(r"Dépend de : ([^·]*)", meta)
        t.deps = [f"{prefix}-{n}" for n in re.findall(r"\d{3}", deps.group(1))] if deps else []
        back = re.search(r"Backend : (.*)$", meta)
        t.backend = back.group(1).strip() if back else ""
        kept = []
        for line in block:
            s = re.match(r"\*\*Statut\*\* : (.*)", line)
            if s:
                t.deferred = s.group(1).startswith("Reporté")
            else:
                kept.append(line)
        while kept and not kept[-1].strip():
            kept.pop()
        t.body = "\n".join(kept)
        tickets.append(t)
    return tickets


def render(t, numbers):
    def ref(d):
        return f"#{numbers[d]} ({d})" if d in numbers else d

    out = [
        f"<!-- sc-ticket: {t.id} -->",
        f"<!-- sc-deps: {','.join(t.deps)} -->",
        f"**Épopée** `{t.epic}` · **Type** {t.type.capitalize()} · **Phase** {t.phase} · **Estimation** {t.points} pts",
        "**Dépend de** : " + (", ".join(ref(d) for d in t.deps) if t.deps else "—"),
    ]
    if t.backend:
        out.append(f"**Backend** : {t.backend}")
    if t.deferred:
        out.append("**Reporté en V1.1** : ne pas prendre en V1.")
    out += ["", t.body, "", "---", "_Ticket importé depuis `TASKS.md`. Les statuts et les cases à cocher vivent maintenant ici._"]
    return "\n".join(out) + "\n"


def gh(*args, stdin=None, check=True):
    p = subprocess.run(["gh", *args], input=stdin, capture_output=True, text=True)
    if check and p.returncode != 0:
        sys.exit(f"gh {' '.join(args[:3])} a échoué : {p.stderr.strip() or p.stdout.strip()}")
    return p.stdout.strip()


def gh_json(*args):
    out = gh(*args)
    return json.loads(out) if out else None


def repo_info():
    return gh_json("repo", "view", "--json", "nameWithOwner,owner")


def find_project(owner, title, create=False):
    projects = gh_json("project", "list", "--owner", owner, "--format", "json", "--limit", "100")
    for p in (projects or {}).get("projects", []):
        if p["title"] == title:
            return p
    if not create:
        return None
    return gh_json("project", "create", "--owner", owner, "--title", title, "--format", "json")


def cmd_import(args):
    tickets = parse_tasks(TASKS)
    if not tickets:
        sys.exit("Aucun ticket trouvé dans TASKS.md.")
    ids = {t.id for t in tickets}
    for t in tickets:
        missing = [d for d in t.deps if d not in ids]
        if missing or not (t.epic and t.type and t.points):
            sys.exit(f"{t.id} : métadonnées invalides (dépendances inconnues {missing}, épopée {t.epic!r}, type {t.type!r}, points {t.points!r}).")
    done = {f"{tickets[0].id.rsplit('-', 1)[0]}-{n}" for n in re.findall(r"\d{3}", args.done or "")}
    unknown = done - ids
    if unknown:
        sys.exit(f"--done : tickets inconnus {sorted(unknown)}")

    total = sum(int(t.points) for t in tickets)
    v1 = sum(int(t.points) for t in tickets if not t.deferred)
    print(f"{len(tickets)} tickets, {total} points ({v1} en V1, {total - v1} reportés en V1.1).")
    if args.dry_run:
        for t in tickets:
            flag = " [fermé]" if t.id in done else ""
            print(f"  {t.title[:70]:<70} {', '.join(t.labels)}{flag}")
        print("\nEssai à blanc : rien n'a été créé.")
        return

    info = repo_info()
    repo, owner = info["nameWithOwner"], info["owner"]["login"]
    print(f"Dépôt : {repo}")

    epics = sorted({t.epic for t in tickets})
    wanted = {e: (EPIC_COLORS[i % len(EPIC_COLORS)], f"Épopée {e}") for i, e in enumerate(epics)}
    for t in tickets:
        wanted.setdefault(f"type:{t.type}", ("c2e0c6", "Type de ticket"))
        wanted.setdefault(f"points:{t.points}", ("ededed", "Estimation en points"))
        if t.phase:
            wanted.setdefault(f"phase:{t.phase}", ("f9d0c4", "Phase"))
    wanted["v1.1"] = ("cccccc", "Reporté en V1.1")
    wanted["en-cours"] = ("fbca04", "Ticket en cours de développement")
    wanted["blocked"] = ("b60205", "Bloqué")
    wanted["needs-decision"] = ("d876e3", "Décision de l'utilisateur attendue")
    for name, (color, desc) in wanted.items():
        gh("label", "create", name, "--color", color, "--description", desc, "--force")

    project = None
    if not args.no_project:
        project = find_project(owner, args.project_title, create=True)
        print(f"Project : {project['title']} (n° {project['number']})")

    existing = {}
    for issue in gh_json("issue", "list", "--state", "all", "--limit", "1000", "--json", "number,title,state") or []:
        m = ID_IN_TITLE.match(issue["title"])
        if m:
            existing[f"{m.group(1)}-{m.group(2)}"] = issue

    numbers, created, pending = {}, [], []
    for t in tickets:
        if t.id in existing:
            numbers[t.id] = existing[t.id]["number"]
            print(f"  = {t.id} existe déjà (#{numbers[t.id]})")
            continue
        body = render(t, numbers)
        cmd = ["issue", "create", "--title", t.title, "--body-file", "-"]
        for label in t.labels:
            cmd += ["--label", label]
        url = gh(*cmd, stdin=body)
        numbers[t.id] = int(url.rstrip("/").rsplit("/", 1)[1])
        created.append((t, url))
        if any(d not in numbers for d in t.deps):
            pending.append(t)
        print(f"  + {t.id} → #{numbers[t.id]}")
        time.sleep(0.4)

    for t in pending:
        gh("issue", "edit", str(numbers[t.id]), "--body-file", "-", stdin=render(t, numbers))

    if project:
        for t in tickets:
            url = f"https://github.com/{repo}/issues/{numbers[t.id]}"
            gh("project", "item-add", str(project["number"]), "--owner", owner, "--url", url)

    for t in tickets:
        n = numbers[t.id]
        state = existing[t.id]["state"] if t.id in existing else "OPEN"
        if t.id in done and state == "OPEN":
            gh("issue", "close", str(n), "--reason", "completed")
            print(f"  ✓ {t.id} (#{n}) fermé")

    print(f"\nTerminé : {len(created)} issues créées, {len(tickets) - len(created)} déjà présentes.")


def load_issues():
    prefix = None
    if TASKS.exists():
        m = re.search(r"^### (SC-[A-Z]+)-\d+ · ", TASKS.read_text(encoding="utf-8"), re.M)
        prefix = m.group(1) if m else None
    issues = gh_json("issue", "list", "--state", "all", "--limit", "1000", "--json", "number,title,state,labels,body,url") or []
    result = {}
    for i in issues:
        m = ID_IN_TITLE.match(i["title"])
        if m and (prefix is None or m.group(1) == prefix):
            i["tid"] = f"{m.group(1)}-{m.group(2)}"
            i["label_names"] = {label["name"] for label in i["labels"]}
            dm = re.search(r"<!-- sc-deps: ([^>]*?) -->", i["body"] or "")
            i["deps"] = [d for d in dm.group(1).split(",") if d] if dm else []
            result[i["tid"]] = i
    if not result:
        sys.exit("Aucune issue de ticket trouvée. Lance d'abord : python3 .claude/scripts/tasks.py import")
    return result


def cmd_next(_args):
    tickets = load_issues()
    ordered = sorted(tickets.values(), key=lambda i: i["number"])
    in_progress = [i for i in ordered if i["state"] == "OPEN" and "en-cours" in i["label_names"]]
    for i in in_progress:
        print(f"EN COURS : #{i['number']} {i['title']}\n  {i['url']}")
    waiting = []
    for i in ordered:
        if i["state"] != "OPEN" or i["label_names"] & SKIP_LABELS:
            continue
        open_deps = [d for d in i["deps"] if d in tickets and tickets[d]["state"] == "OPEN"]
        if open_deps:
            waiting.append((i, open_deps))
            continue
        print(f"SUIVANT : #{i['number']} {i['title']}\n  {i['url']}")
        if i["deps"]:
            print("  dépendances terminées : " + ", ".join(i["deps"]))
        return
    if waiting:
        print("Aucun ticket disponible : les tickets ouverts attendent des dépendances.")
        for i, deps in waiting[:5]:
            print(f"  #{i['number']} {i['tid']} attend {', '.join(deps)}")
    else:
        print("Aucun ticket restant (reportés, bloqués ou en attente de décision exclus).")


def resolve(tickets, ref):
    ref = ref.strip().lstrip("#")
    if ref in tickets:
        return tickets[ref]
    for i in tickets.values():
        if str(i["number"]) == ref or i["tid"].endswith("-" + ref.zfill(3)):
            return i
    sys.exit(f"Ticket introuvable : {ref}")


def set_project_status(repo, owner, number, wanted):
    try:
        project = find_project(owner, DEFAULT_PROJECT)
        if not project:
            print("Project introuvable : statut du Project non modifié.")
            return
        pnum = str(project["number"])
        fields = gh_json("project", "field-list", pnum, "--owner", owner, "--format", "json")["fields"]
        status = next(f for f in fields if f["name"] == "Status")
        option = next(o for o in status["options"] if o["name"].lower() in wanted)
        items = gh_json("project", "item-list", pnum, "--owner", owner, "--format", "json", "--limit", "1000")["items"]
        item = next(it for it in items if it.get("content", {}).get("number") == number and it["content"].get("repository") == repo)
        gh("project", "item-edit", "--id", item["id"], "--project-id", project["id"], "--field-id", status["id"], "--single-select-option-id", option["id"])
    except (StopIteration, KeyError, TypeError) as e:
        print(f"Statut du Project non modifié ({type(e).__name__}) : fais-le à la main si besoin.")


def cmd_start(args):
    issue = resolve(load_issues(), args.ticket)
    if issue["state"] != "OPEN":
        sys.exit(f"{issue['tid']} est déjà fermé.")
    gh("issue", "edit", str(issue["number"]), "--add-label", "en-cours", "--add-assignee", "@me")
    info = repo_info()
    set_project_status(info["nameWithOwner"], info["owner"]["login"], issue["number"], {"in progress", "en cours"})
    print(f"#{issue['number']} {issue['tid']} : en cours.")


def cmd_tick(args):
    issue = resolve(load_issues(), args.ticket)
    body = issue["body"] or ""
    m = re.search(r"(\*\*Critères d'acceptation\*\*\n)(.*?)(\n\*\*|\Z)", body, re.S)
    if not m:
        sys.exit("Section « Critères d'acceptation » introuvable dans l'issue.")
    ticked = m.group(2).replace("- [ ]", "- [x]")
    new = body[: m.start(2)] + ticked + body[m.end(2):]
    gh("issue", "edit", str(issue["number"]), "--body-file", "-", stdin=new)
    print(f"#{issue['number']} {issue['tid']} : {m.group(2).count('- [ ]')} critère(s) coché(s).")


def main():
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    sub = parser.add_subparsers(dest="cmd", required=True)
    imp = sub.add_parser("import")
    imp.add_argument("--dry-run", action="store_true")
    imp.add_argument("--done", help="tickets déjà faits à fermer, ex. 001,002")
    imp.add_argument("--project-title", default=DEFAULT_PROJECT)
    imp.add_argument("--no-project", action="store_true")
    sub.add_parser("next")
    for name in ("start", "tick"):
        sub.add_parser(name).add_argument("ticket")
    args = parser.parse_args()
    {"import": cmd_import, "next": cmd_next, "start": cmd_start, "tick": cmd_tick}[args.cmd](args)


if __name__ == "__main__":
    main()
