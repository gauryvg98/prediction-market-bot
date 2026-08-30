// Package binance fetches BTC spot from Binance's public data API -- no key, no
// auth. It supplies the two prices the tape cannot: the round's STRIKE (spot at
// open, which is what World's oracle fixes) and per-tick SPOT for the vol gate,
// plus the resolution (did spot finish above the strike).
//
// Binance is a CEX-price source; World resolves on Chainlink Data Streams, a
// CEX-price AGGREGATE. They agree to a small basis, which is exactly the
// quantity a later pass measures -- here Binance is a faithful stand-in.
package binance

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"time"
)

const base = "https://data-api.binance.vision/api/v3/klines"

// Series is a time-ordered set of 1-second BTC prices, queryable by instant.
type Series struct {
	tMs   []int64
	price []float64
}

// Fetch pulls 1-second BTCUSDT closes covering [startMs, endMs] (inclusive),
// paging Binance's 1000-candle limit as needed.
func Fetch(ctx context.Context, startMs, endMs int64) (*Series, error) {
	s := &Series{}
	cli := &http.Client{Timeout: 30 * time.Second}
	for cur := startMs; cur <= endMs; {
		url := fmt.Sprintf("%s?symbol=BTCUSDT&interval=1s&startTime=%d&endTime=%d&limit=1000",
			base, cur, endMs)
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		resp, err := cli.Do(req)
		if err != nil {
			return nil, err
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("binance http %d: %.120s", resp.StatusCode, body)
		}
		var rows [][]any
		if err := json.Unmarshal(body, &rows); err != nil {
			return nil, err
		}
		if len(rows) == 0 {
			break
		}
		for _, r := range rows {
			openMs := int64(r[0].(float64))
			var close float64
			fmt.Sscanf(r[4].(string), "%g", &close)
			s.tMs = append(s.tMs, openMs)
			s.price = append(s.price, close)
		}
		last := int64(rows[len(rows)-1][0].(float64))
		if last < cur {
			break
		}
		cur = last + 1000
		if len(rows) < 1000 {
			break
		}
	}
	if len(s.tMs) == 0 {
		return nil, fmt.Errorf("binance: no candles for [%d,%d]", startMs, endMs)
	}
	return s, nil
}

// At returns the BTC price at (or just before) the given instant.
func (s *Series) At(ms int64) float64 {
	i := sort.Search(len(s.tMs), func(i int) bool { return s.tMs[i] > ms })
	if i == 0 {
		return s.price[0]
	}
	return s.price[i-1]
}

func (s *Series) Span() (int64, int64) { return s.tMs[0], s.tMs[len(s.tMs)-1] }
