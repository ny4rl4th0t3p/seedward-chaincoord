package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/ny4rl4th0t3p/seedward-chaincoord/internal/infrastructure/storage/sqlite"
)

func newMigrateCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "migrate",
		Short: "Run database schema migrations and exit",
		RunE:  runMigrate,
	}
	cmd.Flags().String("db-path", "", "path to SQLite database file (required, or env COORD_DB_PATH)")
	return cmd
}

func runMigrate(cmd *cobra.Command, _ []string) error {
	v := viper.New()
	if err := v.BindPFlag("db_path", cmd.Flags().Lookup("db-path")); err != nil {
		return fmt.Errorf("binding flag: %w", err)
	}
	v.SetEnvPrefix("COORD")
	v.AutomaticEnv()

	dbPath := v.GetString("db_path")
	if dbPath == "" {
		return fmt.Errorf("db-path is required (flag --db-path or env COORD_DB_PATH)")
	}

	db, err := sqlite.Open(dbPath)
	if err != nil {
		return fmt.Errorf("running migrations: %w", err)
	}
	defer db.Close()

	fmt.Println("migrations applied successfully")
	return nil
}
