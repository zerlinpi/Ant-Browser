package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/zerlinpi/Ant-Browser/server/platform/postgres"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] != "up" {
		fmt.Fprintln(os.Stderr, "only forward migration command 'up' is supported")
		os.Exit(2)
	}
	databaseURL := firstEnvironment("ANT_MIGRATION_DATABASE_URL", "ANT_DATABASE_URL", "DATABASE_URL")
	if databaseURL == "" {
		fmt.Fprintln(os.Stderr, "ANT_MIGRATION_DATABASE_URL is required (use the migration role)")
		os.Exit(2)
	}
	migrationsPath := firstEnvironment("ANT_MIGRATIONS_PATH", "MIGRATIONS_PATH")
	if migrationsPath == "" {
		migrationsPath = discoverMigrationsPath()
	}
	if err := postgres.Migrate(context.Background(), databaseURL, migrationsPath); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Printf("migrations applied from %s\n", migrationsPath)
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
