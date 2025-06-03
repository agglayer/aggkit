package db

import (
	"context"

	"github.com/agglayer/aggkit/db/types"
)

type Tx struct {
	types.SQLTxer
	rollbackCallbacks []func()
	commitCallbacks   []func()
}

func NewTx(ctx context.Context, db types.DBer) (types.Txer, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	return &Tx{
		SQLTxer: tx,
	}, nil
}

func (s *Tx) AddRollbackCallback(cb func()) {
	s.rollbackCallbacks = append(s.rollbackCallbacks, cb)
}
func (s *Tx) AddCommitCallback(cb func()) {
	s.commitCallbacks = append(s.commitCallbacks, cb)
}

func (s *Tx) Commit() error {
	if err := s.SQLTxer.Commit(); err != nil {
		return err
	}
	for _, cb := range s.commitCallbacks {
		cb()
	}
	return nil
}

func (s *Tx) Rollback() error {
	if err := s.SQLTxer.Rollback(); err != nil {
		return err
	}
	for _, cb := range s.rollbackCallbacks {
		cb()
	}
	return nil
}
