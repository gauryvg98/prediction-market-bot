package binance

import (
	"context"
	"testing"
	"time"
)

// TestLiveFetch hits the real Binance public API. Skipped in -short.
func TestLiveFetch(t *testing.T) {
	if testing.Short() {
		t.Skip("live")
	}
	now := time.Now().UnixMilli()
	s, err := Fetch(context.Background(), now-180000, now)
	if err != nil {
		t.Fatal(err)
	}
	p := s.At(now)
	t.Logf("BTC now ~ %.2f, candles=%d", p, len(s.tMs))
	if p < 1000 || p > 1_000_000 {
		t.Fatalf("implausible BTC price %.2f", p)
	}
}
