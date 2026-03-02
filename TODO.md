- Bridges from AL and bridges restored from L2 should be sent in two diff txs, as the aggsender should send again the L2 ones, and if restored on same tx, aggsender works at per block granularity, will send everything
- hash cert to reuse existing logic @ aggsender service
    - maybe already done
- meaningful names -> avoid "case N" + do not reference runbook, but README on the tool dir
- claims can also create divergence!! Very hard to detect, since aggkit doesn't sync this in a tree shape
    - Unlikely to happen. If it does -> manually remove claim from DB
- instead of finding divergence by comparing bridges, do it by comparing LER
- findDivergencePoint returns divergent leaves that exist on agglayer, but, are we restoring the L2 exits that will be removed?
    - sembla que si, `diagnosis.DivergentLeaves` + `diagnosis.ExtraL2Bridges`
- what if we dont find cert on aggsender, how could we recover? --> ask agglayer for missing data, then load it, how?
- since we have added ERC20 token with mint, parallelize post test conditions (L2 -> L1 using erc20 | L1 -> L2)
- test no divergence when deposit count @ L2 > deposit count @ agglayer (stop aggkit to ensure no further certs received by AL)
- it looks like newBlockPos is an abuse of the primary key of the tree in handleForwardLETEvent fn. If there is a bridge on the same block, in a subsequent tx it could violate the primary key of the DB
- too many aggkit restarts

---

- if any L1 or L2 transaction gets reverted, look for the revert reason, it will be a hexa code that you can get meaningful value by checking this doc: /home/aigent/repos/agglayer-contracts/docs/selectors.txt and then if you need further details, smart contracts are implemented here: /home/aigent/repos/agglayer-contracts/contracts
- aggsender will not send certificates if there are no bridges (new bridge on the L2 smart contract)
- if aggkit is started with the debug endpoint for sending fake certs enabled on aggsender, it will not send certificates