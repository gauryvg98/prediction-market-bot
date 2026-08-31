package feed

import (
	"fmt"
	"sort"

	"github.com/gauryvg98/prediction-bot/internal/market"
	"github.com/gauryvg98/prediction-bot/internal/oracle"
	"github.com/gauryvg98/prediction-bot/internal/paper"
	"github.com/gauryvg98/prediction-bot/internal/solana"
)

// OracleCtx is the price context a real round needs -- satisfied by *oracle.Client.
type OracleCtx interface {
	ChainlinkAt(ms int64) (float64, bool)
	RealizedVol(ms int64, windowSec int) float64
}

var _ OracleCtx = (*oracle.Client)(nil)

// BuildOracleRounds turns paired World trades into fully-contextualized rounds:
// window snapped to the round grid, strike = Chainlink at open, spot = Chainlink
// per tick, resolution = Chainlink at expiry vs strike (World's own oracle), and
// realized vol from Chainlink. Only rounds the oracle history fully covers are
// returned -- everything else is honestly skipped, not approximated.
func BuildOracleRounds(fills []solana.Fill, ocl OracleCtx, durationSec, cadenceSec int) []*Round {
	pairs := pairBySibling(fills)
	var out []*Round
	dur := int64(durationSec) * 1000
	step := int64(cadenceSec) * 1000
	for _, pr := range pairs {
		// snap the round window to the grid: start = floor(firstTrade / dur)
		first := min64(firstMs(pr.up), firstMs(pr.down))
		last := max64(lastMsF(pr.up), lastMsF(pr.down))
		start := (first / dur) * dur
		end := start + dur
		if last > end { // trades exceed one window -> wrong duration; skip
			continue
		}
		strike, ok := ocl.ChainlinkAt(start)
		if !ok {
			continue // oracle doesn't cover the open
		}
		settle, ok := ocl.ChainlinkAt(end)
		if !ok {
			continue // round not resolved within oracle coverage yet
		}
		id := fmt.Sprintf("WLD-%d", start/1000%100000)
		sort.Slice(pr.up, func(i, j int) bool { return pr.up[i].TimeMs < pr.up[j].TimeMs })
		sort.Slice(pr.down, func(i, j int) bool { return pr.down[i].TimeMs < pr.down[j].TimeMs })
		ui, di := 0, 0
		var upP, downP float64
		var ticks []paper.Tick
		for t := start; t <= end; t += step {
			for ui < len(pr.up) && pr.up[ui].TimeMs <= t {
				upP = pr.up[ui].Price()
				ui++
			}
			for di < len(pr.down) && pr.down[di].TimeMs <= t {
				downP = pr.down[di].Price()
				di++
			}
			if upP <= 0 || downP <= 0 {
				continue
			}
			spot, ok := ocl.ChainlinkAt(t)
			if !ok {
				continue
			}
			ticks = append(ticks, paper.Tick{
				RoundID: id, Spot: spot, Strike: strike,
				SecondsToExpiry: float64(end-t) / 1000,
				RealizedVol:     ocl.RealizedVol(t, 60),
				Book: &market.Book{RoundID: id, TsMs: t, ExpiryMs: end,
					Up:   []market.Level{{Price: upP, Size: 1e6}},
					Down: []market.Level{{Price: downP, Size: 1e6}}},
			})
		}
		if len(ticks) < 3 {
			continue
		}
		winner := market.Up
		if settle <= strike {
			winner = market.Down
		}
		out = append(out, &Round{Ticks: ticks,
			Resolution: paper.Resolution{RoundID: id, Winner: winner, SettlePrice: settle}})
	}
	return out
}

func firstMs(fs []solana.Fill) int64 {
	m := int64(1) << 62
	for _, f := range fs {
		if f.TimeMs < m {
			m = f.TimeMs
		}
	}
	return m
}
func lastMsF(fs []solana.Fill) int64 {
	var m int64
	for _, f := range fs {
		if f.TimeMs > m {
			m = f.TimeMs
		}
	}
	return m
}
