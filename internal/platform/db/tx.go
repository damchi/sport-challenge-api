package db

import (
	"context"
	"fmt"

	"gorm.io/gorm"
)

// txKey keys the transaction InTx puts in a context.
type txKey struct{}

// WithTx runs fn in a transaction: committed if fn returns nil, rolled back
// if it returns an error or panics (the panic then goes on). Every write of
// fn must use the *gorm.DB it receives. Inside the transaction of InTx (ctx
// carries it), it is a savepoint of that transaction: its writes commit with
// it, and an error of fn rolls back only them.
func WithTx(ctx context.Context, gdb *gorm.DB, fn func(tx *gorm.DB) error) error {
	if err := Conn(ctx, gdb).Transaction(fn); err != nil {
		return fmt.Errorf("transaction: %w", err)
	}
	return nil
}

// InTx runs fn in a transaction carried by the context fn receives, so that
// the use cases of several modules write in one transaction: each
// repository reads its connection with Conn. Committed if fn returns nil,
// rolled back otherwise.
func InTx(ctx context.Context, gdb *gorm.DB, fn func(ctx context.Context) error) error {
	return WithTx(ctx, gdb, func(tx *gorm.DB) error {
		return fn(context.WithValue(ctx, txKey{}, tx))
	})
}

// Conn returns the transaction of InTx that ctx carries, or gdb outside of
// one, bound to ctx.
func Conn(ctx context.Context, gdb *gorm.DB) *gorm.DB {
	if tx, ok := ctx.Value(txKey{}).(*gorm.DB); ok {
		return tx.WithContext(ctx)
	}
	return gdb.WithContext(ctx)
}
