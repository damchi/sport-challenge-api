# Configuration d'Auth0

Procédure manuelle, suivie pour le seul tenant du projet, à refaire à l'identique si un autre tenant est créé un jour. Elle crée ce dont l'API a besoin pour valider les jetons (ticket SC-API-017) et supprimer un compte chez Auth0 (ticket SC-API-039).

Aucun secret dans le dépôt. Les valeurs non secrètes (domaine, audience, identifiant de la SPA) vont dans la configuration ; le seul secret (client de l'API de gestion) va dans Secret Manager.

> **Navigation du dashboard.** Les chemins ci-dessous suivent la nouvelle navigation d'Auth0 (barre latérale en sections : Integration, Customization, Messaging, Automation & Extensibility, Security & Monitoring), en Early Access en novembre 2026. L'ancienne navigation reste accessible depuis le menu du profil (en haut à droite) ; ses chemins sont rappelés entre parenthèses quand ils diffèrent. Les chemins marqués (à confirmer) n'ont pas encore été vérifiés dans la nouvelle navigation : corrige-les ici après le premier passage.

## Décisions

- ✅ Auth0, un seul tenant, `sport-challenge-prod`, partagé par le développement local et la production, en région Canada (vérifié : disponible avec le forfait choisi). Décision du propriétaire du produit du 2026-11-19 (ticket SC-API-115, issue #216), qui remplace « un tenant par environnement ». La région ne se change pas après la création, et le domaine d'un tenant ne se renomme pas.
- ✅ Connexion : e-mail par code à usage unique, seule méthode en V1. Pas de mot de passe. Le lien magique n'est pas utilisé : le New Universal Login ne le propose pas. Les connexions sociales (Google, Apple) sont reportées : leur procédure est en fin de document.
- ✅ V1 web uniquement : seule l'application SPA est créée. L'application native (mobile) viendra en V2 (section « V2 : application native »).
- ✅ Suppression de compte : l'API supprime aussi l'utilisateur chez Auth0 (décision D7), avec une application machine à machine limitée à `delete:users`.
- ✅ L'API identifie un utilisateur uniquement par la claim `sub`, jamais par l'e-mail. Quand des connexions sociales seront ajoutées, une même personne qui se connecte avec Google puis avec le code par e-mail (même adresse) obtiendra deux comptes : ne jamais activer de liaison automatique de comptes par e-mail (risque de prise de compte). La liaison de comptes est une question produit ouverte (❓).

## Environnements

| Environnement | Tenant | Domaine (`AUTH0_DOMAIN`) | Audience (`AUTH0_AUDIENCE`) | URL du frontend |
|---|---|---|---|---|
| Production ✅ | `sport-challenge-prod` | `sport-challenge-prod.ca.auth0.com` | `https://api.sport-challenge` | à définir |
| Développement local ✅ | `sport-challenge-prod` (le même) | `sport-challenge-prod.ca.auth0.com` | `https://api.local.sport-challenge` | `http://localhost:5173` |

État : le tenant est créé le 2026-11-19 avec cette procédure ; à vérifier avec la liste de la section « Vérification », puis à reporter dans la configuration de l'API et des jobs (Cloud Run, Secret Manager) et du frontend (`docs/deploiement.md`). L'URL du frontend de production s'ajoutera à la SPA quand elle sera connue.

Conséquences du tenant unique :

- Les utilisateurs de test et ceux de production sont dans le même tenant : une adresse connectée en local a le même compte Auth0 (même `sub`) en production. Les bases restent séparées : l'utilisateur local n'est créé que dans la base locale. La suppression d'un compte depuis l'API locale ne touche pas Auth0 : l'application machine à machine locale n'a aucun droit (étape 5), la demande échoue et reste en attente de reprise dans la base locale.
- Le local et la production ont chacun leur SPA et leur API Auth0 (étapes 2 et 3) : Sport Challenge Web (URL de production seulement) est autorisée sur `https://api.sport-challenge`, Sport Challenge Web (local) (`localhost` seulement) sur `https://api.local.sport-challenge`. Un jeton obtenu depuis `localhost` porte l'audience locale : l'API de production le refuse. Le même émetteur ne suffit pas à le faire accepter, l'audience est vérifiée à chaque requête.
- Le fournisseur d'e-mail (étape 4) et les protections (étape 6) sont ceux de la production : les codes envoyés pendant les tests passent par eux et comptent dans leurs limites.
- Deux applications machine à machine (étape 5) : Sport Challenge API (management) pour la production, avec `delete:users`, dont le secret n'est que dans Secret Manager, et Sport Challenge API (local) pour le `.env` des postes, sans aucun droit sur l'API de gestion. Une fuite du `.env` ne permet de supprimer aucun compte, et une rotation de l'une ne touche pas l'autre.

Le domaine exact est affiché dans les réglages d'une application (**Applications** → Sport Challenge Web → Settings → Domain) : reporte-le ici s'il diffère de `<tenant>.ca.auth0.com`. Le `ca` du domaine (ou de l'adresse du dashboard, `manage.auth0.com/dashboard/ca/…`) confirme la région Canada. L'audience est un identifiant (une URI), pas une adresse appelée : elle ne se change pas après la création de l'API. Le port du frontend local (5173) est celui de Vite par défaut : adapte-le au dépôt du frontend.

## Forfait

- Le forfait Free ne permet qu'un seul tenant (le dashboard affiche « Limit Reached » sur la page Tenants de la Team). Pour créer `sport-challenge-prod`, supprimer d'abord tout autre tenant de la Team (par exemple `sport-challenge-dev`) : dans ce tenant, **Settings** → onglet Advanced → Danger Zone → Delete → Yes, Delete Tenant. La suppression est définitive.
- Un nouveau compte démarre avec un essai qui donne accès à des fonctions hors forfait Free (bandeau sur **Home**). À la fin de l'essai, le tenant repasse au Free : vérifier alors que les réglages des étapes 6 et 7 sont toujours disponibles et actifs.

## 1. Créer le tenant

1. Sélecteur de tenant dans la barre du haut (à droite du nom de la Team) → Create tenant (voir « Forfait » si la création est refusée).
2. Tenant Domain : le nom du tableau ci-dessus ; Region : Canada ; Environment Tag : Development ou Production.
3. **Settings** → General : nom affiché Sport Challenge, langues fr et en (français par défaut), adresse de support.
4. Rétention des logs : rien à régler, elle est fixée par le forfait et ne se configure pas dans le dashboard. Les logs Auth0 contiennent des données personnelles (adresse IP, e-mail) : ne pas l'allonger (log streaming, forfait supérieur) sans raison (Loi 25).
5. **Applications** : supprimer la Default App créée avec le tenant.

## 2. Créer l'API (audience)

Le tenant étant partagé, cette étape est faite deux fois, avec les mêmes réglages : Sport Challenge API (identifiant `https://api.sport-challenge`) pour la production, puis Sport Challenge Local API (`https://api.local.sport-challenge`) pour le développement local.

1. **APIs** → Create API.
2. Name : Sport Challenge API ou Sport Challenge Local API ; Identifier : l'audience du tableau ; JSON Web Token (JWT) Profile : Auth0 ; Signing Algorithm : RS256 (l'API refuse tout autre algorithme).
3. Onglet Settings :
   - Maximum Access Token Lifetime : 900 (15 min). Un jeton reste valide jusqu'à son expiration, même après une suppression de compte ou une exclusion : la rotation des refresh tokens rend une durée courte sans effet pour l'utilisateur.
   - Implicit / Hybrid Flow Access Token Lifetime : 900 (sans effet puisque le grant Implicit est désactivé, mais aligné).
   - Allow Offline Access : activé (nécessaire aux refresh tokens).
   - Enable RBAC : désactivé (l'autorisation se fait dans les services de l'API, pas dans le jeton).
   - Allow Skipping User Consent : activé. Sport Challenge Web est une application interne (first-party) : l'utilisateur n'a pas à autoriser l'accès à sa propre API. En local, l'écran de consentement reste affiché malgré ce réglage : Auth0 ne le saute jamais pour une application servie depuis localhost, faute de pouvoir vérifier son origine. Il suffit de l'accepter une fois par utilisateur de test.
4. Aucune permission (scope) à créer pour l'instant.
5. Supprimer l'application de test : Auth0 crée en général, avec l'API, une application machine à machine Sport Challenge API (Test Application) autorisée sur cette audience. Avec son secret, n'importe qui obtiendrait un jeton valide pour l'API (`sub` en `…@clients`). La supprimer (**Applications**), puis vérifier dans l'onglet Machine To Machine Applications de l'API (ou Application Access, selon la version) qu'aucune application n'est autorisée.
6. Autoriser la SPA de l'environnement en accès utilisateur (obligatoire), et elle seule : Sport Challenge Web sur Sport Challenge API, Sport Challenge Web (local) sur Sport Challenge Local API. Jamais la SPA locale sur l'API de production. Sans ce réglage, la connexion du frontend échoue avec `Client "<Client ID de la SPA>" is not authorized to access resource server "<audience>"`. À faire une fois la SPA créée (étape 3) :
   - **APIs** → l'API → Settings → Application Access Policy → User-Delegated Access : Per-app authorization. Seules les applications autorisées une à une obtiennent alors un jeton utilisateur pour l'API.
   - Onglet Application Access → Edit sur la SPA de l'environnement → User-Delegated Access : Grant Access, sans aucune permission (l'API n'en définit pas).
   - Aucune autre application n'y a d'accès, ni utilisateur ni machine à machine (étape 2.5).

## 3. Créer l'application web (SPA)

Le tenant étant partagé, cette étape est faite deux fois, avec les mêmes réglages sauf les URL : Sport Challenge Web pour la production (URL du frontend de production seulement, à ajouter quand elle sera connue), puis Sport Challenge Web (local) pour le développement local (`http://localhost:5173` seulement).

1. **Applications** → Create Application → Single Page Web Applications, nom Sport Challenge Web ou Sport Challenge Web (local).
2. Onglet Settings, avec l'URL du frontend de l'environnement (`<front>`) :
   - Allowed Callback URLs : `<front>/callback`
   - Allowed Logout URLs : `<front>`
   - Allowed Web Origins : `<front>`
   - Allowed Origins (CORS) : `<front>`
   - Jamais de joker (`*`) ; `http://localhost` seulement sur Sport Challenge Web (local), jamais sur Sport Challenge Web.
3. Refresh Token Rotation :
   - Rotation : activée ; Reuse Interval : 0.
   - Absolute Expiration : activée, 2592000 (30 jours).
   - Inactivity Expiration : activée, 604800 (7 jours).
4. Advanced Settings → Grant Types : seulement Authorization Code et Refresh Token (décocher Implicit). Cross-Origin Authentication : désactivée (inutile avec Universal Login). ID Token Expiration : 3600 (1 h). L'API refuse de toute façon les ID tokens : leur audience est le Client ID, pas `AUTH0_AUDIENCE`.
5. Pas de méthode d'authentification à régler : le type Single Page Application l'impose à None (une SPA ne garde pas de secret ; le flux utilise PKCE), et il n'y a pas d'onglet Credentials.
6. Onglet Connections : désactiver google-oauth2 (créée par défaut par Auth0) et toute autre connexion. La connexion email (code) n'apparaît ici qu'une fois créée dans le tenant : revenir l'activer après l'étape 4.
7. Noter le Client ID : il n'est pas secret, il va dans la configuration du frontend (celui de Sport Challenge Web (local) dans la configuration locale).
8. À transmettre au frontend :
   - garder les jetons en mémoire (`cacheLocation: 'memory'` du SDK Auth0, jamais `localStorage`, à cause du risque XSS) et utiliser les refresh tokens (`useRefreshTokens: true`) ;
   - passer `connection: 'email'` dans les `authorizationParams` de la connexion : le New Universal Login n'affiche la connexion par code que si ce paramètre est envoyé à `/authorize` (sinon l'écran de connexion ne propose pas le code, même avec Identifier First) ;
   - la déconnexion par redirection (`/v2/logout`) ne révoque pas le refresh token : le révoquer explicitement à la déconnexion.
9. Revenir à l'étape 2.6 : autoriser la SPA en accès utilisateur sur l'API de son environnement.

## 4. Activer la connexion par code

1. Désactiver les connexions sociales créées par défaut : google-oauth2 est créée automatiquement avec chaque tenant (**Connections**, ancienne navigation : Authentication → Social). La désactiver pour les deux SPA (étape 3.6), ou la supprimer si le dashboard le permet.
2. Supprimer la connexion par mot de passe : **Authentication** → Database → Username-Password-Authentication → supprimer la connexion. La désactiver seulement pour la SPA la laisserait disponible pour toute future application.
3. Code par e-mail (**Authentication** → Passwordless → Email). Ne pas utiliser le passwordless intégré aux connexions Database (Early Access) : cette procédure s'appuie sur la connexion Passwordless Email.
   - Activer la connexion. Onglet Settings :
     - OTP Expiry : 300 (5 min) ; OTP Length : 6.
     - Disable Sign Ups : désactivé (l'inscription se fait à la première connexion ; l'API crée l'utilisateur local au premier login).
     - Promote Connection to Domain Level : désactivé (sinon la connexion est ouverte à toutes les applications du tenant, y compris tierces).
     - Authentication Parameters : vide.
     - From, Subject, Body : ignorés tant que le fournisseur d'e-mail d'Auth0 est utilisé (avertissement affiché) ; les personnaliser en français et en anglais après avoir configuré un fournisseur d'e-mail (production).
   - Onglet Applications de la connexion (ou onglet Connections de la SPA) : l'activer pour Sport Challenge Web et Sport Challenge Web (local).
4. **Authentication** → Authentication Profile : choisir Identifier First (pas Identifier First + Biometrics). L'écran de connexion demande d'abord l'e-mail, puis envoie le code. Sans ce mode, la connexion par code ne fonctionne pas avec le New Universal Login.
5. Vérifier avec le bouton Try que la connexion par code fonctionne (il exige les deux réglages ci-dessus : Identifier First et au moins une application assignée).
6. Avant la mise en production, configurer un fournisseur d'e-mail (**Emails**, section Messaging, à confirmer ; ancienne navigation : Branding → Email Provider) sur le tenant, partagé avec le développement local : le fournisseur de test d'Auth0 est limité et ne convient qu'au développement.
7. Universal Login (**Branding** et **Pages**, section Customization, à confirmer ; ancienne navigation : Branding → Universal Login) : expérience New, langues fr et en.

## 5. Créer l'application machine à machine (API de gestion)

Elle sert uniquement à supprimer un utilisateur chez Auth0 quand il supprime son compte (ticket SC-API-039).

Le tenant étant partagé, cette étape est faite deux fois : Sport Challenge API (management) pour la production, puis Sport Challenge API (local) pour le développement local, avec les mêmes réglages sauf l'étape 2 : l'application locale n'est autorisée sur aucune API (l'API locale démarre avec ses identifiants, et une suppression de compte en local échoue chez Auth0 sans rien supprimer). Le secret de production ne va jamais dans un `.env`.

1. **Applications** → Create Application → Machine to Machine Applications, nom Sport Challenge API (management) (production) ou Sport Challenge API (local).
2. API autorisée : Auth0 Management API, avec une seule permission : `delete:users`. Ne rien cocher d'autre. Pour Sport Challenge API (local) : n'autoriser aucune API (si l'assistant de création l'exige, choisir l'Auth0 Management API sans cocher de permission, puis retirer l'autorisation dans l'onglet APIs).
3. Onglet Connections : aucune connexion activée. Advanced Settings → Grant Types : seulement Client Credentials.
4. Onglet Credentials : Client Secret (Post), la seule méthode que l'API prend en charge (Private Key JWT serait un changement du client, `internal/platform/auth/management.go`). Noter le Client ID et le Client Secret.
5. Ranger le Client Secret dans Secret Manager (secret `auth0-mgmt-client-secret-ca`, réplication gérée à Montréal seulement, `docs/deploiement.md`), jamais dans un fichier du dépôt, un ticket ou un message. Le Client ID est, en production, une simple variable d'environnement (`AUTH0_MGMT_CLIENT_ID`) de l'API et des jobs : seul, il ne permet pas d'obtenir de jeton.
6. Le compte de service qui exécute l'API et les jobs (`sc-run`) lit le secret avec `roles/secretmanager.secretAccessor`. Le rôle lui est donné sur ce secret et sur `database-url-ca` seulement, pas sur le projet (SC-API-102). L'API lit le secret dans la variable `AUTH0_MGMT_CLIENT_SECRET` (injectée depuis Secret Manager) et le Client ID dans `AUTH0_MGMT_CLIENT_ID` (configuration de Cloud Run) : les deux sont obligatoires dans tous les environnements (le démarrage échoue sans elles) ; en développement local, elles vont seulement dans `.env` (ignoré par git), avec les identifiants de Sport Challenge API (local), jamais ceux de la production.
7. Si le secret a été exposé : Credentials → Rotate dans Auth0, nouvelle version dans Secret Manager, puis désactivation de l'ancienne version.

## 6. Protections du tenant

**Attack Protection** (section Security & Monitoring ; ancienne navigation : Security → Attack Protection) :

- Bot Detection : activé si le forfait le permet (à revérifier à la fin de l'essai, voir « Forfait »).
- Suspicious IP Throttling : activé.
- Brute-force Protection : activé.
- Vérifier que ces limites couvrent l'envoi des courriels passwordless (pour empêcher d'inonder une adresse de codes de connexion).

## 7. Domaine personnalisé (production)

À faire lors de la mise en place du déploiement, une fois le domaine du frontend connu : ajouter un Custom Domain Auth0 (**Domains**, section Customization ; ancienne navigation : Branding → Custom Domains), par exemple `login.<domaine du frontend>`, avec le certificat géré par Auth0. Vérifier d'abord que le forfait le permet.

Raison : les jetons sont gardés en mémoire (étape 3.8), donc perdus à chaque rechargement de la page. Le SDK rétablit alors la session sans afficher de page, avec un iframe vers le domaine Auth0 qui lit le cookie de session d'Auth0. Sur un domaine `*.auth0.com`, ce cookie est tiers. Safari et les navigateurs qui bloquent les cookies tiers ne l'envoient pas, et l'utilisateur repasse par la page de connexion Auth0 à chaque rechargement. Avec un domaine personnalisé sur le même site que le frontend, le cookie n'est plus tiers.

Réglage de l'API : ce changement modifie l'émetteur des jetons, et `AUTH0_DOMAIN` doit alors être le domaine personnalisé. L'API de gestion, elle, reste sur le domaine du tenant : régler en même temps `AUTH0_MGMT_DOMAIN` sur le domaine du tenant (`sport-challenge-prod.ca.auth0.com`), dans la configuration de l'API et des jobs du worker (Cloud Run). Sans elle, `AUTH0_MGMT_DOMAIN` vaut `AUTH0_DOMAIN`, et la suppression de compte chez Auth0 échouerait. Voir « Domaine personnalisé » ci-dessous.

En développement local, le frontend et l'API gardent le domaine du tenant (les jetons y ont l'émetteur du tenant) : on accepte de repasser par la page Auth0 à chaque rechargement.

## 8. Ajouter l'e-mail à l'access token (Action)

L'API lit l'e-mail de l'utilisateur dans l'access token (SC-API-116) : un access token n'a pas de claim `email`, et Auth0 n'y garde une claim ajoutée que si son nom a un espace de noms. Une Action post-login l'ajoute sous `https://sport-challenge/email` ; l'API ignore une claim `email` sans espace de noms. Le tenant étant partagé, l'Action sert au local et à la production.

1. **Actions** (section Automation & Extensibility, à confirmer ; ancienne navigation : Actions → Library) → Create Action → Build from scratch : nom Add e-mail to access token, trigger Login / Post Login, runtime Node le plus récent proposé.
2. Code :
   ```js
   exports.onExecutePostLogin = async (event, api) => {
     // Only a verified address: the passwordless e-mail connection verifies it.
     if (event.user.email && event.user.email_verified) {
       api.accessToken.setCustomClaim('https://sport-challenge/email', event.user.email);
     }
   };
   ```
3. Deploy, puis le déclencheur post-login (**Actions** → Triggers → post-login, à confirmer) : glisser l'Action dans le flux, Apply.
4. Vérifier : se connecter depuis le frontend, puis `GET /api/v1/me` renvoie `email`. Un jeton obtenu avant l'ajout de l'Action n'a pas la claim : l'e-mail arrive au jeton suivant (rafraîchissement ou nouvelle connexion).

L'e-mail est une donnée personnelle : l'API ne le journalise jamais et ne le renvoie qu'à son titulaire (README §2.8).

## Points d'attention pour l'API (ticket SC-API-017)

- Jetons machine à machine : refuser un access token dont `gty` vaut `client-credentials` ou dont `sub` se termine par `@clients`, en défense en profondeur de l'étape 2.5.
- Domaine personnalisé (étape 7, prévu en production) : l'émetteur des jetons devient `https://<custom-domain>/`, et `AUTH0_DOMAIN` doit être ce domaine. L'API de gestion, elle, garde l'adresse et l'audience du domaine du tenant (`https://<tenant>.ca.auth0.com/api/v2/`) : le client de suppression (ticket SC-API-039) les construit depuis `AUTH0_MGMT_DOMAIN` (SC-API-114), qui vaut `AUTH0_DOMAIN` si elle est absente. Elle doit donc être réglée sur le domaine du tenant en même temps que `AUTH0_DOMAIN` passe au domaine personnalisé. La validation des jetons (émetteur, JWKS) reste sur `AUTH0_DOMAIN`.

## Valeurs à reporter

Pour le tenant, une fois la procédure terminée :

| Valeur | Secrète | Destination |
|---|---|---|
| `AUTH0_DOMAIN` | non | configuration de l'API (Cloud Run) et du frontend |
| `AUTH0_AUDIENCE` | non | configuration de l'API (Cloud Run) et du frontend : `https://api.sport-challenge` ; en local (`.env`, configuration locale du frontend) : `https://api.local.sport-challenge` |
| `AUTH0_MGMT_DOMAIN` | non | configuration de l'API et des jobs (Cloud Run), seulement avec un domaine personnalisé : le domaine du tenant, `sport-challenge-prod.ca.auth0.com` (étape 7). Absente, elle vaut `AUTH0_DOMAIN` |
| Client ID de la SPA | non | configuration du frontend : Sport Challenge Web en production, Sport Challenge Web (local) en local |
| Client ID de l'application machine à machine | non (seul, il ne donne aucun accès) | configuration de l'API et des jobs (Cloud Run), variable `AUTH0_MGMT_CLIENT_ID` (Sport Challenge API (management)) ; `.env` local avec celui de Sport Challenge API (local) |
| Client Secret de l'application machine à machine | oui | Secret Manager : `auth0-mgmt-client-secret-ca`, lu par l'API dans `AUTH0_MGMT_CLIENT_SECRET` (Sport Challenge API (management)) ; `.env` local avec celui de Sport Challenge API (local) |

## Vérification

Pour le tenant :

- [ ] Tenant en région Canada (`ca` dans le domaine de l'application ou dans l'adresse du dashboard).
- [ ] Les deux API (Sport Challenge API, Sport Challenge Local API) en RS256, durée de vie des access tokens à 15 min, accès hors ligne activé, Allow Skipping User Consent activé.
- [ ] Les deux API en Per-app authorization ; chacune n'autorise que la SPA de son environnement en accès utilisateur, sans permission (étape 2.6).
- [ ] Application de test de l'API et Default App supprimées ; aucune application machine à machine autorisée sur l'API.
- [ ] SPA : Sport Challenge Web avec les seules URL de production, Sport Challenge Web (local) avec les seules URL `localhost`, sans joker ; rotation des refresh tokens activée ; grant Implicit décoché.
- [ ] Profil d'authentification en Identifier First ; seule la connexion par code e-mail est activée sur les deux SPA ; google-oauth2 désactivée ; connexion par mot de passe supprimée.
- [ ] Frontend : `connection: 'email'` passé à la connexion (étape 3.8).
- [ ] Application machine à machine Sport Challenge API (management) limitée à `delete:users`, Sport Challenge API (local) sans aucune API autorisée ; grant Client Credentials seul, aucune connexion ; secret de production dans Secret Manager seulement, accès réservé au compte de service de l'API ; le `.env` local porte celui de l'application locale.
- [ ] Connexion de test réussie depuis le frontend (ou **Authentication** → Passwordless → Email → Try dans le dashboard).
- [ ] Action Add e-mail to access token déployée et placée dans le flux post-login (étape 8) ; `GET /api/v1/me` renvoie l'e-mail.
- [ ] Après la fin de l'essai : protections de l'étape 6 toujours actives.
- [ ] Production : domaine personnalisé Auth0 actif, `AUTH0_DOMAIN` réglé sur ce domaine et `AUTH0_MGMT_DOMAIN` sur le domaine du tenant (étape 7) ; suppression de compte testée ensuite.

## V2 : application native

À faire quand le mobile démarre :

1. **Applications** → Create Application → Native, nom Sport Challenge Mobile.
2. Allowed Callback URLs et Allowed Logout URLs : `<bundle-id>://<AUTH0_DOMAIN>/ios/<bundle-id>/callback` et `<bundle-id>://<AUTH0_DOMAIN>/android/<package>/callback` (format des SDK Auth0).
3. Rotation des refresh tokens activée, mêmes durées que la SPA.
4. Connexions : les mêmes que la SPA. Sign in with Apple est obligatoire sur iOS dès qu'une connexion sociale est proposée.

## Plus tard : connexions sociales

Reportées après la V1. Dans la nouvelle navigation, les connexions sociales sont dans **Connections** (ancienne navigation : Authentication → Social), ou depuis l'onglet Connections de l'application. Ajouter alors la question de la liaison de comptes (même e-mail, plusieurs `sub`) avant de les activer.

Google (**Connections** → Google / Gmail) :

- En développement, les clés de test d'Auth0 suffisent.
- En production, créer un client OAuth dans Google Cloud (APIs & Services → Credentials → OAuth client ID, type Web application, URI de redirection `https://<AUTH0_DOMAIN>/login/callback`) et saisir son identifiant et son secret dans Auth0. Le secret Google reste dans Auth0 : il ne passe jamais par le dépôt ni par l'API.
- Attributs : email, profile.

Apple (**Connections** → Apple), compte Apple Developer requis :

1. Créer un Services ID (identifiant web), une clé Sign in with Apple (fichier `.p8`) et noter le Team ID et le Key ID.
2. Return URL côté Apple : `https://<AUTH0_DOMAIN>/login/callback`.
3. Saisir dans Auth0 le Services ID, le Team ID, le Key ID et le contenu de la clé. La clé reste dans Auth0.
4. L'activer ensuite pour Sport Challenge Web (onglet Applications de la connexion).
