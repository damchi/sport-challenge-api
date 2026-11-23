# ADR 0001 : activité globale et verrouillage des semaines

- **Statut** : accepté (décision D1, `TASKS.md`)
- **Date** : 2026-11-09
- **Tickets concernés** : SC-API-041 à 046, 052, 062, 121 (verrouillage à 7 h), 130 (écriture au moment du verrouillage)

## Contexte

Un utilisateur peut participer à plusieurs challenges en même temps, chacun avec sa fenêtre `[starts_at, ends_at)`, son fuseau horaire et donc ses propres semaines (README §2.3). Une même sortie de course peut compter dans plusieurs d'entre eux.

Deux modèles étaient possibles :

- une activité **par challenge** : l'utilisateur saisit la même sortie plusieurs fois, au risque d'écarts entre les saisies ;
- une activité **globale** : saisie une fois, rattachée à l'utilisateur, comptée partout où elle a sa place.

Le second modèle est aussi celui des imports à venir (Strava, Garmin, Polar, Coros, README §7) : un fournisseur envoie une activité de l'utilisateur, pas une activité d'un challenge.

Il fallait aussi fixer ce qui verrouille une activité, puisqu'une semaine se verrouille à 7 h, heure locale, le premier jour de la semaine suivante (README §2.3) et que les semaines diffèrent d'un challenge à l'autre.

## Décision

1. **Saisie unique.** Une activité est saisie **une seule fois** et appartient à l'utilisateur. Elle n'a pas de lien stocké vers un challenge.
2. **Où elle compte.** Elle compte dans **chaque challenge** où l'utilisateur est participant **actif** et dont la fenêtre contient sa date de début (`started_at ∈ [starts_at, ends_at)`). C'est le statut de participation qui décide, jamais le rôle : un créateur qui a abandonné ne concourt plus. Les challenges supprimés sont ignorés. L'ensemble est calculé à la lecture (classement recalculé depuis les activités brutes, README §2.6), jamais stocké.
3. **Verrouillage.** Une activité ne peut être **créée, modifiée ni supprimée** si sa date de début tombe dans une **semaine verrouillée** d'au moins un des challenges où elle compte (challenges supprimés ignorés). Une semaine se verrouille à **7 h, heure locale de ce challenge, le premier jour de la semaine suivante** (pour la dernière, le premier jour après le challenge, le jour de `ends_at`), au premier instant qui existe si un changement d'heure saute ou répète 7 h (décision du 2026-11-20, SC-API-121, qui remplace le verrouillage à l'instant exact de la fin). L'activité compte entièrement pour la semaine de son début, même si elle finit dans la suivante. Un challenge que l'utilisateur a quitté (abandon ou exclusion) verrouille encore les semaines **déjà verrouillées à l'instant de son départ** (décision du 2026-11-20, SC-API-140) : il reste dans les mini-classements Points de ces semaines (README §2.3), dont les points ne doivent plus bouger. Pour une modification, la règle s'applique à la date de début actuelle **et** à la nouvelle. Réponse : `week_locked`. L'horloge fait foi, pas le passage du worker.
4. **Activité hors de toute fenêtre : acceptée.** Une activité dont la date de début ne tombe dans la fenêtre d'aucun challenge où l'utilisateur est participant actif est **enregistrée**, sans compter nulle part pour l'instant. Exemple : un utilisateur court sans participer à aucun challenge, puis rejoint un challenge en cours dont la fenêtre contient cette sortie ; elle compte alors dans ce challenge (README §2.1 : les activités faites avant l'inscription, mais dans la fenêtre, comptent). Il n'y a donc pas de code `activity_outside_window`.

La validation propre à l'activité (durée, distance selon le sport, date jamais dans le futur) relève de `activity/rules.go` (ticket 042), pas de cet ADR.

## Conséquences

