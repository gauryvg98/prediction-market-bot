package oracle

import (
	"context"
	"testing"
	"time"
)

func TestHistorySpan(t *testing.T) {
	if testing.Short() {
		t.Skip("live")
	}
	c := NewRecording()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = c.Run(ctx)
	lo, hi, ok := c.ChainlinkSpan()
	if !ok {
		t.Fatal("no chainlink history")
	}
	t.Logf("chainlink history: %ds span (%d points)", (hi-lo)/1000, len(c.clHist.pts))
	// sample a mid price
	mid := lo + (hi-lo)/2
	p, _ := c.ChainlinkAt(mid)
	rv := c.RealizedVol(hi, 60)
	t.Logf("mid price=%.2f  realizedVol(per-sqrt-s)=%.2e  (~%.0f%% annualized)", p, rv, rv*sqrt(31557600)*100)
}
