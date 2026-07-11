package bootstrap

import (
	"embed"
	"sort"
)

// embeddedMigrations carries the Postgres schema so `trustpanel bootstrap` is a
// self-contained installer (no separate .sql files to ship). The copies under
// assets/ are kept in sync with migrations/pg/ by TestEmbeddedMigrationsMatchDisk.
//
//go:embed assets/*.sql
var embeddedMigrations embed.FS

// embeddedUnits carries the entry-node systemd templates that the panel uploads
// when provisioning entries. Kept in sync with deploy/systemd/ by the drift test.
//
//go:embed assets/units/*.service
var embeddedUnits embed.FS

// EntryUnitTemplates returns the entry systemd unit files (name -> content) that
// bootstrap stages into the provisioning units dir.
func EntryUnitTemplates() map[string][]byte {
	entries, err := embeddedUnits.ReadDir("assets/units")
	if err != nil {
		panic("bootstrap: read embedded units: " + err.Error())
	}
	out := map[string][]byte{}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		b, err := embeddedUnits.ReadFile("assets/units/" + e.Name())
		if err != nil {
			panic("bootstrap: read embedded unit " + e.Name() + ": " + err.Error())
		}
		out[e.Name()] = b
	}
	return out
}

// Migration is one ordered schema file.
type Migration struct {
	Name string
	SQL  string
}

// Migrations returns the embedded migrations sorted by filename.
func Migrations() []Migration {
	entries, err := embeddedMigrations.ReadDir("assets")
	if err != nil {
		panic("bootstrap: read embedded migrations: " + err.Error())
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	out := make([]Migration, 0, len(names))
	for _, n := range names {
		b, err := embeddedMigrations.ReadFile("assets/" + n)
		if err != nil {
			panic("bootstrap: read embedded migration " + n + ": " + err.Error())
		}
		out = append(out, Migration{Name: n, SQL: string(b)})
	}
	return out
}
