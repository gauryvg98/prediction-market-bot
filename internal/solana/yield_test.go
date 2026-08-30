package solana

import (
	"context"
	"os"
	"testing"
)

func TestYield(t *testing.T) {
	if testing.Short() {
		t.Skip("live")
	}
	url := os.Getenv("SOLANA_RPC_URL")
	if url == "" {
		url = "https://api.mainnet-beta.solana.com"
	}
	c := New(url)
	ctx := context.Background()
	infos, err := c.SignaturesForAddress(ctx, PredictProgram, 40, "")
	if err != nil {
		t.Fatal(err)
	}
	var operators, traders, fills int
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
		if signer == Operator {
			operators++
			continue
		}
		traders++
		if f, ok := reconstruct(tx); ok {
			fills++
			t.Logf("  TRADE %s price=%.4f cash=%.2f mint=%s", si.Signature[:8], f.Price(), f.Cash, f.Mint[:10])
		}
	}
	t.Logf("of %d sigs: %d operator, %d non-operator, %d reconstructed as fills", len(infos), operators, traders, fills)
}