- **Les semaines diffèrent d'un challenge à l'autre** (dates de début et fuseaux différents). Une activité peut donc être verrouillée par un challenge et pas par un autre ; il suffit d'**un** challenge où sa semaine est verrouillée pour qu'elle ne bouge plus nulle part. Chaque challenge verrouille à 7 h dans son propre fuseau.
- Une activité qui ne compte dans aucun challenge n'est verrouillée par aucun, sauf par un challenge quitté dont sa semaine était déjà verrouillée au départ (point 3) : elle reste modifiable et supprimable jusqu'à ce que sa semaine se verrouille dans un challenge où elle compte.
- Rejoindre un challenge peut faire compter des activités déjà saisies. L'inscription n'étant possible que jusqu'à la fin de la semaine 1, les activités d'avant l'inscription tombent dans une semaine non verrouillée au moment de rejoindre, puis se verrouillent avec elle.
- Abandonner (ou être exclu) retire l'activité de ce challenge : elle n'y compte plus en temps ni en distance, et ce challenge ne verrouille plus les semaines verrouillées après le départ. Les semaines déjà verrouillées au départ le restent (point 3) : sans cela, la personne pourrait modifier, supprimer ou antidater une activité d'une semaine jugée et changer les points acquis des autres, dès qu'aucun autre challenge où elle est active ne la verrouille (SC-API-140).
- Une activité datée de la fenêtre d'un challenge terminé est acceptée jusqu'à 7 h le premier jour après le challenge (sa dernière semaine n'est pas encore verrouillée), puis refusée par le verrouillage (README §2.5 : impossible d'importer une activité après la fin).
- **Écriture au moment du verrouillage** (SC-API-130) :
  - **Mécanisme.** Une écriture d'activité (création, modification, suppression) prend un verrou consultatif PostgreSQL **partagé** avant de lire l'horloge pour vérifier le verrouillage, et le garde jusqu'à la fin de sa transaction. Chaque exécution de `close-weeks` et de `finalize-challenges` lit son horloge, puis prend ce verrou en **exclusif** un instant : elle attend ainsi la fin de toute écriture qui a vérifié le verrouillage avant cet instant. Une activité acceptée juste avant 7 h figure donc toujours dans le jugement de sa semaine et dans les résultats figés, quel que soit l'ordre de validation ; une écriture qui commence après lit une horloge plus tardive et est refusée.
  - **Attente.** Les écritures ne s'attendent pas entre elles ; pendant que le worker attend, les nouvelles écritures attendent derrière lui, le temps que celles en cours se terminent. Le worker n'attend pas plus de 30 secondes : au-delà, l'exécution échoue sans rien juger et la suivante donne le même résultat.
  - **Limite d'horloge.** La garantie suppose que l'horloge de l'API et celle du worker concordent ; si l'API retarde de d sur le worker, une écriture vérifiée dans les d avant le verrouillage, selon l'horloge du worker, peut encore échapper au jugement.
  - **Suppression de compte.** Seul `activity.Service` écrit les activités ; la suppression de compte les efface sans ce verrou (elle retire le compte partout, résultats figés compris, README §2.2).
  - **Recalcul par un admin** (SC-API-160, README §2.12). Une modification admin qui touche au passé d'un défi prend ce verrou en **exclusif** en tout premier dans sa transaction, avant toute ligne (dans l'ordre des écritures d'activité : le verrou, puis les lignes), et le garde jusqu'à la fin de cette transaction ; le recalcul (`closure.Service.Recompute`) lit l'horloge une fois le verrou acquis. Le worker traite chaque défi dans une transaction qui tient le verrou en **partagé** et relit le défi une fois le verrou pris : un recalcul et un passage du worker sur le même défi se succèdent, ils ne s'entrelacent jamais, et le second juge ce que le premier a validé. Les écritures d'activité n'attendent pas le worker ; pendant une modification admin, elles et le worker attendent sa fin.
- Une activité à cheval sur la fin d'une semaine (une course du dimanche 23:30 à 00:30) se saisit une fois finie, avant 7 h : elle compte pour la semaine de son début.
- Saisir une activité ne demande pas d'être participant d'un challenge : il suffit d'un profil complet. La réponse de `POST /activities` liste les challenges où l'activité compte, et cette liste peut être vide.
- Le verrouillage et l'ensemble des challenges où une activité compte dépendent de la participation : `activity/policy.go` (ticket 043) les obtient du module `challenge` par une petite interface définie côté `activity`, sans importer son repository.
