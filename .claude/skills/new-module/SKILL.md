---
name: new-module
description: Crée un nouveau module métier dans internal/ du backend Sport Challenge (handler, service, repository, modèle, tests, câblage). À utiliser quand on dit « ajoute un module », « crée le module X », « nouveau domaine ».
---

# Créer un module `internal/<nom>`

Avant de commencer : vérifie dans le `README.md` (§5.2, §5.3) que le module existe dans l'architecture ; sinon, demande avant de l'ajouter.

## Fichiers

Structure complète d'un module, **à créer fichier par fichier, seulement quand le ticket en cours en a besoin** (pas de fichier vide ni de méthode inutilisée) :

```
internal/<nom>/
├── model.go            entités (tags GORM pour le mapping, pas pour le schéma)
├── service.go          règles métier + interface Repository (définie ICI, côté consommateur)
├── repository.go       implémentation GORM ou SQL, seul accès à la base
├── handler.go          routes Gin, fin : parse, appelle le service, répond
├── rules.go            (si utile) validation pure, sans dépendance
├── policy.go           (si utile) qui peut faire quoi : fonctions pures, testées sans base
└── service_test.go     tests unitaires avec un faux repository
```

## Étapes

1. **Contrat** : ajoute les routes à `api/openapi.yaml` (voir le skill `new-endpoint`).
2. **Schéma** : s'il y a de nouvelles tables, crée la migration (voir le skill `new-migration`).
3. **Modèle et règles** : `model.go`, puis `rules.go` avec ses tests.
4. **Service** : définis l'interface `Repository` dans `service.go` avec seulement les méthodes dont le service a besoin. Le temps vient de `clock.Clock` injecté, jamais de `time.Now()`.
5. **Repository** : implémente l'interface. Contexte en premier paramètre ; transactions via le helper de `platform/db` quand plusieurs écritures vont ensemble ; pas de `Preload` en boucle.
6. **Handler** : méthode `Register(r *gin.RouterGroup)` ; lit la requête, appelle le service, traduit les erreurs métier en réponses (format `{ "error": { "code", "message" } }`).
7. **Câblage** : instancie repository → service → handler dans `cmd/api/main.go` (et dans `cmd/worker/main.go` si une tâche en a besoin).
8. **Tests** : unitaires (table-driven) ; test d'intégration sur le repository si le SQL est non trivial.

## Règles

- Un module n'importe jamais le repository d'un autre module : déclare une petite interface côté consommateur et passe le service de l'autre module à la place.
- Aucune règle métier dans le handler.
- Chaque route vérifie l'autorisation dans le service.

## Fin

`make fmt && make lint && make test`, puis agent `go-reviewer` (et `security-reviewer` si des routes sont exposées).
