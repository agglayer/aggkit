package db

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
