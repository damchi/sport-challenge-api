# Runbook de production

Ce document rassemble les gestes d'exploitation de la production et la liste de contrôle avant l'ouverture au public. Il renvoie aux documents de détail plutôt que de les répéter.

> **Notations.** Les commandes nomment la production telle qu'elle est (projet `sport-challenge-prod`, région `northamerica-northeast1`, ressources Cloud Run préfixées `sc`) : elles se copient sans rien remplacer (le préfixe `sc` vaut tant que la variable `CLOUD_RUN_NAME_PREFIX` du dépôt n'est pas redéfinie), sauf les paramètres propres à chaque incident, entre chevrons (`<révision>`, `<sha court précédent>`, `<version>`, `<secret>`, `<utilisateur>`, `<suffixe>`). L'infrastructure est décrite dans `docs/deploiement.md`.
>
> Toute commande qui modifie la production (base, secrets, révisions, ressources GCP) se fait à deux : une personne lance, une autre relit. Rien n'est lancé sur la production sans accord explicite.

## 1. Déploiement

Détail : `docs/deploiement.md`, `docs/release.md`.

- **Chemin normal** : une pull request fusionnée dans `main` (CI verte) reçoit une version patch (workflow `Release`), puis le workflow `Deploy` attend l'approbation de l'Environment `production`. Une fois approuvé : image construite et poussée, migrations (`sc-migrate`), API et jobs du worker mis à jour, test de fumée.
- **Approuver** : onglet Actions > `Deploy` > *Review deployments*. Avant d'approuver, lire les notes de la release (onglet Releases) et vérifier qu'aucune migration risquée n'en fait partie (`migrations/`).
- **Vérifier après coup** :
  - l'exécution `Deploy` est verte (le test de fumée vérifie que la révision qui sert tout le trafic utilise l'image du commit, puis que `/ready` répond) ;
  - aucune erreur 5xx dans les minutes qui suivent : l'alerte « API : erreurs 5xx » prévient au-delà de 5 réponses 5xx en 5 minutes (`docs/deploiement.md`, « Alertes ») ; une erreur isolée se lit dans les journaux du service `sc-api` ; si l'API ne répond plus du tout (aucune requête, donc aucun 5xx) ou que sa base est injoignable, c'est l'alerte « API injoignable » qui arrive (vérification de `/ready` toutes les 5 minutes, échec pendant 10 minutes) ;
  - Error Reporting ne montre pas de nouvelle erreur.
- **Un déploiement échoue** :
  - au build : rien n'est changé en production, corriger et refusionner ;
  - aux migrations : rien n'est déployé, voir §3 ;
  - au test de fumée : la nouvelle révision est déjà en place. Lire les journaux du service, puis revenir en arrière (§2) si l'API ne répond pas correctement.

## 2. Retour arrière

Détail : `docs/deploiement.md` (Retour arrière) et `docs/migrations.md` (Retour arrière).

Du plus rapide au plus lourd :

1. **Revenir à la révision précédente de l'API** (Cloud Run garde les révisions), le plus rapide quand seule l'API est en cause :

   ```
   gcloud run revisions list --service=sc-api --project=sport-challenge-prod --region=northamerica-northeast1
   gcloud run services update-traffic sc-api --to-revisions=<révision>=100 --project=sport-challenge-prod --region=northamerica-northeast1
   ```

   Le trafic reste alors épinglé sur cette révision : le prochain déploiement crée une révision **sans trafic**, et son test de fumée échoue. Une fois le correctif déployé, remettre le trafic sur la dernière révision (`gcloud run services update-traffic sc-api --to-latest --project=sport-challenge-prod --region=northamerica-northeast1`), vérifier `/ready`, puis relancer le workflow pour obtenir une exécution verte.

   Ce geste ne touche **pas le worker** : ses jobs n'ont pas de trafic et gardent la nouvelle image. Si le worker est en cause (une clôture de semaine fausse élimine à tort à chaque heure), utiliser le point 2, ou remettre l'image précédente sur les jobs du worker :

   ```
   gcloud run jobs update sc-worker-close-weeks --image=northamerica-northeast1-docker.pkg.dev/sport-challenge-prod/sport-challenge/api:<sha court précédent> --project=sport-challenge-prod --region=northamerica-northeast1
   gcloud run jobs update sc-worker-finalize-challenges --image=northamerica-northeast1-docker.pkg.dev/sport-challenge-prod/sport-challenge/api:<sha court précédent> --project=sport-challenge-prod --region=northamerica-northeast1
   gcloud run jobs update sc-worker-retry-auth0-deletions --image=northamerica-northeast1-docker.pkg.dev/sport-challenge-prod/sport-challenge/api:<sha court précédent> --project=sport-challenge-prod --region=northamerica-northeast1
   ```
