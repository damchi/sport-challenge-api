---
name: new-endpoint
description: Ajoute une route à l'API du backend Sport Challenge en partant du contrat OpenAPI (spec-first), avec handler fin, autorisation, erreurs à code stable et tests. À utiliser quand on dit « ajoute une route », « nouvel endpoint », « expose X dans l'API ».
---

# Ajouter une route (spec-first)

## Étapes

1. **Vérifie la règle** : relis la règle fonctionnelle concernée dans le `README.md` (§2) et la liste des routes (§5.2). Si une décision est ❓, demande avant d'écrire.
2. **Contrat d'abord** : modifie `api/openapi.yaml` (chemin, méthode, schémas de requête et de réponse, codes d'erreur possibles, sécurité). Puis `make generate` si le code est généré depuis la spec.
3. **Handler fin** : lis la requête, valide la forme, récupère l'utilisateur **depuis le contexte** (jamais depuis un paramètre), appelle le service, renvoie la réponse.
4. **Service** : vérifie l'**autorisation** (créateur, participant, public ; challenge privé = participants et invités seulement), applique les règles métier, renvoie des erreurs `apperr` avec un code stable.
5. **Tests** :
   - service : table-driven avec un faux repository, y compris les cas interdits ;
   - handler : `httptest` (statuts, format d'erreur, authentification absente ou invalide).
6. **Revue** : agents `go-reviewer` et `security-reviewer`, et `spec-guardian` si la route porte une règle métier.

## Conventions

- Préfixe `/api/v1`, ressources au pluriel, identifiants UUID.
- Listes : pagination par curseur (`limit`, `cursor`).
- Créations d'activité : accepter une clé d'idempotence.
- Format d'erreur unique : `{ "error": { "code": "challenge_not_found", "message": "..." } }`. Le `code` est stable (`snake_case`, en anglais) ; le client le traduit.
- Statuts : 200/201/204 en succès ; 400 forme invalide ; 401 non authentifié ; 403 interdit ; 404 introuvable (aussi pour une ressource privée dont l'existence ne doit pas fuiter) ; 409 conflit ; 422 règle métier violée.
- Aucune donnée personnelle ni jeton dans les logs de la route.

## Rappels métier fréquents

- Une activité hors de la fenêtre du challenge est refusée ; après la fin, aucune écriture n'est possible.
- Les catégories ne sont pas modifiables après la création ; titre et description le sont.
- Rejoindre : possible seulement jusqu'à la fin de la première semaine.
