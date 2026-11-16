package multidownloader

import (
	"fmt"

	dbtypes "github.com/agglayer/aggkit/db/types"
	mdrtypes "github.com/agglayer/aggkit/multidownloader/types"
	"github.com/ethereum/go-ethereum/common"
)

// transactionAvailabilityForTest is the TEST-ONLY replacement for the removed
// production mdrtypes.TransactionAvailability type. It mirrors the same three
// fields so the regression tests keep asserting on the exact same semantics
// without resurrecting the dead production API.
//
//   - MissingTxHashes are the distinct canonical transactions of the relevant
//     logs that still miss a canonical row (or whose row no longer matches the
//     exact source log).
//   - LogCoverage is true when the requested addresses/block range are fully
//     covered by the live synced state.
//   - Complete is true when LogCoverage is true and there are no missing
//     transactions.
type transactionAvailabilityForTest struct {
	MissingTxHashes []common.Hash
	LogCoverage     bool
	Complete        bool
}

// mdAvailabilityForTest recomputes the enriched transaction availability of
// query from the live multidownloader PUBLIC API. It is the test-only equivalent
// of the removed production TransactionAvailabilityForLogQuery and deliberately
// reuses the real reads the provider/worker use:
//
//   - coverage comes from md.IsAvailable (the in-memory synced state);
//   - the canonical source logs come from md.storage.GetEthLogs;
//   - every distinct (tx hash, block hash) inclusion is looked up with the
//     canonical md.transactionStorage.GetTransaction and validated against the
//     exact source log (including its transaction index), so a stored row that
//     does not match its log is still reported as missing.
//
// An empty address list means "all addresses" and is expanded to every
// registered contract address so coverage and log reads agree. tx is an optional
// caller-owned storage transaction (nil uses the storage default). A storage
// read error is returned, never swallowed, so a caller can distinguish a real
// failure from a genuine "not available" answer.
func mdAvailabilityForTest(md *EVMMultidownloader, tx dbtypes.Querier,
	query mdrtypes.LogQuery, topic0 []common.Hash) (transactionAvailabilityForTest, error) {
	if md == nil {
		return transactionAvailabilityForTest{},
			fmt.Errorf("mdAvailabilityForTest: nil multidownloader")
	}
	if md.transactionStorage == nil {
		return transactionAvailabilityForTest{},
			fmt.Errorf("mdAvailabilityForTest: transaction storage is not enabled")
	}
	if len(query.Addrs) == 0 {
		query.Addrs = registeredContractAddresses(md)
		if len(query.Addrs) == 0 {
			// Nothing is registered: nothing can be covered and there is
			// nothing to look up.
			return transactionAvailabilityForTest{}, nil
		}
	}
	covered := md.IsAvailable(query)

	logs, err := md.storage.GetEthLogs(tx, query)
	if err != nil {
		return transactionAvailabilityForTest{},
			fmt.Errorf("mdAvailabilityForTest: cannot read canonical logs: %w", err)
	}

	missing := make([]common.Hash, 0)
	seen := make(map[string]struct{}, len(logs))
	for i := range logs {
		lg := logs[i]
		if !mdrtypes.Topic0Match(topic0, lg.Topics) {
			continue
		}
		// Count each canonical inclusion once, not each log occurrence.
		key := lg.TxHash.Hex() + ":" + lg.BlockHash.Hex()
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}

		stored, err := md.transactionStorage.GetTransaction(tx, lg.TxHash, lg.BlockHash)
		if err != nil {
			return transactionAvailabilityForTest{},
				fmt.Errorf("mdAvailabilityForTest: cannot read canonical transaction %s: %w",
					lg.TxHash.Hex(), err)
		}
		if stored == nil {
			missing = append(missing, lg.TxHash)
			continue
		}
		// A stored row that no longer matches the exact source log (e.g. a
		// wrong mined transaction index) must not be reported as resolved.
		if err := md.validateTransactionInclusion(stored, &lg); err != nil {
			missing = append(missing, lg.TxHash)
		}
	}
	return transactionAvailabilityForTest{
		MissingTxHashes: missing,
		LogCoverage:     covered,
		Complete:        covered && len(missing) == 0,
	}, nil
}

// registeredContractAddresses returns every contract address declared by the
// registered syncer configs, de-duplicated and in registration order.
func registeredContractAddresses(md *EVMMultidownloader) []common.Address {
	configs := md.syncersConfig.SyncerConfigs()
	seen := make(map[common.Address]struct{})
	addrs := make([]common.Address, 0)
	for _, cfg := range configs {
		for _, addr := range cfg.ContractAddresses {
			if _, ok := seen[addr]; ok {
				continue
			}
			seen[addr] = struct{}{}
			addrs = append(addrs, addr)
		}
	}
	return addrs
}
