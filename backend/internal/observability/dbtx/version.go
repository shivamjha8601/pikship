package dbtx

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// RequiredSchemaVersion is the migration version this binary expects.
// Bumped manually with each release that adds migrations. Keep in sync
// with the highest-numbered file under /migrations.
const RequiredSchemaVersion = 23

// CheckSchemaVersion errors if the connected DB is older than required
// or if a migration is in the dirty state. Called from main on startup;
// the binary refuses to serve traffic on mismatch.
func CheckSchemaVersion(ctx context.Context, pool *pgxpool.Pool) error {
	var version int
	var dirty bool
	err := pool.QueryRow(ctx, "SELECT version, dirty FROM schema_migrations").Scan(&version, &dirty)
	if err != nil {
		// Table missing means migrations haven't been run; treat as a hard fail
		// so devs notice early rather than have the app come up half-broken.
		if errors.Is(err, pgx.ErrNoRows) {
			return errors.New("schema_migrations table empty: run `make migrate-up`")
		}
		return fmt.Errorf("query schema_migrations: %w", err)
	}
	if dirty {
		return fmt.Errorf("schema dirty at version %d; manual cleanup required", version)
	}
	if version < RequiredSchemaVersion {
		return fmt.Errorf("schema at version %d, binary requires %d", version, RequiredSchemaVersion)
	}
	return nil
}
