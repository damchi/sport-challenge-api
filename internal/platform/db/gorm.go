package db

import (
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

// slowQueryThreshold marks a query as slow in the logs.
const slowQueryThreshold = 200 * time.Millisecond

// NewGorm returns a GORM handle on pool. The schema only comes from the
// migrations: never call AutoMigrate. Queries are logged through slog, as
// JSON, without their values (they may hold personal data), and only when
// they fail or are slow.
func NewGorm(pool *pgxpool.Pool, logger *slog.Logger) (*gorm.DB, error) {
	gdb, err := gorm.Open(postgres.New(postgres.Config{Conn: stdlib.OpenDBFromPool(pool)}), &gorm.Config{
		Logger: gormlogger.NewSlogLogger(logger, gormlogger.Config{
			SlowThreshold:             slowQueryThreshold,
			LogLevel:                  gormlogger.Warn,
			IgnoreRecordNotFoundError: true,
			ParameterizedQueries:      true,
		}),
	})
	if err != nil {
		return nil, fmt.Errorf("open gorm: %w", err)
	}
	return gdb, nil
}
