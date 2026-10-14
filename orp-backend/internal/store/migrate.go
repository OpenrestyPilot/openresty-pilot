package store

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

//go:embed migrations/*.sql
var migrations embed.FS

type migration struct {
	version     int
	versionText string
	description string
	filename    string
	contents    string
}

func Migrate(database *sql.DB) error {
	ctx := context.Background()
	connection, err := database.Conn(ctx)
	if err != nil {
		return fmt.Errorf("get migration connection: %w", err)
	}
	defer connection.Close()

	var lockResult sql.NullInt64
	if err := connection.QueryRowContext(ctx, "SELECT GET_LOCK('orp-schema-migrations', 30)").Scan(&lockResult); err != nil {
		return fmt.Errorf("acquire schema migration lock: %w", err)
	}
	if !lockResult.Valid || lockResult.Int64 != 1 {
		return fmt.Errorf("could not acquire schema migration lock")
	}
	defer connection.ExecContext(ctx, "SELECT RELEASE_LOCK('orp-schema-migrations')")

	if _, err := connection.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS openresty_schema_migration (
		version VARCHAR(50) NOT NULL PRIMARY KEY,
		description VARCHAR(200) NOT NULL,
		script VARCHAR(1000) NOT NULL,
		installed_at TIMESTAMP(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6)
	) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`); err != nil {
		return fmt.Errorf("create schema migration history: %w", err)
	}

	if err := seedFlywayHistory(ctx, connection); err != nil {
		return err
	}

	pending, err := loadMigrations()
	if err != nil {
		return err
	}
	for _, item := range pending {
		var installed bool
		if err := connection.QueryRowContext(ctx,
			"SELECT EXISTS(SELECT 1 FROM openresty_schema_migration WHERE version = ?)", item.versionText,
		).Scan(&installed); err != nil {
			return fmt.Errorf("check migration %s: %w", item.filename, err)
		}
		if installed {
			continue
		}

		if _, err := connection.ExecContext(ctx, item.contents); err != nil {
			return fmt.Errorf("apply migration %s: %w", item.filename, err)
		}
		if _, err := connection.ExecContext(ctx,
			"INSERT INTO openresty_schema_migration (version, description, script, installed_at) VALUES (?, ?, ?, ?)",
			item.versionText, item.description, item.filename, time.Now(),
		); err != nil {
			return fmt.Errorf("record migration %s: %w", item.filename, err)
		}
	}

	return validateSchema(ctx, connection)
}

func seedFlywayHistory(ctx context.Context, connection *sql.Conn) error {
	var exists bool
	if err := connection.QueryRowContext(ctx, `SELECT EXISTS(
		SELECT 1 FROM information_schema.tables
		WHERE table_schema = DATABASE() AND table_name = 'flyway_schema_history'
	)`).Scan(&exists); err != nil {
		return fmt.Errorf("check legacy Flyway history: %w", err)
	}
	if !exists {
		return nil
	}
	_, err := connection.ExecContext(ctx, `INSERT IGNORE INTO openresty_schema_migration (version, description, script, installed_at)
		SELECT version, description, script, installed_on
		FROM flyway_schema_history WHERE success = 1 AND version IS NOT NULL`)
	if err != nil {
		return fmt.Errorf("import legacy Flyway migration history: %w", err)
	}
	return nil
}

func loadMigrations() ([]migration, error) {
	entries, err := fs.ReadDir(migrations, "migrations")
	if err != nil {
		return nil, fmt.Errorf("read embedded migrations: %w", err)
	}
	items := make([]migration, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}
		name := strings.TrimSuffix(entry.Name(), filepath.Ext(entry.Name()))
		parts := strings.SplitN(name, "__", 2)
		if len(parts) != 2 || !strings.HasPrefix(parts[0], "V") {
			return nil, fmt.Errorf("invalid migration filename %q", entry.Name())
		}
		versionText := strings.TrimPrefix(parts[0], "V")
		version, err := strconv.Atoi(versionText)
		if err != nil || version < 1 {
			return nil, fmt.Errorf("invalid migration version in %q", entry.Name())
		}
		contents, err := fs.ReadFile(migrations, "migrations/"+entry.Name())
		if err != nil {
			return nil, fmt.Errorf("read migration %s: %w", entry.Name(), err)
		}
		items = append(items, migration{
			version:     version,
			versionText: versionText,
			description: strings.ReplaceAll(parts[1], "_", " "),
			filename:    entry.Name(),
			contents:    string(contents),
		})
	}
	sort.Slice(items, func(i, j int) bool { return items[i].version < items[j].version })
	for i := 1; i < len(items); i++ {
		if items[i-1].version == items[i].version {
			return nil, fmt.Errorf("duplicate migration version %s", items[i].versionText)
		}
	}
	if len(items) == 0 {
		return nil, fmt.Errorf("no database migrations were embedded")
	}
	return items, nil
}

func validateSchema(ctx context.Context, connection *sql.Conn) error {
	var missing []string
	for _, table := range []string{
		"center", "audit_event", "nginx_node", "http_upstream", "http_upstream_member",
		"ip_policy", "api_policy", "api_policy_rule", "http_server", "http_location",
		"runtime_configuration_version", "stream_upstream", "stream_server",
		"control_api_reload_task", "control_api_reload_node_result", "configuration_dictionary",
		"http_upstream_target", "tls_certificate", "dns_resolver_configuration", "http_configuration",
		"orp_alert_channel", "orp_tls_alert_rule", "orp_tls_alert_rule_channel", "orp_alert_delivery",
		"orp_agent_heartbeat", "orp_agent_task", "orp_agent_token",
	} {
		var exists bool
		if err := connection.QueryRowContext(ctx, `SELECT EXISTS(
			SELECT 1 FROM information_schema.tables
			WHERE table_schema = DATABASE() AND table_name = ?
		)`, table).Scan(&exists); err != nil {
			return fmt.Errorf("validate table %s: %w", table, err)
		}
		if !exists {
			missing = append(missing, table)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("database schema is incomplete after migrations; missing tables: %s", strings.Join(missing, ", "))
	}
	return nil
}
