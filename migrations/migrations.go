// Package migrations embeds the SQL migrations in the binaries that apply them
// (cmd/migrate), so that the schema always matches the version of the code.
// Its integration tests check the business rules the migrations enforce with
// constraints, and their reversibility. golang-migrate reads the *.up.sql and
// *.down.sql files and ignores Go files.
package migrations

import "embed"

// Files holds every *.up.sql and *.down.sql file, at the root.
//
//go:embed *.sql
var Files embed.FS