2. **Redéployer une version précise** par le workflow (API et worker ensemble, trafic sur la dernière révision) : Actions > `Deploy` > *Run workflow*, avec un tag (`v0.1.4`) ou un SHA de `main`. L'image existante est redéployée sans reconstruction **et sans migrations**, après approbation. Les jobs du worker reprennent aussi cette image.
3. **Corriger en avant** : une nouvelle pull request, le chemin normal dès que l'urgence est passée.

Un retour arrière n'annule jamais une migration. Le code plus ancien doit rester compatible avec le schéma actuel, ce que vise chaque migration (ajouter sans retirer ni renommer).

## 3. Migration en échec

Détail : `docs/migrations.md`.

- **Symptôme** : l'étape *Apply the migrations* du workflow `Deploy` échoue (l'alerte « Job Cloud Run en échec » arrive par courriel pour `sc-migrate` ; l'échec se voit aussi dans le workflow et les journaux du job). Rien n'est déployé : la production continue de tourner sur l'ancienne version.
- **Diagnostic** : journaux de l'exécution du job `sc-migrate` dans Cloud Logging. Si la base est marquée `dirty`, ni `up` ni `down` ne s'exécutent.
- **Réparer** : suivre la section « Migration sale » de `docs/migrations.md` (état réel du schéma, remise en état à la main, `migrate force <version>`, correction dans une **nouvelle** migration). Avant toute action sur le schéma, lancer une sauvegarde à la demande :

  ```
  gcloud sql backups create --instance=sc-db --location=northamerica-northeast1 --project=sport-challenge-prod
  ```

- `down` et `force` ne se lancent qu'à la main, par les personnes qui ont, sur le projet ou sur le job `sc-migrate`, un rôle qui permet de lancer un job avec d'autres arguments (`roles/run.developer`, `roles/run.admin`, propriétaire…) : deux au plus (`docs/migrations.md`).

## 4. Relance du worker

Les tâches sont idempotentes : une relance ne refait pas un travail déjà fait.

| Tâche | Planification (`America/Toronto`) | Job Cloud Run |
|---|---|---|
| `close-weeks` | chaque heure pile | `sc-worker-close-weeks` |
| `finalize-challenges` | chaque heure, à :10 | `sc-worker-finalize-challenges` |
| `retry-auth0-deletions` | chaque heure, à :20 | `sc-worker-retry-auth0-deletions` |

- **Symptômes** : l'alerte « Job Cloud Run en échec » (une exécution en échec dans Cloud Run > Jobs, nom du job dans l'incident) ou « close-weeks : aucune exécution réussie depuis 3 heures » (le job ne tourne plus ou échoue à chaque fois ; vérifier aussi la planification dans Cloud Scheduler), `docs/deploiement.md`, « Alertes ». Les semaines ne se clôturent pas, les classements finaux ne se figent pas.
- **Diagnostic** : journaux de la dernière exécution (Cloud Run > Jobs > le job > Exécutions), puis cause (base indisponible, migration en attente, erreur de code).
- **Relancer** une fois la cause traitée :

  ```
  gcloud run jobs execute sc-worker-close-weeks --project=sport-challenge-prod --region=northamerica-northeast1 --wait
  gcloud run jobs execute sc-worker-finalize-challenges --project=sport-challenge-prod --region=northamerica-northeast1 --wait
  ```

  Les relancer dans l'ordre de la planification. Chacune juge ses propres semaines : `close-weeks` les semaines intermédiaires, `finalize-challenges` la dernière semaine d'un challenge, puis fige ses résultats.
