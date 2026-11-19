# Sport Challenge — API (backend)

Projet **backend** de l'application de challenges sportifs entre amis : API Go, base PostgreSQL, tâches planifiées.

Le **frontend** (web et mobile) est un projet séparé, avec son propre README. Le contrat entre les deux est la spécification OpenAPI de ce dépôt (`api/openapi.yaml`).

> **Légende** : ✅ décidé · 🟡 proposition par défaut, à confirmer · ❓ question ouverte

---

## 1. Concept

Une personne crée un ou plusieurs challenges sportifs, choisit une durée, la visibilité (privé ou public) et si les participants peuvent inviter à leur tour. Le classement est mis à jour pour chaque catégorie. Les activités sont saisies à la main en V1, puis synchronisées depuis Strava, Garmin, Polar et Coros dans une version ultérieure.

---

## 2. Règles fonctionnelles

### 2.1 Challenges

- ✅ Un utilisateur peut créer plusieurs challenges.
- ✅ Le créateur choisit la durée en **nombre entier de semaines, de 2 à 52** (ex. 6 semaines) : pas de dernière semaine partielle.
- ✅ Le challenge commence à minuit (heure locale) d'un jour choisi entre **aujourd'hui et un an plus tard**, dans son fuseau.
- ✅ Là où un changement d'heure saute minuit (Chili, Égypte, Liban…), le jour commence au **premier instant qui existe** ce jour-là (par exemple 01:00) : jamais la veille. La même règle vaut pour chaque début de semaine.
- ✅ Un challenge a **toujours les 3 catégories** ; le créateur règle seulement l'incrément X de la catégorie 3.
- ✅ **Plusieurs créateurs** possibles pour un même challenge.
- ✅ Visibilité :
  - **Public** : accès libre, listé sur la page d'accueil de l'app.
  - **Privé** : sur invitation d'un créateur ou d'un participant.
