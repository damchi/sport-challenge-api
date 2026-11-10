# ADR 0001 : activité globale et verrouillage des semaines

- **Statut** : accepté (décision D1, `TASKS.md`)
- **Date** : 2026-11-09
- **Tickets concernés** : SC-API-041 à 046, 052, 062

## Contexte

Un utilisateur peut participer à plusieurs challenges en même temps, chacun avec sa fenêtre `[starts_at, ends_at)`, son fuseau horaire et donc ses propres semaines (README §2.3). Une même sortie de course peut compter dans plusieurs d'entre eux.

Deux modèles étaient possibles :

- une activité **par challenge** : l'utilisateur saisit la même sortie plusieurs fois, au risque d'écarts entre les saisies ;
- une activité **globale** : saisie une fois, rattachée à l'utilisateur, comptée partout où elle a sa place.

Le second modèle est aussi celui des imports à venir (Strava, Garmin, Polar, Coros, README §7) : un fournisseur envoie une activité de l'utilisateur, pas une activité d'un challenge.

Il fallait aussi fixer ce qui verrouille une activité, puisqu'une semaine terminée est verrouillée (README §2.3) et que les semaines diffèrent d'un challenge à l'autre.

## Décision

1. **Saisie unique.** Une activité est saisie **une seule fois** et appartient à l'utilisateur. Elle n'a pas de lien stocké vers un challenge.
2. **Où elle compte.** Elle compte dans **chaque challenge** où l'utilisateur est participant **actif** et dont la fenêtre contient sa date de début (`started_at ∈ [starts_at, ends_at)`). C'est le statut de participation qui décide, jamais le rôle : un créateur qui a abandonné ne concourt plus. Les challenges supprimés sont ignorés. L'ensemble est calculé à la lecture (classement recalculé depuis les activités brutes, README §2.6), jamais stocké.
3. **Verrouillage.** Une activité ne peut être **créée, modifiée ni supprimée** si sa date de début tombe dans une **semaine terminée** d'au moins un des challenges où elle compte (challenges supprimés ignorés). Pour une modification, la règle s'applique à la date de début actuelle **et** à la nouvelle. Réponse : `week_locked`. L'horloge fait foi, pas le passage du worker.
4. **Activité hors de toute fenêtre : acceptée.** Une activité dont la date de début ne tombe dans la fenêtre d'aucun challenge où l'utilisateur est participant actif est **enregistrée**, sans compter nulle part pour l'instant. Exemple : un utilisateur court sans participer à aucun challenge, puis rejoint un challenge en cours dont la fenêtre contient cette sortie ; elle compte alors dans ce challenge (README §2.1 : les activités faites avant l'inscription, mais dans la fenêtre, comptent). Il n'y a donc pas de code `activity_outside_window`.

La validation propre à l'activité (durée, distance selon le sport, date jamais dans le futur) relève de `activity/rules.go` (ticket 042), pas de cet ADR.

## Conséquences

- **Les semaines diffèrent d'un challenge à l'autre** (dates de début et fuseaux différents). Une activité peut donc être verrouillée par un challenge et pas par un autre ; il suffit d'**un** challenge où sa semaine est terminée pour qu'elle ne bouge plus nulle part.
- Une activité qui ne compte dans aucun challenge n'est verrouillée par aucun : elle reste modifiable et supprimable jusqu'à ce que sa semaine se termine dans un challenge où elle compte.
- Rejoindre un challenge peut faire compter des activités déjà saisies. L'inscription n'étant possible que jusqu'à la fin de la semaine 1, les activités d'avant l'inscription tombent dans une semaine non terminée au moment de rejoindre, puis se verrouillent avec elle.
- Abandonner (ou être exclu) retire l'activité de ce challenge : elle n'y compte plus et ce challenge ne la verrouille plus.
- Une activité datée de la fenêtre d'un challenge terminé tombe dans sa dernière semaine, qui est terminée : elle est refusée par le verrouillage (README §2.5 : impossible d'importer une activité après la fin).
- Saisir une activité ne demande pas d'être participant d'un challenge : il suffit d'un profil complet. La réponse de `POST /activities` liste les challenges où l'activité compte, et cette liste peut être vide.
- Le verrouillage et l'ensemble des challenges où une activité compte dépendent de la participation : `activity/policy.go` (ticket 043) les obtient du module `challenge` par une petite interface définie côté `activity`, sans importer son repository.
