package db

import (
	"context"
	"fmt"

	"gorm.io/gorm"
)

// WithTx runs fn in a transaction: committed if fn returns nil, rolled back
// if it returns an error or panics (the panic then goes on). Every write of
// fn must use the *gorm.DB it receives.
func WithTx(ctx context.Context, gdb *gorm.DB, fn func(tx *gorm.DB) error) error {
	if err := gdb.WithContext(ctx).Transaction(fn); err != nil {
		return fmt.Errorf("transaction: %w", err)
	}
	return nil
}
