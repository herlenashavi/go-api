package dbx

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// WithTx runs fn inside a transaction on pool: commits iff fn returns nil,
// rolls back otherwise — including on panic, where the panic is re-raised
// after rollback so an outer Recover middleware still sees and reports it.
//
// Call this explicitly wherever a sequence of statements needs atomicity
// (a repository's multi-step create, a saga step). Most single-statement
// operations don't need it at all — Postgres already makes one statement
// atomic — so reach for WithTx only when you actually have more than one
// write that must succeed or fail together.
func WithTx(ctx context.Context, pool *Pool, fn func(ctx context.Context, tx pgx.Tx) error) (err error) {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("dbx: begin transaction: %w", err)
	}

	defer func() {
		if p := recover(); p != nil {
			_ = tx.Rollback(ctx)
			panic(p)
		}
	}()

	if fnErr := fn(ctx, tx); fnErr != nil {
		if rbErr := tx.Rollback(ctx); rbErr != nil && !errors.Is(rbErr, pgx.ErrTxClosed) {
			return fmt.Errorf("%w (rollback also failed: %v)", fnErr, rbErr)
		}
		return fnErr
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("dbx: commit: %w", err)
	}
	return nil
}
