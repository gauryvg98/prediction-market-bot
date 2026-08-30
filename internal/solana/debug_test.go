package solana

import (
	"context"
	"os"
	"testing"
)

func TestDebugSigsAndDeltas(t *testing.T) {
	if testing.Short() {
		t.Skip("live")
	}
	url := os.Getenv("SOLANA_RPC_URL")
	if url == "" {
		url = "https://api.mainnet-beta.solana.com"
	}
	c := New(url)
	ctx := context.Background()
	for _, addr := range []string{JanusMaker, BisonMaker, PredictProgram} {
		infos, err := c.SignaturesForAddress(ctx, addr, 5, "")
		if err != nil {
			t.Logf("%s: ERR %v", addr[:8], err)
			continue
		}
		t.Logf("%s -> %d sigs", addr[:8], len(infos))
		// dump deltas of the first successful one
		for _, si := range infos {
			if si.Err != nil {
				continue
			}
			tx, err := c.getTransaction(ctx, si.Signature)
			if err != nil || tx == nil {
				continue
			}
			var signer string
			for _, k := range tx.Transaction.Message.AccountKeys {
				if k.Signer {
					signer = k.Pubkey
					break
				}
			}
			t.Logf("  sig %s signer=%s pre=%d post=%d", si.Signature[:10], sh(signer),
				len(tx.Meta.PreTokenBalances), len(tx.Meta.PostTokenBalances))
			// show token deltas by owner
			shown := 0
			for _, b := range tx.Meta.PostTokenBalances {
				if shown >= 5 {
					break
				}
				tag := b.Mint[:8]
				if b.Mint == CashMint {
					tag = "CASH"
				}
				t.Logf("      owner=%s mint=%s amt=%s", sh(b.Owner), tag, b.UITokenAmt.UIAmountString)
				shown++
			}
			break
		}
	}
}
func sh(s string) string {
	if len(s) > 8 {
		return s[:8]
	}
	return s
}