- ✅ Le créateur choisit si les participants peuvent aussi inviter du monde (une personne seulement invitée, qui n'a pas rejoint, ne peut pas inviter).
- ✅ **Fuseau horaire** : début et fin sont définis selon le fuseau du lieu de lancement du challenge (ex. Montréal). Les semaines sont découpées dans ce même fuseau.
- ✅ Après le début, on peut modifier le **titre** et la **description**, mais **pas les catégories**.
- ✅ Personne ne « quitte » vraiment un challenge : l'action « quitter » (ou la suppression de son compte, pour les challenges à venir ou en cours) équivaut à un **abandon**. Les créateurs peuvent **supprimer le challenge**.
- ✅ Un challenge est **supprimable à tout moment** par n'importe quel créateur, même en cours et avec des participants, après une confirmation qui avertit que tout disparaît pour tous.
- ✅ Suppression **logique** immédiate : le challenge disparaît pour tous (`deleted_at`), ses lignes restent en base. La purge définitive suit la politique de conservation des données (❓, §8).

### 2.2 Participation

- ✅ On peut rejoindre un challenge **jusqu'à la fin de sa première semaine**.
- ✅ Un participant est **classé dans toutes les catégories** du challenge (pas d'inscription par catégorie).
- ✅ Abandonner (« quitter ») un challenge = **éliminé de toutes les catégories**, définitivement.
- ✅ Un participant qui abandonne apparaît comme « a abandonné » et sort du podium.
- ✅ Un compte supprimé est traité comme un abandon dans les challenges à venir ou en cours ; un challenge terminé garde ses résultats, sous un nom neutre (voir §6).
- ✅ **Dernier créateur** : la suppression d'un compte est **refusée** (`last_creator`, avec la liste des challenges) tant qu'il est le dernier créateur, ayant encore un compte, d'un challenge à venir ou en cours. La personne désigne d'abord un autre créateur (« ajouter un créateur »), ou, s'il n'y a personne à désigner, supprime le challenge elle-même. Un challenge terminé ne bloque rien. Un co-créateur qui a abandonné garde ses droits de créateur (§5.7) et compte donc comme autre créateur. Les liens d'invitation d'un compte supprimé sont **annulés** : une personne qui avait ouvert le lien sans rejoindre perd l'accès au challenge privé ; celles qui ont déjà rejoint restent participantes.
- ✅ Les activités faites avant l'inscription, mais dans la fenêtre du challenge, comptent.

### 2.3 Catégories et podiums

Un challenge a **3 catégories**. **Chaque catégorie a son propre podium**, donc plusieurs gagnants par challenge. Les catégories sont figées à la création.

| # | Catégorie | Activités acceptées | Classement |
|---|---|---|---|
| 1 | **Temps cumulé** | Toutes les activités, tous sports confondus (course, yoga, escalade, aviron…) | Plus de temps cumulé |
| 2 | **Distance cumulée** | Toutes les activités qui produisent des km, ramenées à une distance de course si le sport est plus rapide | Plus de distance équivalente cumulée |
| 3 | **Course avec élimination** | Course uniquement, en distance | Voir ci-dessous |

**Catégorie 3 : course avec élimination** (la distance doit augmenter chaque semaine) :
- ✅ **X** est un **incrément en km**, choisi par le créateur à la création du challenge (ex. X = 2 km).
- ✅ Règle : la distance courue en semaine s+1 doit être **au minimum** égale à la distance de la semaine s plus X. Exemple avec X = 2 : semaine 1 = 5 km, donc semaine 2 au moins 7 km, puis semaine 3 au moins (semaine 2) + 2 km, et ainsi de suite jusqu'à la fin du challenge.
- ✅ À la fin d'une semaine, si cette condition n'est pas remplie, la personne est éliminée **de cette catégorie seulement** et reste en lice dans les deux autres.
- ✅ Le gagnant est celui qui a le plus de km cumulés.
- ✅ La « distance de la semaine s » est la distance **réellement courue** par la personne en semaine s. **Si elle court plus que le minimum requis, cette distance devient sa nouvelle base de calcul** : avec X = 2, minimum de 7 km en semaine 2 ; si elle court 10 km, le minimum de la semaine 3 est 12 km. Il n'y a donc pas de distance de départ à régler, et personne n'est éliminé en semaine 1 (pas de référence).
- ✅ Il n'y a **pas de délai de grâce** : le minimum de la semaine s+1 est définitif dès la fin de la semaine s.
- ✅ La distance d'une semaine est le cumul de toutes les courses de la semaine (pas une seule sortie).
- ✅ Les personnes qui terminent le challenge sans être éliminées sont classées par km cumulés, **quelle que soit leur base de départ** : celle qui a couru le plus de km gagne.
- ✅ Viennent ensuite les éliminés : le dernier éliminé est classé devant, et en cas d'égalité sur la semaine d'élimination, la distance cumulée départage.
- ✅ La distance cumulée d'un éliminé s'arrête à la **fin de sa semaine d'élimination** (cette semaine comprise) : ses courses suivantes ne comptent plus, son rang ne bouge plus.
- ✅ L'élimination est jugée à l'instant exact de la fin de la semaine (fuseau du challenge), sans délai de grâce.
- ✅ Une semaine terminée est **verrouillée** : aucune activité datée de cette semaine ne peut plus être ajoutée, modifiée ni supprimée (saisie manuelle comprise), puisque l'élimination est immédiate et définitive. C'est l'horloge qui fait foi, pas le passage du worker.
- ✅ Le statut live affiche « objectif atteint » dès que la distance de la semaine atteint le minimum ; sinon « à risque » quand il reste **24 h ou moins** avant la fin de la semaine, et « en cours » avant. Aucun statut en semaine 1 (pas de minimum). Le seuil de 24 h est un paramètre de configuration.

### 2.4 Équivalences de distance (catégorie 2, distance cumulée)

- ✅ Les activités **plus lentes que la course** (marche, randonnée, natation) comptent **sans coefficient** (1 pour 1).
- ✅ Les activités **plus rapides** sont ramenées à une distance de course :

| Activité | Coefficient |
|---|---|
| Course | ×1 |
| Marche, randonnée, natation et autres sports à km plus lents | ×1 |
| Vélo | ×0,3 |
| Aviron | ×0,5 |
| Roller | ×0,5 |
| Ski de fond | ×0,5 |

- ✅ Ce sont des **suggestions** : le catalogue propose un coefficient pour chaque sport à distance, et le **créateur choisit les coefficients de son challenge** à la création (pré-remplis par ces suggestions), entre **0,1 et 2**. La course reste la référence : son coefficient est toujours ×1 et ne s'envoie pas à la création (refusé même à 1).
- ✅ Les coefficients sont **figés à la création du challenge** : ni une modification ultérieure du catalogue ni les créateurs ne les changent pour un challenge en cours.
- ✅ La distance équivalente est **sommée exactement**, puis **arrondie au mètre** (demi-mètre vers le haut) une seule fois, sur le total de chaque participant : deux totaux à moins d'un mètre l'un de l'autre peuvent être ex aequo.
- ✅ Un sport à distance **ajouté au catalogue après la création** d'un challenge n'a pas de coefficient figé dans ce challenge : ses activités comptent pour le temps cumulé, mais pas pour la distance cumulée.
- Conséquence assumée : la marche compte pleinement, donc un marcheur peut battre un coureur en distance pure.

### 2.5 Activités

- ✅ **V1 : saisie manuelle uniquement.** Les synchronisations Strava, Garmin, Polar et Coros arrivent après.
- ✅ **Impossible d'importer une activité après la fin** du challenge.
- ✅ Une activité compte selon sa date de début, dans la fenêtre du challenge ; il n'y a pas de délai de grâce.
- ✅ Pendant la semaine en cours, un participant peut modifier ou supprimer ses propres activités ; une fois la semaine terminée, plus rien ne change (voir §2.3).
- ✅ Toutes les activités ont une **durée** ; la distance n'est demandée que pour les sports qui en produisent (pas le yoga, par exemple).
- ✅ La **vitesse (ou l'allure) n'est jamais saisie** : elle est calculée automatiquement à partir de la durée et de la distance.
- ✅ **Activités cohérentes** (SC-API-084) :
  - deux activités d'un même utilisateur ne se **chevauchent jamais** (`activity_overlap`), chacune occupant l'intervalle [début, début + durée) ; des activités **à la suite** restent permises (un triathlon : natation, puis vélo, puis course, la fin de l'une étant le début de la suivante) ; une activité supprimée ne compte plus ; la modification d'une activité ne revérifie le chevauchement que si son début ou sa durée change ;
  - une activité se saisit une fois **finie** : sa fin (début + durée) n'est jamais dans le futur, avec la même tolérance de 5 minutes que pour le début ;
  - le début n'est jamais antérieur au **1er janvier 2000 à 00:00 UTC** (borne technique contre les erreurs de saisie).
- 🟡 Les activités saisies à la main sont marquées « manuelle » (surtout utile à partir de la V2, quand des activités synchronisées s'ajoutent).
- ❓ Aucun plafond de vitesse en V1 par défaut (seulement des bornes techniques : durée et distance positives, valeurs maximales raisonnables). À revoir si des abus apparaissent.
- ✅ Un **catalogue de sports** définit, pour chaque sport : si la distance est obligatoire, interdite ou facultative (« autre »), et son coefficient suggéré (§2.4).
- ✅ Un utilisateur peut **saisir un sport à la main** s'il n'est pas proposé : il choisit « autre » et écrit un libellé libre (2 à 40 caractères). ✅ Un sport saisi à la main compte toujours pour le **temps** (catégorie 1) ; la distance est **facultative** : s'il y en a une, il compte aussi pour la catégorie 2 (✅ coefficient suggéré ×1, modifiable par le créateur comme les autres), sinon non.
- ✅ **Une activité est saisie une seule fois** et compte dans **tous** les challenges où l'utilisateur est participant actif et dont la fenêtre la contient ; ✅ une activité hors de toute fenêtre est acceptée et comptera si l'utilisateur rejoint un challenge dont la fenêtre la contient (voir `docs/adr/0001-activity-scope.md`). Conséquence : une activité est non modifiable dès que sa semaine est terminée dans **l'un** de ces challenges.

### 2.6 Classement et équité

- ✅ Aucun handicap ni catégorie de niveau : tout le monde est traité à la même enseigne.
- ✅ **V1 : mise à jour du classement au rafraîchissement** de l'écran (pas de temps réel instantané).
- ✅ Égalités : classement **ex aequo** (1, 1, 3), sans départage arbitraire.
- ✅ Résultats **figés** à la fin du challenge, sans délai de grâce.
- ✅ Le classement est toujours recalculé à partir des activités brutes.

### 2.7 Badges

- ✅ **(V1.1, après la V1)** Le créateur d'un challenge peut créer **autant de badges qu'il veut** pour ce challenge. Pour chaque badge : un nom, une description et une **condition**.
- ✅ Dès qu'un participant atteint la condition, il gagne le badge (attribution immédiate, pas seulement en fin de semaine).
- ✅ Mesures possibles : **nombre d'activités**, **distance** et **temps**. La distance d'un badge est la distance **réelle**, sans coefficient ; un badge peut être limité à un sport. Exemples : un badge à 5, à 10 et à 20 activités ; un badge à 100 km cumulés ; un badge à 10 heures cumulées.
- 🟡 Une condition est une **règle structurée** (pas de code libre) : une mesure, une portée (sur tout le challenge, sur une semaine, sur une seule sortie, ou plusieurs semaines de suite), un filtre de sport (tous, course seule ou un sport donné) et un seuil (« au moins »). Les portées et le filtre se compléteront sans changer le modèle, puisque la règle est stockée en JSON.
- ✅ Un badge s'obtient une seule fois par participant. Une fois gagné, il est conservé même si l'activité est ensuite modifiée ou supprimée pendant la semaine en cours.
- ✅ Un badge créé en cours de challenge est évalué aussi sur les activités déjà enregistrées.
- 🟡 Un badge déjà gagné par quelqu'un ne peut plus être supprimé ni modifié.
- ✅ Limite de 50 badges par challenge (chaque activité déclenche l'évaluation des badges du challenge pour son auteur).

### 2.8 Profil et langues

- ✅ Pseudo et avatar. Pseudo **unique sans tenir compte de la casse**, de 3 à 20 caractères (lettres, chiffres, `_`, `.`, `-`).
- ✅ Les lettres du pseudo sont **ASCII** (`a-z`, `A-Z`, sans accent) : l'unicité sans tenir compte de la casse est la même partout, et deux pseudos ne peuvent pas se ressembler à l'œil en étant différents.
- ✅ Le profil est **complet** dès que le pseudo est choisi ; un pseudo ne peut plus être retiré, seulement changé. **Créer ou rejoindre un challenge exige un profil complet** (erreur `profile_incomplete`).
- ✅ En V1, l'avatar affiché est formé des initiales du pseudo ; l'envoi d'une image vient en V1.1.
- ✅ Langues : **français et anglais**.
- ✅ Unité : **km uniquement** pour le moment.

### 2.9 Modération et litiges

- ✅ Pas de système de contestation pour l'instant.
- ✅ Les créateurs peuvent ajouter un créateur et exclure un participant.
- ✅ Un participant exclu par un créateur apparaît comme un abandon (« a abandonné »), sans mention particulière ; il est éliminé de toutes les catégories, définitivement.
- ✅ Seul un participant actif peut devenir créateur (tout créateur est donc participant) ; ajouter quelqu'un qui est déjà créateur ne change rien (même après la fin) ; nommer un nouveau créateur n'est plus possible une fois le challenge terminé.
- ✅ Un créateur ne peut pas être exclu (les créateurs sont à égalité ; un créateur peut toujours abandonner lui-même). Une exclusion n'est plus possible une fois le challenge terminé, et exclure quelqu'un qui a déjà abandonné ne change rien.
- ✅ Pas de système de contestation en V1 : les participants se font confiance.
- ✅ **Pas de plafond de volume par utilisateur** (SC-API-083) : ni sur le nombre de challenges créés, ni sur les invitations, ni sur les activités par jour. La seule protection contre les abus est la **limite de débit** (par adresse IP, et par utilisateur sur les écritures, §5.7), tenue en mémoire par instance. À revoir si des abus apparaissent, notamment des challenges publics en masse sur la page d'accueil.

### 2.10 Modèle économique

- ✅ Gratuit pour le moment.

---

## 3. Stack technique

### 3.1 Backend

- ✅ **Go** avec **Gin**, en **monolithe modulaire** (pas de microservices au départ).
- ✅ API REST avec **OpenAPI** pour générer les clients TypeScript.
- ✅ **PostgreSQL** (driver `pgx`).
- ✅ **GORM** pour le CRUD ; SQL brut (ou `sqlc`) pour les classements et agrégations.
- ✅ Migrations SQL versionnées avec **golang-migrate** (fichiers `.up.sql` et `.down.sql`). Le schéma vient uniquement des migrations : jamais `AutoMigrate`, ni en développement ni en production.
- ✅ **Pas de Redis en V1** : le classement se calcule depuis PostgreSQL, puisque la mise à jour se fait au rafraîchissement.
- ✅ Un worker (même image, autre commande) pour les tâches planifiées : clôture de semaine, éliminations, badges, gel des résultats.

### 3.2 Authentification

- ✅ Service tiers pour les comptes et la connexion (OIDC). V1 : e-mail sans mot de passe par **code à usage unique**, seule méthode de connexion. Connexions sociales (Google, Apple) reportées à plus tard.
- ✅ **Auth0**, avec **un seul tenant** (`sport-challenge-dev.ca.auth0.com`), partagé par le développement local et la production, en région **Canada** (vérifiée disponible avec le forfait choisi). Décision du propriétaire du produit du 2026-11-19 (SC-API-115), qui remplace « un tenant par environnement » ; conséquences et procédure : `docs/auth0-setup.md`.
- ✅ Connexion par e-mail : **code à usage unique** (passwordless, mode code), et non lien magique : le New Universal Login d'Auth0 ne propose pas le lien magique.
- Le backend reste indépendant du fournisseur (interface `TokenVerifier`, identifiant d'utilisateur local, validation JWT par JWKS) : un changement ultérieur ne toucherait pas les modules métier.
- La région du tenant ne se change pas après sa création.
- Quand des connexions sociales seront ajoutées : Sign in with Apple obligatoire sur iOS si une autre connexion sociale est proposée.
- Le backend valide le JWT et garde son propre `user_id` lié à l'identifiant du fournisseur.

### 3.3 Stockage d'images

- Avatars sur **Cloud Storage**, avec URL signées : le mobile et le web envoient le fichier directement au bucket.
- Validation du type et de la taille côté serveur.

---

## 4. Hébergement et infrastructure

- ✅ **Google Cloud Platform**, région **Montréal** (`northamerica-northeast1`), car les premiers utilisateurs sont au Canada.
- Services prévus :

| Besoin | Service GCP |
|---|---|
| API et worker | **Cloud Run** (service et jobs) |
| Tâches planifiées | **Cloud Scheduler** |
| Base de données | **Cloud SQL (PostgreSQL)** avec sauvegardes |
| Images | **Cloud Storage** (CDN plus tard) |
| Secrets | **Secret Manager** |
| Images Docker | **Artifact Registry** |
| Logs, erreurs, alertes | **Cloud Logging, Error Reporting, Monitoring** |

- ✅ **Docker** multi-stage (binaire statique, image minimale).
- ✅ Pas de Kubernetes : Cloud Run suffit.
- ✅ **Environnements** : la production seule (projet `sport-challenge-prod`) ; le développement se fait en local. Un staging pourra être recréé plus tard, dans un projet GCP séparé.
- 🟡 **Horloge réglable** (`CLOCK_OFFSET`, en local seulement) pour tester les clôtures de semaine en quelques minutes.
- 🟡 **CI/CD** : GitHub Actions (tests, build, push, migrations, déploiement Cloud Run), avec authentification sans clé (Workload Identity Federation).
- ✅ **Infrastructure configurée à la main** dans la console GCP, décrite dans `docs/deploiement.md` (pas de Terraform).
- ⚠️ Mettre une **alerte de budget** dès le premier jour (Cloud SQL a un coût fixe).

### Observabilité et analytics

- ✅ Logs, suivi des erreurs (ex. Sentry) et alertes : prévus.
- ✅ Analytics produit prévus plus tard, avec consentement et un outil compatible avec les lois de protection des données.

---

## 5. Architecture backend

### 5.1 Vue d'ensemble

```mermaid
flowchart LR
  subgraph Clients
    W[Web React]
    M[Mobile Expo]
  end
  AUTH[Auth0]
  subgraph GCP["Google Cloud — Montréal"]
    API[Cloud Run : API Go/Gin]
    JOB[Cloud Run Job : worker]
    SCH[Cloud Scheduler]
    DB[(Cloud SQL PostgreSQL)]
    GCS[(Cloud Storage : avatars)]
    OBS[Logging / Error Reporting / Monitoring]
  end
  W & M -->|HTTPS REST| API
  W & M -->|connexion OIDC| AUTH
  API -.->|validation JWT via JWKS| AUTH
  API --> DB
  API -->|URL signées| GCS
  W & M -->|upload direct| GCS
  SCH -->|déclenche| JOB
  JOB --> DB
  API & JOB --> OBS
  EXT[Strava / Polar / Garmin / Coros] -.->|V2+ webhooks| API
  API -.->|V3 push| PUSH[FCM / APNs]
```

- ✅ **Monolithe modulaire** : un seul dépôt et une seule image Docker, avec trois commandes : `api` (requêtes HTTP), `worker` (tâches planifiées) et `migrate` (migrations, lancées par un job avant chaque déploiement).
- ✅ Le worker est lancé par Cloud Scheduler, sans serveur permanent.
- 🟡 Chaque module a ses couches : **handler** (HTTP) → **service** (règles métier) → **repository** (base de données).

### 5.2 Structure détaillée du dépôt backend

> Cette arborescence décrit le **dépôt actuel** (V1). Les entrées marquées *(cible, V1.1)* ou *(cible, V2+)* n'existent pas encore : chaque dossier et chaque fichier sont créés par le premier ticket qui les utilise (pas de squelette vide ni de code écrit d'avance), voir `TASKS.md`. Les tests (`*_test.go`, unitaires et `*_integration_test.go`) sont à côté du code et ne sont pas listés.

```
sport-challenge-api/
├── cmd/
│   ├── api/
│   │   └── main.go                  assemble config, base, services, routes et lance le serveur HTTP
│   ├── worker/
│   │   └── main.go                  tâches : close-weeks, finalize-challenges, retry-auth0-deletions (V2 : sync, refresh-tokens) ;
│   │                                appelle directement closure.Service et user.Service
│   └── migrate/
│       └── main.go                  applique les migrations embarquées (up, down, force) : job lancé avant chaque déploiement
├── internal/
│   ├── platform/                    socle technique, importé par tous les modules
│   │   ├── config/                  lecture et validation des variables d'environnement
│   │   ├── db/                      connexion PostgreSQL (pgx, GORM), transactions (WithTx, InTx) ;
│   │   │   └── testdb/              une base PostgreSQL par test d'intégration, construite par les migrations
│   │   ├── httpx/                   routeur Gin et middlewares : en-têtes de sécurité, identifiant de requête, logs,
│   │   │                            recovery, CORS, limitation de débit, validation des requêtes contre le contrat ;
│   │   │                            curseurs de pagination ; conversion des erreurs en réponses JSON
│   │   ├── auth/                    validation des JWT (JWKS Auth0), middlewares Authenticate et CurrentUser,
│   │   │                            client de l'API Management d'Auth0 (suppression de compte)
│   │   ├── clock/                   interface Clock : horloge réelle, horloge décalable (tests, `CLOCK_OFFSET`)
│   │   ├── apperr/                  erreurs métier avec code stable (traduit côté client)
│   │   ├── textx/                   règles des textes libres : caractères cachés refusés, longueur visible
│   │   ├── logging/                 logger slog structuré commun à l'API et au worker
│   │   ├── buildinfo/               version, commit et date du build (fixés à l'édition des liens)
│   │   └── storage/                 (cible, V1.1) stockage Cloud Storage, URL signées des avatars
│   │
│   ├── api/
│   │   ├── api.gen.go               généré par oapi-codegen depuis api/openapi.yaml (make generate) : routes Gin,
│   │   │                            types de requête et de réponse, interface StrictServerInterface ; jamais modifié à la main
│   │   └── apitest/                 helpers de test : valide une réponse réelle contre le contrat (kin-openapi),
│   │                                implémentation vide des opérations des autres modules
│   │
│   ├── health/
│   │   └── handler.go               GET /health (vivant, version du build), GET /ready (la base répond)
│   │
│   ├── user/
│   │   ├── model.go                 entité User
│   │   ├── profile.go               règles du profil : pseudo, langue
│   │   ├── service.go               création au premier login, profil, suppression (anonymisation), reprise
│   │   │                            des suppressions Auth0 échouées
│   │   ├── repository.go            accès base (GORM)
│   │   └── handler.go               routes /me
│   │
│   ├── challenge/
│   │   ├── model.go                 Challenge, Category, Participant, CreatorLink
│   │   ├── rules.go                 validation pure : fuseau, dates, paramètres de catégories
│   │   ├── status.go                statut (à venir, en cours, terminé) déduit des dates
│   │   ├── policy.go                qui peut faire quoi (fonctions pures, testées sans base)
│   │   ├── service.go               création, modification limitée, rejoindre, abandonner, exclure, supprimer
│   │   ├── repository.go
│   │   └── handler.go
│   │
│   ├── invitation/
│   │   └── model.go · service.go · repository.go · handler.go
│   │                                liens d'invitation, règle « les participants peuvent inviter »
│   │
│   ├── sport/                       le catalogue lui-même est dans la migration 000003 (données de référence)
│   │   ├── model.go                 Sport : code, mode de distance, coefficient suggéré
│   │   ├── repository.go            lecture de la table sports
│   │   ├── service.go               lecture du catalogue, coefficients suggérés, mode de distance d'un sport
│   │   └── handler.go               GET /sports
│   │
│   ├── activity/
│   │   ├── model.go                 Activity (source, identifiant externe, sport, durée, distance)
│   │   ├── rules.go                 validation pure : bornes techniques (durée et distance positives, valeurs
│   │   │                            maximales raisonnables), distance selon le sport ; vitesse calculée
│   │   ├── policy.go                qui peut saisir, modifier, supprimer (auteur, semaine terminée verrouillée)
│   │   ├── service.go               point d'entrée unique : validation, fenêtre du challenge, idempotence
│   │   ├── repository.go
│   │   └── handler.go
│   │
│   ├── scoring/                     Go pur, sans dépendance : testé sans base de données
│   │   ├── types.go                 Activity, Window, Entry, interface Scorer
│   │   ├── window.go                semaines découpées dans le fuseau du challenge
│   │   ├── distance.go              catégorie 2 : distance cumulée (coefficients)
│   │   ├── duration.go              catégorie durée
│   │   ├── elimination.go           catégorie à élimination
│   │   └── ranks.go                 classement ex aequo (1, 1, 3)
│   │
│   ├── leaderboard/
│   │   ├── model.go                 challenge et catégories lus, classement par catégorie
│   │   ├── repository.go            participants et activités de la fenêtre (SQL brut, lecture seule)
│   │   ├── service.go               charge participants et activités, appelle les scorers
│   │   └── handler.go               GET /challenges/{id}/leaderboard
│   │
│   ├── closure/                     sans handler : appelé par le worker
│   │   ├── model.go                 challenges à clore, résultats, instantanés
│   │   ├── repository.go            clôtures de semaine, éliminations, result_snapshot (SQL brut)
│   │   └── service.go               clôture de semaine, éliminations, gel des résultats
│   │
│   ├── badge/                       (cible, V1.1)
│   │   ├── model.go                 BadgeDefinition (par challenge, règle en JSON), BadgeAward
│   │   ├── rules.go                 évaluation pure d'une condition de badge (sans base de données) ; testée
│   │   └── service.go · repository.go · handler.go
│   │
│   ├── integration/                 (cible, V2+)
│   │   ├── provider.go              interface Provider commune
│   │   ├── connection/              connexions OAuth, jetons chiffrés, renouvellement
│   │   ├── webhook/                 route publique, vérification de signature
│   │   ├── dedupe/                  règle de fusion des doublons entre marques
│   │   └── strava/ · polar/ · garmin/ · coros/     un adaptateur par marque
│   │
│   └── notification/                (cible, V3) FCM et APNs
│
├── migrations/                      000001_init_utils.up.sql / .down.sql, 000002_create_users… (golang-migrate) ;
│                                    migrations.go les embarque dans le binaire migrate
├── api/
│   ├── openapi.yaml                 spécification de l'API : source du code serveur généré et du client TypeScript
│   └── oapi-codegen.yaml            configuration de la génération du code serveur (internal/api)
├── deploy/
│   ├── Dockerfile                   build multi-stage, image minimale (api, worker, migrate)
│   └── docker-compose.yml           local : PostgreSQL (émulateur de stockage en V1.1)
├── docs/                            déploiement, exploitation, migrations, publication, Auth0, ADR
├── .github/workflows/               ci.yml, release.yml, deploy.yml, claude-review.yml
├── Makefile                         raccourcis : run, worker, test, lint, fmt, migrate, generate, docker (make help)
├── go.mod
└── README.md
```

**Règles de dépendance**

```mermaid
flowchart TD
  H[handler] --> S[service]
  S --> R[repository]
  S --> SC[scoring]
  LB[leaderboard] --> CH[challenge]
  LB --> AC[activity]
  LB --> SC
  CL[closure] --> LB
  CL -.->|V1.1| BG[badge]
  PL[platform] -.-> H & S & R
```

- Un `handler` ne contient aucune règle métier : il lit la requête, appelle le `service`, renvoie la réponse.
- Seul le `repository` parle à la base. Un module n'importe jamais le `repository` d'un autre module.
- Les modules se parlent par de petites **interfaces définies côté consommateur** (ex. `leaderboard` déclare ce dont il a besoin de `activity`).
- `scoring` n'importe rien d'autre que la bibliothèque standard.
- `platform` peut être importé par tous ; il n'importe aucun module métier.
- Les tests unitaires sont à côté du code ; les tests d'intégration utilisent PostgreSQL en conteneur.

**Routes par module**

| Module | Routes (`/api/v1`) |
|---|---|
| `user` | `GET /me`, `PATCH /me`, `DELETE /me` |
| `challenge` | `POST /challenges`, `GET /challenges` (publics et les miens), `GET /challenges/{id}`, `PATCH /challenges/{id}` (titre, description), `POST /challenges/{id}/join`, `POST /challenges/{id}/abandon`, `GET /challenges/{id}/participants`, `DELETE /challenges/{id}/participants/{userId}` (exclure), `POST /challenges/{id}/creators`, `DELETE /challenges/{id}` (créateurs : supprimer le challenge) |
| `invitation` | `POST /challenges/{id}/invitations`, `POST /invitations/open`, `POST /invitations/accept` (le jeton dans le corps, jamais dans l'URL) |
| `leaderboard` | `GET /challenges/{id}/leaderboard` (les 3 catégories ensemble, sans filtre ni pagination) |
| `activity` | `POST /activities`, `GET /me/activities`, `GET /challenges/{id}/participants/{userId}/activities` (participants du challenge), `PATCH` et `DELETE /activities/{id}` (auteur, tant que la semaine n'est pas terminée dans aucun challenge où l'activité compte) |
| `sport` | `GET /sports` |
| `badge` (V1.1) | `GET /me/badges`, `GET /challenges/{id}/badges`, `POST /challenges/{id}/badges` (créateur), `PATCH` et `DELETE /challenges/{id}/badges/{badgeId}` (créateur, tant que personne ne l'a gagné) |
| uploads (V1.1) | `POST /uploads/avatar` (URL signée) |
| `integration` (V2) | `GET /integrations`, `POST /integrations/{provider}/connect`, `DELETE /integrations/{provider}`, `POST /webhooks/{provider}` (public) |
| `health` | `GET /health`, `GET /ready` (à la racine, hors `/api/v1`) |

**Tâches du worker**

- `close-weeks` : clôt les semaines terminées (fin de semaine atteinte), enregistre les éliminations, évalue les badges de type « plusieurs semaines de suite ».
- `retry-auth0-deletions` : rejoue la suppression chez Auth0 des utilisateurs dont l'appel a échoué.
- `finalize-challenges` : fige les résultats des challenges terminés (statut « terminé », classements enregistrés) et clôt leur **dernière semaine**, laissée par `close-weeks` : sa clôture et ses éliminations sont enregistrées dans la même transaction que les classements figés.
- 🟡 Cloud Scheduler les lance **toutes les heures**, car les semaines se terminent à des heures différentes selon le fuseau de chaque challenge. Chaque tâche est **idempotente** : la relancer ne change rien si le travail est déjà fait.

### 5.3 Responsabilités des modules

| Module | Responsabilité |
|---|---|
| `user` | Crée la ligne locale au premier login, gère pseudo, avatar, langue |
| `challenge` | Création, modification limitée (titre, description), fuseau, durée, visibilité, règle d'invitation, co-créateurs |
| `invitation` | Liens et invitations pour les challenges privés, selon la règle « les participants peuvent inviter » |
| `sport` | Catalogue des sports et coefficients de distance, figés par challenge à sa création |
| `activity` | Saisie manuelle, validation (durée obligatoire, distance selon le sport, vitesse calculée et jamais saisie), marquage « manuelle » ; une activité hors de toute fenêtre est enregistrée et ne compte nulle part (ADR 0001) |
| `scoring` | Calcule un classement à partir d'activités et de règles, sans dépendance à la base ni à Gin |
| `leaderboard` | Charge participants et activités, appelle les scorers, renvoie un classement par catégorie |
| `closure` | À chaque fin de semaine : éliminations, badges « semaines de suite » ; à la fin du challenge : gel des résultats |
| `badge` | Badges créés par les créateurs d'un challenge (nom, description, condition structurée) ; évaluation à chaque nouvelle activité, attribution idempotente |
| `integration` | (V2+) Connexion OAuth au fournisseur, réception des webhooks, import, dédoublonnage |

### 5.4 Modèle de données (entités principales)

| Entité | Champs clés |
|---|---|
| `users` | id, identifiant du fournisseur d'auth, pseudo, avatar, langue |
| `challenges` | id, titre, description, visibilité, participants peuvent inviter (`members_can_invite`), fuseau, début et fin (UTC), créé par, suppression logique (`deleted_at`) ; le statut (à venir, en cours, terminé) se déduit des dates |
| `challenge_creators` | challenge, utilisateur |
| `categories` | challenge, type (temps cumulé, distance cumulée, course avec élimination), position, paramètres figés (`params`) ; le nom est traduit par le frontend |
| `participants` | challenge, utilisateur, statut (actif ou abandonné), date d'inscription, date d'abandon |
| `invitation` | challenge, invité par, empreinte du jeton (jamais le jeton), expiration (fin de la semaine 1), date de révocation (`revoked_at`, prévue par le schéma : aucune route ne révoque une invitation pour l'instant) ; les personnes qui ont ouvert le lien (`invitation_recipients`) |
| `sports` | code, mode de distance (obligatoire, interdite, facultative), coefficient suggéré (libellés traduits par le frontend) |
| `activity` | utilisateur, sport, libellé du sport (si « autre »), date de début (UTC), durée, distance, source (manuelle ou fournisseur), identifiant externe, clé d'idempotence |
| `week_closure` | challenge, numéro de semaine, date de clôture |
| `elimination` | participant, catégorie, semaine d'élimination, distance courue et minimum exigé cette semaine-là (trace du jugement, jamais lue pour classer) |
| `badge_definition` | challenge, nom, description, règle (JSON structuré), créateur, date de création |
| `badge_award` | badge, utilisateur, date d'obtention, activité déclencheuse (si applicable) ; unique (badge, utilisateur) |
| `result_snapshot` | challenge, catégorie, classement figé en fin de challenge |

Index à prévoir : `activity (utilisateur, date de début)`, `participant (challenge)`, `category (challenge)`.

### 5.5 Flux principaux

1. **Création d'un challenge** : validation (fuseau, dates, catégories) → challenge, catégories et premier créateur enregistrés → le créateur devient participant.
2. **Saisie d'une activité** (point d'entrée unique) : validation (aucune semaine terminée dans les challenges où l'activité compte : une semaine terminée est verrouillée, ADR 0001) → stockage avec source « manuelle » → aucun recalcul stocké.
3. **Lecture du classement** (au rafraîchissement) : chargement des participants et, en une requête, des **totaux** des activités de la fenêtre (par participant et par semaine pour le sport de la catégorie à élimination, par participant et par sport pour les autres) → un scorer par catégorie → classement ex aequo renvoyé au client. Le calcul se fait toujours depuis les activités brutes, sommées par la base, jamais depuis des totaux stockés : les scorers ne faisant que des sommes, le résultat est identique (un test d'intégration le vérifie). ✅ Ni plafond de participants ni pagination du classement pour l'instant (SC-API-082). Mesure (benchmark `BenchmarkActivities5000`, PostgreSQL local) pour 5 000 participants, 12 semaines, 5 activités par semaine (300 000 activités) : 70 000 lignes lues au lieu de 300 000, ~125 ms au lieu de ~945 ms et 45 Mo alloués au lieu de 219 Mo par lecture.
4. **Clôture hebdomadaire** : Cloud Scheduler lance le worker → pour chaque challenge actif dont une semaine est terminée → éliminations (catégorie à élimination) enregistrées → badges « semaines de suite » évalués.
5. **Fin de challenge** : à l'instant de fin → statut « terminé », `result_snapshot` enregistré, avec la clôture (`week_closure`) et les éliminations (`elimination`) de la dernière semaine, comme pour les autres semaines. Plus aucune activité datée de sa fenêtre ne peut être ajoutée (sa dernière semaine est terminée, donc verrouillée).
6. **Abandon d'un participant** : statut « abandonné » (volontairement, ou après l'exclusion par un créateur), éliminé de toutes les catégories, apparaît comme « a abandonné ».
7. **Badges** : après l'enregistrement d'une activité, évaluation des badges du challenge pour son auteur ; attribution immédiate et idempotente (unique par badge et utilisateur). Les conditions « plusieurs semaines de suite » sont évaluées à la fin de chaque semaine par le worker.

### 5.6 API

- ✅ REST sous `/api/v1`, spécification OpenAPI (génération du client TypeScript).
- Ressources principales : `me`, `challenges` (le détail d'un challenge contient ses catégories), `challenges/{id}/participants`, `challenges/{id}/leaderboard`, `challenges/{id}/invitations`, `invitations`, `activities`, `sports` ; en V1.1 : `badges`, `uploads` (URL signées).
- 🟡 Les erreurs renvoient un **code stable** (traduit côté client en français ou en anglais), pas un texte.
- 🟡 Les créations d'activité acceptent une **clé d'idempotence** (utile pour le hors-ligne et les futures synchronisations).
- 🟡 Pagination par curseur sur les listes.

### 5.7 Authentification et autorisation

**Chaîne de middlewares Gin (dans cet ordre)**

| # | Middleware | Rôle |
|---|---|---|
| 1 | `SecurityHeaders` | En-têtes posés sur toute réponse, erreurs comprises : `X-Content-Type-Options: nosniff`, `Cache-Control: no-store` (sauf si le handler en fixe un autre) |
| 2 | `RequestID` | Identifiant de requête, repris dans les logs et les réponses d'erreur |
| 3 | `Logger` | Logs structurés (`slog`), sans donnée personnelle ni jeton |
| 4 | `Recovery` | Transforme un plantage en erreur 500 propre, sans arrêter le serveur ; placé après `RequestID` et `Logger` pour que le plantage rattrapé soit journalisé comme un 500 avec son identifiant de requête |
| 5 | `CORS` | Origines explicites (web uniquement, `CORS_ALLOWED_ORIGINS`), jamais `*` ; répond aux pré-requêtes `OPTIONS` sans jeton ; pas de cookie, donc pas d'`Allow-Credentials` |
| 6 | `RateLimitIP` | Limite de débit par adresse IP, avant tout travail coûteux ; répond 429 au-delà |
| 7 | `Authenticate` | Lit le jeton `Bearer`, le vérifie (signature par JWKS, émetteur, audience, expiration), place les informations du jeton dans le contexte |
| 8 | `CurrentUser` | Retrouve l'utilisateur local à partir de l'identifiant du fournisseur (`sub`), le crée au premier login, place son identifiant dans le contexte |
| 9 | `RateLimitUser` | Limite de débit par utilisateur, sur les requêtes d'écriture seulement (toute méthode sauf `GET`, `HEAD` et `OPTIONS`) |
| 10 | `RequestValidator` | Par route du contrat, juste avant le handler : vérifie la requête contre `api/openapi.yaml` (400 `validation_failed`) |

**Groupes de routes**
- Publiques, sans jeton : `GET /health` et `GET /ready` (sondes de Cloud Run), et les pré-requêtes `OPTIONS` (CORS).
- Protégées (tout le reste, dont `/api/v1`) : middlewares 1 à 10 ; les routes publiques passent par tous sauf 7 à 9. **Protégé par défaut** : `cmd/api` applique `Authenticate` puis `CurrentUser` à toute route absente d'une liste explicite de routes publiques, et `api/openapi.yaml` exige `bearerAuth` au niveau global (seules les sondes le surchargent avec `security: []`). Une nouvelle route est donc protégée sans rien ajouter ; un test parcourt les routes enregistrées pour le vérifier.
- ✅ Connexion **obligatoire** pour toutes les routes, y compris la lecture des challenges publics (V1). Pas de middleware `OptionalAuth` pour l'instant.
- V2 : `POST /webhooks/{provider}` n'utilise pas le jeton utilisateur mais un middleware de **vérification de signature** du fournisseur.

**Indépendance du fournisseur**
- `platform/auth` définit une interface `TokenVerifier`. Auth0 émet des jetons JWT vérifiables par clés publiques (JWKS) : l'adresse des clés, l'émetteur et l'audience viennent de la configuration (`AUTH0_DOMAIN`, `AUTH0_AUDIENCE`). Un autre fournisseur JWT s'ajouterait sans toucher au reste.
- Le reste de l'application ne connaît que l'identifiant de l'utilisateur local : changer de fournisseur ne touche ni les modules métier ni la base (hors la correspondance `sub` → utilisateur).

**Autorisation** (pas dans un middleware)
- Chaque service vérifie le rôle : créateur, participant ou public. Un utilisateur ne peut jamais agir sur une ressource d'un autre en changeant un identifiant.
- ✅ Un challenge privé n'est lisible que par ses créateurs, participants et invités ; un participant qui a abandonné (ou a été exclu) le voit encore, avec le détail des activités, sans pouvoir agir. Un **créateur** qui abandonne garde tous ses droits de créateur (modifier, inviter, exclure, ajouter un créateur, supprimer), mais ne concourt plus : ses activités n'y comptent plus. Une ressource privée inconnue renvoie 404 plutôt que 403 pour ne pas révéler son existence.

**Comment l'autorisation est vérifiée** : chaque cas d'usage du service reçoit explicitement **qui agit** (`actorID`), charge la ressource, puis appelle une fonction de politique pure (`policy.go`) avant toute écriture. Le handler ne peut pas contourner le service, et le repository n'est jamais appelé directement par un handler. Les listes filtrent dans la requête SQL (jamais « tout charger puis retirer »).

**Matrice des droits**

| Action | Qui peut |
|---|---|
| Voir un challenge public et son classement | tout utilisateur connecté (✅) |
| Voir un challenge privé et son classement | participants (y compris ceux qui ont abandonné), invités et créateurs (✅). Une invitation est un **lien réutilisable** jusqu'à la fin de la semaine 1 : toute personne connectée qui l'ouvre devient invitée, tant que l'invitation reste valide ; connaître le jeton sans l'avoir ouvert ne donne rien (✅). Le jeton voyage dans le **corps** de `POST /invitations/open` et `POST /invitations/accept`, jamais dans l'URL : les journaux de requêtes de la plateforme enregistrent les URL complètes, et ouvrir un lien est une écriture, qui ne doit pas se faire par un `GET` (un préchargement suffirait) (✅, SC-API-086) |
| Rejoindre un challenge public | tout utilisateur connecté, jusqu'à la fin de la semaine 1 (✅) |
| Rejoindre un challenge privé | sur invitation valide, jusqu'à la fin de la semaine 1 (✅) ; une invitation n'est valide que tant que son auteur est encore créateur ou participant actif, et que son compte n'est pas supprimé. Exclure un participant (ou qu'il abandonne) n'exclut pas les personnes qu'il a invitées : celles qui ont déjà rejoint restent participantes ; seul son lien est annulé, pour les personnes invitées qui n'ont pas encore rejoint (✅) |
| Inviter | créateurs ; participants seulement si le créateur l'a autorisé à la création (✅) |
| Modifier titre et description | créateurs (✅) |
| Modifier les catégories | personne, après la création (✅) |
| Ajouter un créateur | créateurs (✅) |
| Exclure un participant | créateurs (✅) |
| Créer un badge | créateurs (✅) |
| Modifier ou supprimer un badge | créateurs, tant que personne ne l'a gagné (🟡) |
| Saisir une activité | l'utilisateur, pour lui-même, hors des semaines terminées des challenges où elle compte (✅, ADR 0001) |
| Modifier ou supprimer une activité | son auteur, tant que sa semaine n'est terminée dans aucun challenge où elle compte (✅, ADR 0001) |
| Abandonner (« quitter ») un challenge | le participant, pour lui-même ; équivaut à un abandon définitif (✅) |
| Supprimer le challenge | créateurs, à tout moment (✅) |
| Voir le détail des activités (date, sport, durée, distance) d'un participant | les participants du même challenge (y compris ceux qui ont abandonné) et les créateurs (✅) ; un non-participant d'un challenge public ne voit que le classement (✅). Le participant visé peut avoir abandonné ou été exclu : ses activités de la fenêtre du challenge restent visibles jusqu'à la fin, **y compris celles saisies après son départ**, qui n'y comptent plus (✅) |

**Codes de réponse** :
- ressource inexistante ou privée inaccessible → 404 (`challenge_not_found`…) ;
- ressource visible mais action interdite → 403 `forbidden` ;
- exception : le détail des activités d'un participant, demandé par quelqu'un qui voit le challenge sans y participer (non-participant d'un challenge public, invité), répond 404 `not_found` : pour lui, cette liste n'existe pas (✅) ;
- entrée invalide (format, bornes, dates, champ inconnu) → 400 `validation_failed`, avec les champs fautifs dans `params.fields` ;
- entrée valide mais action contraire à une règle métier → 422 avec un code stable (`profile_incomplete`, `registration_closed`, `challenge_ended`, `field_not_editable`, `cannot_exclude_creator`…) ;
- conflit avec une autre donnée (pseudo déjà pris) → 409.

**Protections** : l'identifiant de l'acteur vient toujours du contexte (jamais d'un paramètre) ; le contrôle et l'écriture se font dans la même transaction quand une course est possible (rejoindre à la limite de la semaine 1, suppression du challenge pendant qu'un participant le rejoint) ; chaque règle a un test « interdit » ; l'agent `security-reviewer` vérifie ce point à chaque revue.

### 5.8 Éléments transverses

- ✅ **Horloge injectable** : toute règle qui dépend du temps passe par une horloge réglable, pour tester les clôtures sans attendre (horloge simulée dans les tests, `CLOCK_OFFSET` en local).
- Configuration par variables d'environnement, secrets dans Secret Manager.
- Logs structurés en JSON (lus par Cloud Logging), avec un identifiant de requête.
- Limitation de débit sur les routes d'écriture et d'upload.
- **Erreurs** (✅) : `platform/apperr` est écrit pour ce projet. Le package maison de Faunadex (`apierrors` et `translator`) n'est **pas repris** : il renvoie un code numérique et un message traduit par le serveur, sans paramètres ni cause chaînée. Contrat : un **code stable** (`snake_case`, en anglais) ; un statut HTTP associé ; des **paramètres** optionnels pour les textes avec valeur (par exemple la date limite) ; une **cause** chaînée (`errors.Is` et `errors.As` fonctionnent), jamais exposée au client ; aucune donnée personnelle dans les paramètres. Réponse unique : `{ "error": { "code", "message", "params", "request_id" } }`, construite à **un seul endroit** (`platform/httpx`). Les textes d'erreur affichés dans l'app viennent des fichiers de traduction du frontend, indexés par le code ; le `message` du serveur n'est qu'un texte de secours pour les autres clients. La traduction côté serveur sert aux textes **émis par le serveur** (courriels, notifications push). Les codes sont listés dans `openapi.yaml`.
- 🟡 **Tests** : unitaires sur `scoring` et les validations (sans base), d'intégration avec PostgreSQL en conteneur dans la CI ; tests de bout en bout à prévoir si un staging est recréé.

### 5.9 Intégrations sportives (V2+)

- Une interface **Provider** commune à Strava, Polar, Garmin et Coros : connexion OAuth, réception des webhooks, récupération d'une activité, conversion du sport vers le catalogue.
- Table des connexions avec jetons **chiffrés**, gestion du renouvellement et de la révocation.
- Route de webhook publique, vérifiée par signature ou jeton du fournisseur.
- **Dédoublonnage** : une même sortie peut arriver par deux marques ; règle de fusion basée sur l'heure de début, la durée et la distance.
- Mise à jour et suppression côté fournisseur répercutées tant que la semaine concernée n'est pas terminée ; une activité reçue après la fin de sa semaine ne compte plus (❓ à revisiter, voir §8).

### 5.10 Ce que la V1 prépare pour la V2

La V2 (synchronisations) a peu d'impact sur la V1 si ces choix, presque gratuits, sont faits dès le départ :
- 🟡 `activity` porte déjà une `source` (« manuelle » en V1) et un `external_id` nul en V1, avec une contrainte d'unicité (`source`, `external_id`) quand il est renseigné.
- 🟡 `activity` a des dates de création, de modification et de suppression, car un fournisseur peut modifier ou supprimer une activité.
- 🟡 **Point d'entrée unique** pour créer une activité : la saisie manuelle et, plus tard, les imports passent par le même service, où s'appliquent le verrouillage des semaines, l'évaluation des badges et les contrôles de validité.
- Tout le reste (connexions OAuth, webhooks, correspondance des sports par marque, déduplication, jetons chiffrés) s'ajoute en V2 par des migrations additives, sans toucher aux tables de la V1.
- Les règles propres aux synchronisations (activités tardives, agrégateur ou connexions directes) se décident en V2 et n'affectent pas les règles de la V1.

---

## 6. Données, légalité et conformité

> Je ne suis pas juriste : ces points sont à faire valider avant le lancement public.

- **Loi 25 (Québec)** applicable aux utilisateurs québécois : politique de confidentialité claire en français et en anglais, consentement explicite, personne responsable de la protection des renseignements, registre des incidents, évaluation avant tout transfert hors Québec.
- **LCAP (Canada)** pour les courriels promotionnels.
- **RGPD** si des utilisateurs européens s'ajoutent.
- ✅ **Suppression de compte** : anonymisation, traitée comme un abandon dans tous les challenges à venir ou en cours (un challenge terminé garde ses résultats, sous un nom neutre), refusée tant que le compte est le dernier créateur d'un challenge à venir ou en cours (§2.2) ; l'utilisateur est aussi **supprimé chez Auth0** (API de gestion, avec reprise en cas d'échec) pour que l'effacement soit réel.
- ✅ Les activités détaillées d'un participant sont visibles par les autres participants du challenge : l'app l'indique **avant** de rejoindre, et la politique de confidentialité le mentionne. Quitter un challenge (ou en être exclu) ne les cache pas : les activités datées de sa fenêtre restent visibles jusqu'à sa fin, même celles d'après le départ.
- 🟡 Ne stocker que les **résumés d'activité** (distance, durée, type, date), pas les traces GPS.
- Tenir un **inventaire des sous-traitants** (Auth, notifications, analytics, fournisseurs sportifs) avec la région de chacun.
- ✅ **Cible** : héberger à Montréal (`northamerica-northeast1`) les données de production : base, sauvegardes, journaux et secrets. Elle n'est pas encore atteinte partout : l'état réel, avec ses exceptions datées (journaux d'audit de GCP, Error Reporting, sauvegardes et journaux antérieurs en attente d'expiration, emplacement non vérifié des journaux de transactions), est dans `docs/deploiement.md`.
- Respecter les conditions d'usage de chaque API sportive (affichage, marque, quotas).

---

## 7. Feuille de route

### V1, phase par phase (même ordre que le skill `bootstrap`)

Le détail ticket par ticket (69 tickets, critères d'acceptation, commits proposés) est dans [`TASKS.md`](TASKS.md) (importé en issues GitHub, voir « Les tickets vivent dans GitHub »).

| Phase | Contenu | Points à ne pas oublier |
|---|---|---|
| 0 | Fondations minimales et CI | `go.mod` ; `Makefile` limité au nécessaire, avec garde-fou : les migrations ne s'appliquent que sur la base locale ; `docker-compose` (PostgreSQL) ; lint ; CI GitHub Actions ; relecture des pull requests par Claude |
| 1 | Serveur minimal | `config` réduite aux variables utilisées, `slog`, middlewares `Recovery`, `RequestID`, `Logger`, `GET /health` et `/ready`. Le reste du socle (**`apperr`**, §5.8, `clock` injectable, transactions, pagination, `CORS`) arrive avec la première fonctionnalité qui l'utilise |
| 2 | Utilisateur et **Auth0** | Middlewares `Authenticate`, `CORS`, `CurrentUser` (la limitation de débit par IP et par utilisateur vient en phase 9, avant l'exposition publique) ; interface `TokenVerifier` ; **access token** de l'API (pas l'ID token) ; tenant Auth0 unique pour le local et la production (région Canada, code à usage unique par e-mail) ; création de l'utilisateur au premier login, idempotente ; connexion **obligatoire** partout sauf `/health` et `/ready` |
| 3 | Catalogue de sports | Données de référence idempotentes ; coefficients copiés dans le challenge à sa création |
| 4 | Challenges et catégories | **`policy.go`** (matrice des droits §5.7) ; **aucune autorisation dans un middleware** ; 3 catégories figées ; inscription jusqu'à la fin de la semaine 1 ; plusieurs créateurs ; titre et description seuls modifiables ; test « interdit » pour chaque règle |
| 5 | Activités | **Point d'entrée unique** ; `source` et `external_id` ; **semaine terminée verrouillée** ; modification et suppression tant que sa semaine n'est terminée dans aucun challenge où elle compte ; route de détail des activités des participants (visible des seuls participants du challenge) |
| 6 | Scoring et classement | Tests d'abord ; catégorie 3 : minimum = distance réelle de la semaine précédente + X, aucune élimination en semaine 1, sans délai de grâce ; classement des éliminés (dernier éliminé devant, puis km) ; ex aequo |
| 7 | Clôtures et worker | `close-weeks` et `finalize-challenges` toutes les heures, **idempotents** ; résultat indépendant du moment où le worker passe ; horloge réglable hors production |
| 8 | Invitations (badges reportés en V1.1) | Invitations avec la règle « les participants peuvent inviter » ; les badges (conditions structurées, évaluation après chaque activité) viennent en V1.1 |
| 9 | Limitation de débit et déploiement | `RateLimitIP` et `RateLimitUser`, Docker, compléments de CI/CD, infrastructure de production (configurée à la main), alerte de budget ; jamais de modification de l'infrastructure sans demande |

**À chaque phase** : `make check` passe ; chaque nouvelle route a ses tests d'autorisation (autorisé, interdit, inexistant) ; les erreurs ont un code stable ; aucune donnée personnelle dans les logs ; une paire de migrations par changement de schéma ; les agents de relecture sont lancés ; **commit et push sur la branche du ticket une fois ces vérifications passées ; pull request, fusion et push sur `main` uniquement sur demande**.

### V1.1 (après la V1)

Badges créés par les créateurs (§2.7) et upload d'avatar par URL signée (avatars par défaut en V1).

### V2

Synchronisation Strava (webhooks), puis Polar. Les règles propres aux synchronisations (activités tardives, agrégateur ou connexions directes) se décident à ce moment-là (§5.10).

### V3

Garmin et Coros (demandes de partenariat à lancer tôt, les délais sont longs), notifications push, flux temps réel (SSE).

---

## 8. Questions ouvertes

- ❓ (V2) Synchronisations tardives (Garmin, Coros) : sans délai de grâce, une activité reçue après la fin de sa semaine ne compte pas. À revisiter à l'arrivée des synchronisations.
- ❓ Agrégateur ou connexions directes pour les synchronisations (à trancher avant la V2).
- ✅ Package d'erreurs maison (Faunadex) : examiné, non repris. `platform/apperr` suit le contrat du §5.8 ; ses messages FR/EN pourront alimenter les fichiers de traduction du frontend. Le `translator` (`go-i18n`, fichiers `.toml`) pourra servir plus tard aux textes émis par le serveur (courriels, notifications).
- ❓ Pseudos qui se ressemblent (`marie.92`, `marie_92`, `marie-92`) et pseudos réservés (`admin`, `support`…) : faut-il les refuser ? Aujourd'hui seule la casse est ignorée pour l'unicité.
- ❓ Purge définitive des challenges supprimés : après quel délai, et par quelle tâche du worker ?
- ❓ Confirmer les points marqués 🟡.
