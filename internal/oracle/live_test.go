package oracle

import (
	"context"
	"testing"
	"time"
)

// TestLiveRTDS connects to the real Polymarket RTDS and confirms BOTH the
// Chainlink resolution oracle and Binance spot stream in. Skipped in -short.
func TestLiveRTDS(t *testing.T) {
	if testing.Short() {
		t.Skip("live")
	}
	c := New()
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	_ = c.Run(ctx)
	cl, _ := c.Chainlink()
	bn, _ := c.Binance()
	t.Logf("chainlink btc=%.2f  binance btc=%.2f  basis=%.2f", cl, bn, bn-cl)
	if cl < 1000 || cl > 1_000_000 {
		t.Fatalf("no plausible Chainlink BTC price: %.2f", cl)
	}
	if bn < 1000 || bn > 1_000_000 {
		t.Fatalf("no plausible Binance BTC price: %.2f", bn)
	}
}
