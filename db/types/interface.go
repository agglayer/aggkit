package types

import (
	"context"
	"database/sql"
)

type Querier interface {
	Exec(query string, args ...interface{}) (sql.Result, error)
	Query(query string, args ...interface{}) (*sql.Rows, error)
	QueryRow(query string, args ...interface{}) *sql.Row
}

type DBer interface {
	Querier
	BeginTx(ctx context.Context, opts *sql.TxOptions) (*sql.Tx, error)
}

type SQLTxer interface {
	Querier
	Commit() error
	Rollback() error
}

type Txer interface {
	SQLTxer
	AddRollbackCallback(cb func())
	AddCommitCallback(cb func())
}

// KeyValueStorager is the interface that defines the methods to interact with the storage as a key/value
type KeyValueStorager interface {
	// InsertValue inserts the value of the key in the storage
	InsertValue(tx Querier, owner, key, value string) error
	// GetValue returns the value of the key from the storage
	GetValue(tx Querier, owner, key string) (string, error)
	// UpdateValue updates the value of the key in the storage
	UpdateValue(tx Querier, owner, key, value string) error
}
