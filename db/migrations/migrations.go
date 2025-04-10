package migrations

import (
	_ "embed"

	"github.com/agglayer/aggkit/db/types"
)

//go:embed basedb0001.sql
var mig001 string

func GetBaseMigrations() []types.Migration {
	return []types.Migration{
		{
			ID:  "basedb0001",
			SQL: mig001,
		},
	}
}
