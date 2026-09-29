package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/zerlinpi/Ant-Browser/server/platform/postgres"
)

const usage = "usage: migrate [up | preflight [-fix]]"

func main() {
	command, args := "up", []string(nil)
	if len(os.Args) > 1 {
		command, args = os.Args[1], os.Args[2:]
	}
	switch command {
	case "up":
		os.Exit(runUp())
	case "preflight":
		databaseURL, ok := migrationDatabaseURL()
		if !ok {
			os.Exit(2)
		}
		os.Exit(runPreflight(context.Background(), databaseURL, args, os.Stdout, os.Stderr))
	default:
		fmt.Fprintln(os.Stderr, "only the forward migration command 'up' and the read-only check 'preflight' are supported")
		fmt.Fprintln(os.Stderr, usage)
		os.Exit(2)
	}
}

func runUp() int {
	databaseURL, ok := migrationDatabaseURL()
	if !ok {
		return 2
	}
	migrationsPath := firstEnvironment("ANT_MIGRATIONS_PATH", "MIGRATIONS_PATH")
	if migrationsPath == "" {
		migrationsPath = discoverMigrationsPath()
	}
	if err := postgres.Migrate(context.Background(), databaseURL, migrationsPath); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	fmt.Printf("migrations applied from %s\n", migrationsPath)
	return 0
}

// migrationDatabaseURL is shared by up and preflight: both need the
// migration role, which owns the tables.
func migrationDatabaseURL() (string, bool) {
	databaseURL := firstEnvironment("ANT_MIGRATION_DATABASE_URL", "ANT_DATABASE_URL", "DATABASE_URL")
	if databaseURL == "" {
		fmt.Fprintln(os.Stderr, "ANT_MIGRATION_DATABASE_URL is required (use the migration role)")
		return "", false
	}
	return databaseURL, true
}

func firstEnvironment(names ...string) string {
	for _, name := range names {
		if value := strings.TrimSpace(os.Getenv(name)); value != "" {
			return value
		}
	}
	return ""
}

func discoverMigrationsPath() string {
	candidates := []string{
		filepath.Join("database", "migrations"),
		filepath.Join("..", "database", "migrations"),
		filepath.Join("app", "database", "migrations"),
		filepath.Join(string(filepath.Separator), "app", "database", "migrations"),
	}
	for _, candidate := range candidates {
		if info, err := os.Stat(candidate); err == nil && info.IsDir() {
			return candidate
		}
	}
	return filepath.Join("database", "migrations")
}
