package types

import (
	"context"
	"database/sql"
)

// Querier defines an interface for executing SQL queries and commands.
// It abstracts the basic operations for interacting with a SQL database,
// including executing statements, querying multiple rows, and querying a single row.
// Implementations of this interface can be used to generalize database access logic.
type Querier interface {
	Exec(query string, args ...interface{}) (sql.Result, error)
	Query(query string, args ...interface{}) (*sql.Rows, error)
	QueryRow(query string, args ...interface{}) *sql.Row
}

// DBer defines the interface for database operations, embedding the Querier interface
// and providing a method to begin a new transaction with context and transaction options.
type DBer interface {
	Querier
	BeginTx(ctx context.Context, opts *sql.TxOptions) (*sql.Tx, error)
}

// SQLTxer represents a database transaction interface that extends the Querier interface.
// It provides methods to commit or rollback a transaction.
type SQLTxer interface {
	Querier
	Commit() error
	Rollback() error
}

// Txer extends the SQLTxer interface by providing methods to register
// callbacks that are executed upon transaction rollback or commit.
// AddRollbackCallback registers a function to be called if the transaction
// is rolled back, while AddCommitCallback registers a function to be called
// upon successful commit of the transaction.
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
