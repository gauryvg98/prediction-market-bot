package solana

import (
	"context"
	"os"
	"testing"
)

// TestLiveRecon hits a real RPC (SOLANA_RPC_URL or public mainnet) and
// reconstructs a few real World trades. Skipped in -short.
func TestLiveRecon(t *testing.T) {
	if testing.Short() {
		t.Skip("live")
	}
	url := os.Getenv("SOLANA_RPC_URL")
	if url == "" {
		url = "https://api.mainnet-beta.solana.com"
	}
	c := New(url)
	fills, err := c.RecentFills(context.Background(), 12, 400)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("reconstructed %d real World fills", len(fills))
	for i, f := range fills {
		if i >= 8 {
			break
		}
		pump := ""
		if len(f.Mint) > 4 && f.Mint[len(f.Mint)-4:] == "pump" {
			pump = " <-- CONTAMINATION"
		}
		t.Logf("  %s price=%.4f cash=%.2f mint=%s%s",
			map[bool]string{true: "BUY", false: "SELL"}[f.Buy], f.Price(), f.Cash, f.Mint[:12], pump)
	}
}