- **Suppressions Auth0 en attente** (comptes supprimés dans l'API mais pas encore chez Auth0) : la tâche tourne chaque heure ; pour ne pas attendre, la lancer à la main :

  ```
  gcloud run jobs execute sc-worker-retry-auth0-deletions --project=sport-challenge-prod --region=northamerica-northeast1 --wait
  ```

  À faire après une panne d'Auth0, après une rotation du secret Auth0 (§5), ou si des comptes supprimés peuvent encore se connecter.
- Une semaine déjà terminée se juge selon l'horloge, jamais selon le passage du worker : une relance tardive donne le même résultat qu'une exécution à l'heure.
- **Tester une fin de semaine sans attendre** : seulement en local (`CLOCK_OFFSET` n'est accepté qu'avec `ENV=local`). Les tests d'intégration couvrent le scénario complet avec une horloge simulée (`internal/closure/service_integration_test.go` : élimination semaine par semaine, relance sans effet, abandon, résultats figés). Pour le voir à la main, en local : avancer l'horloge de l'API et du worker du même décalage (`CLOCK_OFFSET=168h make run`, puis `CLOCK_OFFSET=168h make worker TASK=close-weeks`) ; une activité datée d'une semaine terminée doit alors être refusée (`week_locked`) avant même le passage du worker.

## 5. Rotation des secrets

Aucun secret dans le dépôt ; les valeurs vivent dans Secret Manager (projet `sport-challenge-prod`). Les secrets sont lus en version `latest` : chaque exécution d'un job (worker, migrations) et chaque nouvelle instance de l'API prennent la nouvelle valeur. Les instances de l'API déjà démarrées gardent l'ancienne : forcer une nouvelle révision de l'API (par exemple `gcloud run services update sc-api --revision-suffix=<suffixe> --project=sport-challenge-prod --region=northamerica-northeast1`, à valider une première fois hors production). Passer par le workflow (§2, point 2) marche aussi, mais attend l'approbation.

Pour saisir un secret sans le laisser dans l'historique du shell :

```
read -rs S && printf '%s' "$S" | gcloud secrets versions add <secret> --data-file=- --project=sport-challenge-prod; unset S
```

| Secret | Quand | Comment |
|---|---|---|
| `auth0-mgmt-client-secret-ca` | exposition, départ d'une personne, au moins une fois par an | Auth0 : **Credentials → Rotate** sur l'application machine à machine (l'ancien secret cesse aussitôt de marcher). Nouvelle version du secret (commande ci-dessus), nouvelle révision de l'API, vérification (une suppression de compte de test passe chez Auth0), **désactivation** de l'ancienne version (`gcloud secrets versions disable <version> --secret=auth0-mgmt-client-secret-ca --project=sport-challenge-prod`), puis `retry-auth0-deletions` (§4) pour les suppressions échouées entre-temps. Voir `docs/auth0-setup.md`. |
| `database-url-ca` (mot de passe de l'utilisateur `sport`) | exposition, départ d'une personne | Nouveau mot de passe généré par le gestionnaire de mots de passe (32 caractères au moins, jamais affiché dans le terminal), saisi dans Cloud SQL (`gcloud sql users set-password <utilisateur> --instance=sc-db --prompt-for-password --project=sport-challenge-prod`) ; l'URL complète se compose dans le gestionnaire (mot de passe encodé pour URL, hôte = socket du connecteur : `docs/deploiement.md`, « Connexion à la base »), jamais avec `echo` ni une variable exportée, puis devient une nouvelle version du secret `database-url-ca` (commande ci-dessus) ; enfin, **destruction** de l'ancienne version (`gcloud secrets versions destroy <version> --secret=database-url-ca --project=sport-challenge-prod`), qui contient un mot de passe révoqué. L'ancien mot de passe cesse de marcher dès le premier geste : prévoir une courte coupure de l'API : forcer aussitôt une nouvelle révision, puis vérifier `/ready` et un job du worker. |
| Client OAuth Google (dans Auth0) | exposition | Nouveau secret dans Google Cloud (*Credentials*), saisi dans Auth0 ; il ne passe jamais par l'API ni par le dépôt. |

Le déploiement n'utilise **aucune clé** (Workload Identity Federation) : rien à faire tourner côté GitHub. En cas de doute sur un accès, retirer la personne des reviewers de l'Environment `production`, des droits d'écriture du dépôt et des rôles GCP.

Ne jamais coller un secret dans un ticket, un message ou un journal. Un secret exposé se traite comme un incident (§7).

## 6. Liste de contrôle avant l'ouverture au public

> Les points juridiques sont à faire valider par une personne compétente (README §6) : cette liste dit ce qui doit exister, pas ce qui suffit.

### Confidentialité et conformité

- [ ] **Politique de confidentialité** publiée en **français et en anglais**, qui mentionne :
  - que les activités détaillées d'un participant sont **visibles par les autres participants** du challenge (l'application le dit aussi avant de rejoindre), **y compris après avoir quitté le challenge ou en avoir été exclu**, pour toutes les activités datées de sa fenêtre (README §5.7) ;
  - que le **prénom et le nom** d'un participant sont visibles par les autres utilisateurs (participants, classements ; pour un challenge public, par tout utilisateur connecté), et que son **adresse e-mail**, reçue d'Auth0, n'est visible que de lui (README §2.8) ;
  - les données conservées (profil : pseudo, prénom, nom, e-mail ; résumés d'activité), leur lieu (Canada, Montréal) et leur durée de conservation (à décider : la purge des challenges supprimés est une question ouverte, README §8) ;
  - l'effet d'une suppression de compte (anonymisation : pseudo, prénom, nom et e-mail effacés ; résultats des challenges terminés conservés sous un nom neutre) ;
  - la personne responsable de la protection des renseignements personnels et comment la joindre.
- [ ] **Loi 25** : validation juridique faite (consentement, personne responsable désignée, registre des incidents ouvert, évaluation des facteurs relatifs à la vie privée du système, évaluation avant tout transfert hors Québec).
- [ ] **Accord de traitement des données** signé avec **Auth0** (Okta), et région du tenant vérifiée (Canada).
- [ ] **Inventaire des sous-traitants** tenu à jour, avec la région de chacun : GCP (Montréal), Auth0 (Canada), le fournisseur de courriel configuré dans Auth0 (il traite les adresses courriel, `docs/auth0-setup.md`), et tout ajout.

### Vérifications techniques

- [ ] **Suppression de compte testée** en production avec un compte de test : anonymisation dans l'API, abandon des challenges à venir ou en cours, résultats des challenges terminés sous un nom neutre, **utilisateur supprimé chez Auth0** (et reprise par `retry-auth0-deletions` si Auth0 a échoué).
- [ ] **Sauvegardes testées** : restauration d'une sauvegarde (ou clone à un instant donné) vers une **instance séparée**, puis vérification des données ; durée de l'opération notée ici.
- [ ] **Quotas** vérifiés : instances maximales de Cloud Run, connexions Cloud SQL, quotas de l'API de gestion Auth0 (suppressions) et du plan Auth0 (utilisateurs actifs).
- [ ] **Budget** : alertes de budget actives (50 %, 90 %, 100 %) et reçues par une adresse lue ; coût fixe de Cloud SQL (`db-f1-micro`, zonale) connu.
- [ ] **Alertes** reçues au moins une fois (5xx, échec de job, absence de `close-weeks`, API injoignable) : chaque courriel est arrivé lors du test du 2026-11-17 (`docs/deploiement.md`, « Alertes ») ; à refaire si le canal ou l'adresse change.
- [ ] **Accès** : Environment `production` avec reviewers, tags `v*` protégés, deux personnes au plus avec les droits `down`/`force` sur le job de migration, vérifié dans la politique IAM du projet (`gcloud projects get-iam-policy sport-challenge-prod`).
- [ ] Ce runbook relu, et les contacts du §7 remplis.

## 7. Incidents

### Contacts

| Rôle | Personne | Moyen de contact |
|---|---|---|
| Responsable de la protection des renseignements personnels (Loi 25) | _à remplir_ | _à remplir_ |
| Astreinte technique (API, base, déploiement) | _à remplir_ | _à remplir_ |
| Remplaçant·e | _à remplir_ | _à remplir_ |
| Support Auth0 | compte du tenant | tableau de bord Auth0 > Support |
| Support GCP | compte de facturation | console GCP > Support |

### Procédure

1. **Constater** : alerte, signalement d'un utilisateur ou anomalie. Noter l'heure (UTC).
2. **Contenir** : retour arrière (§2), désactivation d'un accès, rotation d'un secret (§5). Préférer le geste réversible.
3. **Évaluer** : des renseignements personnels sont-ils touchés (accès, perte, communication non autorisée) ? Si oui, prévenir **sans attendre** la personne responsable de la protection des renseignements personnels.
4. **Notifier si nécessaire** : la Loi 25 impose d'aviser la Commission d'accès à l'information et les personnes concernées quand l'incident présente un **risque de préjudice sérieux**. La décision revient à la personne responsable.
5. **Corriger** puis **clore** : correctif déployé, cause comprise, action de prévention notée.

### Registre des incidents

La Loi 25 impose de tenir un registre de **tous** les incidents de confidentialité, même sans risque de préjudice sérieux. Chaque entrée se conserve au moins **5 ans** après la prise de connaissance de l'incident, et la Commission d'accès à l'information peut en demander une copie. Le registre ne se tient **pas dans ce dépôt** (il peut contenir des renseignements personnels) : il vit dans un espace privé, accessible à la personne responsable et à l'astreinte. Chaque entrée contient :

- date et heure de l'incident, et de sa découverte ;
- description des renseignements touchés et du nombre approximatif de personnes ;
- circonstances et cause ;
- mesures prises pour réduire le risque ;
- évaluation du risque de préjudice, et décision de notifier ou non (avec la date des avis envoyés).
