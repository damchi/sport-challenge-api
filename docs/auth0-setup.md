# Configuration d'Auth0

Procédure manuelle, suivie pour le seul tenant du projet, à refaire à l'identique si un autre tenant est créé un jour. Elle crée ce dont l'API a besoin pour valider les jetons (ticket SC-API-017) et supprimer un compte chez Auth0 (ticket SC-API-039).

**Aucun secret dans le dépôt.** Les valeurs non secrètes (domaine, audience, identifiant de la SPA) vont dans la configuration ; le seul secret (client de l'API de gestion) va dans Secret Manager.

## Décisions

- ✅ Auth0, **un seul tenant**, `sport-challenge-dev`, partagé par le développement local et la production, en région **Canada** (vérifié : disponible avec le forfait choisi). Décision du propriétaire du produit du 2026-11-19, qui remplace « un tenant par environnement » : aucun tenant `sport-challenge-prod` n'est à créer. La région ne se change pas après la création, et le domaine d'un tenant ne se renomme pas.
- ✅ Connexion : e-mail par **code à usage unique**, seule méthode en V1. Pas de mot de passe. Le lien magique n'est pas utilisé : le New Universal Login ne le propose pas. Les connexions sociales (Google, Apple) sont reportées : leur procédure est en fin de document.
- ✅ V1 web uniquement : seule l'application SPA est créée. L'application native (mobile) viendra en V2 (section « V2 : application native »).
- ✅ Suppression de compte : l'API supprime aussi l'utilisateur chez Auth0 (décision D7), avec une application machine à machine limitée à `delete:users`.
- ✅ L'API identifie un utilisateur **uniquement** par la claim `sub`, jamais par l'e-mail. Quand des connexions sociales seront ajoutées, une même personne qui se connecte avec Google puis avec le code par e-mail (même adresse) obtiendra deux comptes : ne jamais activer de liaison automatique de comptes par e-mail (risque de prise de compte). La liaison de comptes est une question produit ouverte (❓).

## Environnements

| Environnement | Tenant | Domaine (`AUTH0_DOMAIN`) | Audience (`AUTH0_AUDIENCE`) | URL du frontend |
|---|---|---|---|---|
| Développement local ✅ | `sport-challenge-dev` | `sport-challenge-dev.ca.auth0.com` | `https://api.dev.sport-challenge` | `http://localhost:5173` |
| Production ✅ | `sport-challenge-dev` (le même) | `sport-challenge-dev.ca.auth0.com` | `https://api.dev.sport-challenge` | *à définir* |

**État** : le tenant est configuré (connexion par code testée) et vérifié conforme le 2026-11-19 : accès utilisateur de la SPA (étape 2.6) et consentement. L'API et les jobs de production l'utilisent déjà (`AUTH0_DOMAIN` et `AUTH0_AUDIENCE` de Cloud Run, vérifiés le 2026-11-19). L'URL du frontend de production s'ajoutera à la SPA quand elle sera connue.

**Conséquences du tenant unique** :
- Les utilisateurs de test et ceux de production sont dans le même tenant : un compte créé en local existe aussi en production, et la suppression d'un compte chez Auth0 vaut pour les deux.
- La SPA `Sport Challenge Web` autorise à la fois `localhost` et l'URL de production (URL de rappel, de déconnexion et origines). Si un tenant de production est créé un jour, retirer `localhost` de sa SPA, ou séparer les deux.
- Le fournisseur d'e-mail (étape 4) et les protections (étape 6) sont ceux de la production : les codes envoyés pendant les tests passent par eux et comptent dans leurs limites.
- L'application machine à machine (étape 5) est la même en local et en production : le `.env` local porte les identifiants de production, et une rotation du secret touche les deux.
- L'audience garde son nom `https://api.dev.sport-challenge` en production : c'est un identifiant, il ne se renomme pas.

Le domaine exact est affiché dans les réglages d'une application (**Applications → Applications → `Sport Challenge Web` → Settings → Domain**) : reporte-le ici s'il diffère de `<tenant>.ca.auth0.com`. Le `ca` du domaine (ou de l'adresse du dashboard, `manage.auth0.com/dashboard/ca/…`) confirme la région Canada. L'audience est un identifiant (une URI), pas une adresse appelée : elle ne se change pas après la création de l'API. Le port du frontend local (`5173`) est celui de Vite par défaut : adapte-le au dépôt du frontend.

## 1. Créer le tenant

1. Auth0 Dashboard → menu du tenant (en haut à gauche) → **Create tenant**.
2. **Tenant Domain** : le nom du tableau ci-dessus ; **Region** : **Canada** ; **Environment Tag** : *Development* ou *Production*.
3. **Settings → General** : nom affiché `Sport Challenge`, langues `fr` et `en` (français par défaut), adresse de support.
4. **Settings → Advanced** : **Log Retention** : la durée par défaut du forfait. Les logs Auth0 contiennent des données personnelles (adresse IP, e-mail) : ne pas l'allonger sans raison (Loi 25).
5. **Applications → Applications** : supprimer la **Default App** créée avec le tenant.

## 2. Créer l'API (audience)

1. **Applications → APIs → Create API**.
2. **Name** : `Sport Challenge API` ; **Identifier** : l'audience du tableau ; **JSON Web Token (JWT) Profile** : *Auth0* ; **Signing Algorithm** : **RS256** (l'API refuse tout autre algorithme).
3. Onglet **Settings** :
   - **Maximum Access Token Lifetime** : `900` (15 min). Un jeton reste valide jusqu'à son expiration, même après une suppression de compte ou une exclusion : la rotation des refresh tokens rend une durée courte sans effet pour l'utilisateur.
   - **Implicit / Hybrid Flow Access Token Lifetime** : `900` (sans effet puisque le grant *Implicit* est désactivé, mais aligné).
   - **Allow Offline Access** : activé (nécessaire aux refresh tokens).
   - **Enable RBAC** : désactivé (l'autorisation se fait dans les services de l'API, pas dans le jeton).
   - **Allow Skipping User Consent** : activé. Sport Challenge Web est une application interne (*first-party*) : l'utilisateur n'a pas à autoriser l'accès à sa propre API. En local, l'écran de consentement reste affiché malgré ce réglage : Auth0 ne le saute jamais pour une application servie depuis `localhost`, faute de pouvoir vérifier son origine. Il suffit de l'accepter une fois par utilisateur de test.
4. Aucune permission (*scope*) à créer pour l'instant.
5. **Supprimer l'application de test** : Auth0 crée en général, avec l'API, une application machine à machine `Sport Challenge API (Test Application)` autorisée sur cette audience. Avec son secret, n'importe qui obtiendrait un jeton valide pour l'API (`sub` en `…@clients`). La supprimer (**Applications → Applications**), puis vérifier dans l'onglet **Machine To Machine Applications** de l'API qu'**aucune** application n'est autorisée.
6. **Autoriser la SPA en accès utilisateur (obligatoire)**. Sans ce réglage, la connexion du frontend échoue avec `Client "<Client ID de la SPA>" is not authorized to access resource server "<audience>"`. À faire une fois la SPA créée (étape 3) :
   - **Applications → APIs → Sport Challenge API → Settings → Application Access Policy → User-Delegated Access** : **Per-app authorization**. Seules les applications autorisées une à une obtiennent alors un jeton utilisateur pour l'API.
   - Onglet **Application Access** → **Edit** sur `Sport Challenge Web` → **User-Delegated Access** : **Grant Access**, sans aucune permission (l'API n'en définit pas).
   - Aucune autre application n'y a d'accès, ni utilisateur ni machine à machine (étape 2.5).

## 3. Créer l'application web (SPA)

1. **Applications → Applications → Create Application** → **Single Page Web Applications**, nom `Sport Challenge Web`.
2. Onglet **Settings**, avec chaque URL du frontend (`<front>`) : `http://localhost:5173` et l'URL de production, séparées par des virgules dans chaque champ (tenant unique) :
   - **Allowed Callback URLs** : `<front>/callback`
   - **Allowed Logout URLs** : `<front>`
   - **Allowed Web Origins** : `<front>`
   - **Allowed Origins (CORS)** : `<front>`
   - Jamais de joker (`*`) ; `http://localhost` est accepté tant que le tenant est partagé avec le développement local.
3. **Refresh Token Rotation** :
   - **Rotation** : activée ; **Reuse Interval** : `0`.
   - **Absolute Expiration** : activée, `2592000` (30 jours).
   - **Inactivity Expiration** : activée, `604800` (7 jours).
4. **Advanced Settings → Grant Types** : seulement **Authorization Code** et **Refresh Token** (décocher *Implicit*).
   **Cross-Origin Authentication** : désactivée (inutile avec Universal Login).
   **ID Token Expiration** : `3600` (1 h). L'API refuse de toute façon les ID tokens : leur audience est le Client ID, pas `AUTH0_AUDIENCE`.
5. Pas de méthode d'authentification à régler : le type *Single Page Application* l'impose à *None* (une SPA ne garde pas de secret ; le flux utilise PKCE), et il n'y a pas d'onglet **Credentials**.
6. Onglet **Connections** : **désactiver** `google-oauth2` (créée par défaut par Auth0) et toute autre connexion. La connexion `email` (code) n'apparaît ici qu'une fois créée dans le tenant : revenir l'activer après l'étape 4.
7. Noter le **Client ID** : il n'est pas secret, il va dans la configuration du frontend.
8. À transmettre au frontend : garder les jetons **en mémoire** (`cacheLocation: 'memory'` du SDK Auth0, jamais `localStorage`, à cause du risque XSS) et utiliser les refresh tokens (`useRefreshTokens: true`). La déconnexion par redirection (`/v2/logout`) ne révoque pas le refresh token : le révoquer explicitement à la déconnexion.
9. Revenir à l'étape 2.6 : autoriser la SPA en accès utilisateur sur l'API.

## 4. Activer la connexion par code

**Désactiver les connexions sociales créées par défaut** : `google-oauth2` est créée automatiquement avec chaque tenant. La désactiver pour `Sport Challenge Web` (étape 3.6), ou la supprimer si le dashboard le permet.

**Supprimer la connexion par mot de passe** : **Authentication → Database → Username-Password-Authentication** → supprimer la connexion. La désactiver seulement pour la SPA la laisserait disponible pour toute future application.

**Code par e-mail** (**Authentication → Passwordless → Email**) :
- Activer la connexion. Onglet **Settings** :
  - **OTP Expiry** : `300` (5 min) ; **OTP Length** : `6`.
  - **Disable Sign Ups** : désactivé (l'inscription se fait à la première connexion ; l'API crée l'utilisateur local au premier login).
  - **Promote Connection to Domain Level** : désactivé (sinon la connexion est ouverte à toutes les applications du tenant, y compris tierces).
  - **Authentication Parameters** : vide.
  - **From**, **Subject**, **Body** : ignorés tant que le fournisseur d'e-mail d'Auth0 est utilisé (avertissement affiché) ; les personnaliser en français et en anglais après avoir configuré un fournisseur d'e-mail (production).
- Onglet **Applications** de la connexion (ou onglet **Connections** de la SPA) : l'activer pour `Sport Challenge Web`.
- **Authentication → Authentication Profile** : choisir **Identifier First** (l'écran de connexion demande d'abord l'e-mail, puis envoie le code). Sans ce mode, la connexion par code ne fonctionne pas avec le New Universal Login.
- Vérifier avec le bouton **Try** que la connexion par code fonctionne (il exige les deux réglages ci-dessus : *Identifier First* et au moins une application assignée).
- Avant la mise en production, configurer un fournisseur d'e-mail (**Branding → Email Provider**) sur le tenant, partagé avec le développement local : le fournisseur de test d'Auth0 est limité et ne convient qu'au développement.

**Universal Login** (**Branding → Universal Login**) : expérience *New*, langues `fr` et `en`.

## 5. Créer l'application machine à machine (API de gestion)

Elle sert uniquement à supprimer un utilisateur chez Auth0 quand il supprime son compte (ticket SC-API-039).

1. **Applications → Applications → Create Application** → **Machine to Machine Applications**, nom `Sport Challenge API (management)`.
2. API autorisée : **Auth0 Management API**, avec **une seule** permission : `delete:users`. Ne rien cocher d'autre.
3. Onglet **Connections** : aucune connexion activée. **Advanced Settings → Grant Types** : seulement **Client Credentials**.
4. Onglet **Credentials** : **Client Secret (Post)**, la seule méthode que l'API prend en charge (Private Key JWT serait un changement du client, `internal/platform/auth/management.go`). Noter le **Client ID** et le **Client Secret**.
5. Ranger le **Client Secret** dans Secret Manager (secret `auth0-mgmt-client-secret-ca`, réplication gérée à Montréal seulement, `docs/deploiement.md`), jamais dans un fichier du dépôt, un ticket ou un message. Le **Client ID** est, en production, une simple variable d'environnement (`AUTH0_MGMT_CLIENT_ID`) de l'API et des jobs : seul, il ne permet pas d'obtenir de jeton.

   Le compte de service qui exécute l'API et les jobs (`sc-run`) lit le secret avec `roles/secretmanager.secretAccessor`. Le rôle lui est donné sur ce secret et sur `database-url-ca` seulement, pas sur le projet (SC-API-102). L'API lit le secret dans la variable `AUTH0_MGMT_CLIENT_SECRET` (injectée depuis Secret Manager) et le Client ID dans `AUTH0_MGMT_CLIENT_ID` (configuration de Cloud Run) : les deux sont obligatoires dans tous les environnements (le démarrage échoue sans elles) ; en développement local, elles vont seulement dans `.env` (ignoré par git), avec la même application machine à machine que la production (tenant unique).
6. Si le secret a été exposé : **Credentials → Rotate** dans Auth0, nouvelle version dans Secret Manager, puis **désactivation** de l'ancienne version.

## 6. Protections du tenant

**Security → Attack Protection** :
- **Bot Detection** : activé si le forfait le permet.
- **Suspicious IP Throttling** : activé.
- **Brute-force Protection** : activé.
- Vérifier que ces limites couvrent l'envoi des courriels passwordless (pour empêcher d'inonder une adresse de liens de connexion).

## 7. Domaine personnalisé (production)

**À faire lors de la mise en place du déploiement**, une fois le domaine du frontend connu : ajouter un *Custom Domain* Auth0 (**Branding → Custom Domains**), par exemple `login.<domaine du frontend>`, avec le certificat géré par Auth0. Vérifier d'abord que le forfait le permet.

**Raison** : les jetons sont gardés en mémoire (étape 3.8), donc perdus à chaque rechargement de la page. Le SDK rétablit alors la session sans afficher de page, avec un iframe vers le domaine Auth0 qui lit le cookie de session d'Auth0. Sur un domaine `*.auth0.com`, ce cookie est tiers. Safari et les navigateurs qui bloquent les cookies tiers ne l'envoient pas, et l'utilisateur repasse par la page de connexion Auth0 à chaque rechargement. Avec un domaine personnalisé sur le même site que le frontend, le cookie n'est plus tiers.

**Réglage de l'API** : ce changement modifie l'émetteur des jetons, et `AUTH0_DOMAIN` doit alors être le domaine personnalisé. L'API de gestion, elle, reste sur le domaine du tenant : régler en même temps `AUTH0_MGMT_DOMAIN` sur le domaine du tenant (`sport-challenge-dev.ca.auth0.com`), dans la configuration de l'API et des jobs du worker (Cloud Run). Sans elle, `AUTH0_MGMT_DOMAIN` vaut `AUTH0_DOMAIN`, et la suppression de compte chez Auth0 échouerait. Voir « Domaine personnalisé » ci-dessous.

En développement local, le frontend et l'API gardent le domaine du tenant (les jetons y ont l'émetteur du tenant) : on accepte de repasser par la page Auth0 à chaque rechargement.

## Points d'attention pour l'API (ticket SC-API-017)

- **Jetons machine à machine** : refuser un access token dont `gty` vaut `client-credentials` ou dont `sub` se termine par `@clients`, en défense en profondeur de l'étape 2.5.
- **Domaine personnalisé** (étape 7, prévu en production) : l'émetteur des jetons devient `https://<custom-domain>/`, et `AUTH0_DOMAIN` doit être ce domaine. L'API de gestion, elle, garde l'adresse et l'audience du domaine du tenant (`https://<tenant>.ca.auth0.com/api/v2/`) : le client de suppression (ticket SC-API-039) les construit depuis `AUTH0_MGMT_DOMAIN` (SC-API-114), qui vaut `AUTH0_DOMAIN` si elle est absente. Elle doit donc être réglée sur le domaine du tenant **en même temps** que `AUTH0_DOMAIN` passe au domaine personnalisé. La validation des jetons (émetteur, JWKS) reste sur `AUTH0_DOMAIN`.

## Valeurs à reporter

Pour le tenant, une fois la procédure terminée :

| Valeur | Secrète | Destination |
|---|---|---|
| `AUTH0_DOMAIN` | non | configuration de l'API (Cloud Run) et du frontend |
| `AUTH0_AUDIENCE` | non | configuration de l'API (Cloud Run) et du frontend |
| `AUTH0_MGMT_DOMAIN` | non | configuration de l'API et des jobs (Cloud Run), seulement avec un domaine personnalisé : le domaine du tenant, `sport-challenge-dev.ca.auth0.com` (étape 7). Absente, elle vaut `AUTH0_DOMAIN` |
| Client ID de la SPA | non | configuration du frontend |
| Client ID de l'application machine à machine | non (seul, il ne donne aucun accès) | configuration de l'API et des jobs (Cloud Run), variable `AUTH0_MGMT_CLIENT_ID` |
| Client Secret de l'application machine à machine | **oui** | Secret Manager : `auth0-mgmt-client-secret-ca`, lu par l'API dans `AUTH0_MGMT_CLIENT_SECRET` |

`AUTH0_DOMAIN` et `AUTH0_AUDIENCE` seront ajoutées à la configuration de l'API et à `.env.example` par le ticket SC-API-017.

## Vérification

Pour le tenant :

- [ ] Tenant en région Canada (`ca` dans le domaine de l'application ou dans l'adresse du dashboard).
- [ ] API en RS256, durée de vie des access tokens à 15 min, accès hors ligne activé, *Allow Skipping User Consent* activé.
- [ ] API en *Per-app authorization* ; `Sport Challenge Web` seule autorisée en accès utilisateur, sans permission (étape 2.6).
- [ ] Application de test de l'API et Default App supprimées ; aucune application machine à machine autorisée sur l'API.
- [ ] SPA : URL de rappel, de déconnexion et origines du local et de la production, sans joker ; rotation des refresh tokens activée ; grant *Implicit* décoché.
- [ ] Profil d'authentification en *Identifier First* ; seule la connexion par code e-mail est activée sur la SPA ; `google-oauth2` désactivée ; connexion par mot de passe supprimée.
- [ ] Application machine à machine limitée à `delete:users`, grant *Client Credentials* seul, aucune connexion ; identifiants dans Secret Manager, accès réservé au compte de service de l'API.
- [ ] Connexion de test réussie depuis le frontend (ou **Authentication → … → Try** dans le dashboard).
- [ ] Production : domaine personnalisé Auth0 actif, `AUTH0_DOMAIN` réglé sur ce domaine et `AUTH0_MGMT_DOMAIN` sur le domaine du tenant (étape 7) ; suppression de compte testée ensuite.

## V2 : application native

À faire quand le mobile démarre :

1. **Create Application** → **Native**, nom `Sport Challenge Mobile`.
2. **Allowed Callback URLs** et **Allowed Logout URLs** : `<bundle-id>://<AUTH0_DOMAIN>/ios/<bundle-id>/callback` et `<bundle-id>://<AUTH0_DOMAIN>/android/<package>/callback` (format des SDK Auth0).
3. Rotation des refresh tokens activée, mêmes durées que la SPA.
4. Connexions : les mêmes que la SPA. Sign in with Apple est **obligatoire** sur iOS dès qu'une connexion sociale est proposée.

## Plus tard : connexions sociales

Reportées après la V1. Selon la version du dashboard, les connexions sociales se trouvent dans **Authentication → Social**, ou via la recherche (`Social`, `Google`, `Apple`), ou depuis l'onglet **Connections** de l'application. Ajouter alors la question de la liaison de comptes (même e-mail, plusieurs `sub`) avant de les activer.

**Google** (**Authentication → Social → Google / Gmail**) :
- En développement, les clés de test d'Auth0 suffisent.
- En production, créer un client OAuth dans Google Cloud (**APIs & Services → Credentials → OAuth client ID**, type *Web application*, URI de redirection `https://<AUTH0_DOMAIN>/login/callback`) et saisir son identifiant et son secret dans Auth0. Le secret Google reste dans Auth0 : il ne passe jamais par le dépôt ni par l'API.
- Attributs : `email`, `profile`.

**Apple** (**Authentication → Social → Apple**), compte Apple Developer requis :
- Créer un *Services ID* (identifiant web), une clé *Sign in with Apple* (fichier `.p8`) et noter le *Team ID* et le *Key ID*.
- **Return URL** côté Apple : `https://<AUTH0_DOMAIN>/login/callback`.
- Saisir dans Auth0 le Services ID, le Team ID, le Key ID et le contenu de la clé. La clé reste dans Auth0.
- L'activer ensuite pour `Sport Challenge Web` (onglet **Applications** de la connexion).
