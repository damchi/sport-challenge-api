---
name: new-migration
description: Crée ou modifie le schéma PostgreSQL du backend Sport Challenge avec golang-migrate. À utiliser quand on dit « ajoute une table », « ajoute une colonne », « crée une migration », « change le schéma », « ajoute un index ».
---

# Migrations avec golang-migrate

Le schéma vient **uniquement** des migrations. Jamais `AutoMigrate`. Les tags GORM décrivent le mapping, pas le schéma.

## Créer une migration

```
make migrate-new NAME=create_challenge_tables
# équivaut à : migrate create -ext sql -dir migrations -seq create_challenge_tables
```

Cela crée `migrations/NNNNNN_create_challenge_tables.up.sql` et `.down.sql`. Remplis **les deux**.

## Règles

- **Ne modifie jamais** une migration déjà appliquée ou déjà fusionnée : crée-en une nouvelle.
- Un changement cohérent par migration ; nom en `snake_case` (`add_activity_idempotency_key`).
- `down` : annule exactement le `up`, dans l'ordre inverse.
- Types : `timestamptz` pour tous les instants (jamais `timestamp`), `uuid` avec `DEFAULT gen_random_uuid()`, `text`, distances en mètres, durées en secondes, `numeric` ou `bigint` plutôt que `float` pour ce qui est cumulé.
- Contraintes : clés primaires et étrangères, `NOT NULL`, `CHECK` pour les énumérations (visibilité, type de catégorie, statut). Pour `ON DELETE`, pense à l'anonymisation d'un compte : ne supprime pas en cascade les résultats d'un challenge.
- Index du README : `activity (user_id, started_at)`, `participant (challenge_id)`, `category (challenge_id)`. N'en ajoute pas d'autres sans raison de requête.
- `CREATE INDEX CONCURRENTLY` : seul dans son fichier (il ne peut pas être combiné à d'autres instructions dans la même migration).
- Table qui contient déjà des données : évite les verrous longs (colonne `NOT NULL` sans valeur par défaut, réécriture de table). Pour un changement destructif, fais-le en plusieurs migrations (ajouter, migrer les données, retirer) car l'ancienne version du code tourne encore pendant le déploiement.
- Données de référence (catalogue de sports) : idempotentes (`INSERT ... ON CONFLICT DO NOTHING`). Aucune donnée de test ni secret.

## Vérifier

1. `make docker-up`
2. `make migrate-up` → `make migrate-down` → `make migrate-up` (doit passer sans erreur).
3. Adapte les modèles GORM et les repositories ; `make test-integration`.
4. Agent `migration-reviewer`.

## Déploiement

Les migrations sont appliquées par la CI/CD **avant** le déploiement de la nouvelle version (étape dédiée, jamais au démarrage de l'API). En cas de migration « sale » (`dirty`), ne force pas la version sans comprendre la cause : demande à l'utilisateur.
