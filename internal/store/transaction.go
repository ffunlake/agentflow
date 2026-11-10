package store

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"time"
)

// transactionQuery is shared by sql.Tx and the explicitly owned transaction on
// a pinned sql.Conn. Helpers do not acquire connections or end transactions.
type transactionQuery interface {
	queryRower
	journalWriter
}

// immediateTx owns both the SQL transaction and its physical connection. A
// sql.Tx cannot roll back after Commit fails: database/sql has marked it done,
// even if SQLite still has an open transaction. Explicit SQL preserves control
// of cleanup, and BEGIN IMMEDIATE matches the Store's _txlock setting.
type immediateTx struct {
	*sql.Conn
	committed    bool
	commitFailed bool
}

func (s *Store) beginImmediate(ctx context.Context) (*immediateTx, error) {
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return nil, err
	}
	tx := &immediateTx{Conn: conn}
	if _, err := conn.ExecContext(ctx, `BEGIN IMMEDIATE`); err != nil {
		tx.discard()
		_ = conn.Close()
		return nil, err
	}
	return tx, nil
}

func (tx *immediateTx) Commit(ctx context.Context) error {
	_, err := tx.ExecContext(ctx, `COMMIT`)
	tx.committed, tx.commitFailed = err == nil, err != nil
	return err
}

func (tx *immediateTx) discard() {
	// Conn.Close alone returns the underlying driver connection to the pool.
	_ = tx.Raw(func(any) error { return driver.ErrBadConn })
}

func (tx *immediateTx) finish(resultErr *error) {
	defer tx.Close()
	if tx.committed {
		return
	}
	// Cancellation of the request must not suppress cleanup. Its own deadline
	// bounds a stalled rollback; any uncertain connection is physically closed.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, rollbackErr := tx.ExecContext(ctx, `ROLLBACK`)
	if tx.commitFailed || rollbackErr != nil {
		tx.discard()
	}
	if rollbackErr != nil {
		*resultErr = errors.Join(*resultErr, fmt.Errorf("transaction rollback: %w", rollbackErr))
	}
	// A commit error does not establish that the transaction was unapplied.
	// Recovery must consult durable receipts before retrying any external action.
}
