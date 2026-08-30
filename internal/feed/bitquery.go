package feed

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/gauryvg98/prediction-bot/internal/bitquery"
	"github.com/gauryvg98/prediction-bot/internal/market"
	"github.com/gauryvg98/prediction-bot/internal/paper"
)

// Bitquery reconstructs REAL rounds from the live World tape and serves them
// through the same Feed interface the synthetic generator uses -- so the paper
// engine runs on real fills unchanged.
//
// Two queries, rate-limit friendly: (1) every outcome-token transfer in the
// window (Token-2022, minus CASH), (2) the CASH map to price them. Fills are
// grouped by mint, complementary mints (prices summing to ~1+vig) are paired
// into rounds, and each round is sampled into Book ticks. Resolution is inferred
// from the tape itself -- the winning side's price converges toward 1. No
// external spot feed, so this is asset-agnostic: it measures the pure LOCK
// mechanic on real prices.
type Bitquery struct {
	rounds []*Round
	i      int
}

func parseMs(iso string) int64 {
	t, err := time.Parse(time.RFC3339, iso)
	if err != nil {
		return 0
	}
	return t.UnixMilli()
}

// LoadBitquery builds rounds from the last `minutes` of tape (2 queries).
func LoadBitquery(ctx context.Context, bq *bitquery.Client, minutes int, cadenceSec float64) (*Bitquery, error) {
	now := time.Now().UnixMilli()
	sinceISO := time.UnixMilli(now).UTC().Add(-time.Duration(minutes) * time.Minute).Format(time.RFC3339)
	tillISO := time.UnixMilli(now + 60000).UTC().Format(time.RFC3339)

	fmt.Printf("[feed] scanning CASH sigs + batched leg fetch (last %d min)...\n", minutes)
	fills, err := bq.RecentFillsBatched(ctx, sinceISO, tillISO, 1500)
	if err != nil {
		return nil, fmt.Errorf("reconstruct: %w", err)
	}
	series := map[string][]bitquery.Fill{}
	for _, f := range fills {
		series[f.Mint] = append(series[f.Mint], f)
	}
	fmt.Printf("[feed] %d priced fills across %d outcome mints\n", len(fills), len(series))

	pairs := pairComplementary(series)
	fmt.Printf("[feed] paired %d round(s)\n", len(pairs))

	var rounds []*Round
	for _, pr := range pairs {
		if r, ok := buildRound(pr, cadenceSec); ok {
			rounds = append(rounds, r)
		}
	}
	fmt.Printf("[feed] built %d usable round(s)\n\n", len(rounds))
	if len(rounds) == 0 {
		return nil, fmt.Errorf("no usable rounds (try -minutes larger)")
	}
	return &Bitquery{rounds: rounds}, nil
}

func (b *Bitquery) Next() (*Round, bool) {
	if b.i >= len(b.rounds) {
		return nil, false
	}
	r := b.rounds[b.i]
	b.i++
	return r, true
}

type mintPair struct {
	up, down []bitquery.Fill
	startMs  int64
	endMs    int64
}

func pairComplementary(series map[string][]bitquery.Fill) []mintPair {
	type mm struct {
		mint       string
		mean       float64
		lastPrice  float64
		lo, hi     int64
		fills      []bitquery.Fill
	}
	var ms []mm
	for m, fs := range series {
		if len(fs) < 2 {
			continue
		}
		var sum float64
		lo, hi := int64(1)<<62, int64(0)
		for _, f := range fs {
			sum += f.Price()
			t := parseMs(f.TimeISO)
			if t < lo {
				lo = t
			}
			if t > hi {
				hi = t
			}
		}
		ms = append(ms, mm{m, sum / float64(len(fs)), fs[len(fs)-1].Price(), lo, hi, fs})
	}
	var out []mintPair
	used := map[string]bool{}
	// prefer the tightest complementary pairs first
	type cand struct {
		i, j int
		s    float64
	}
	var cands []cand
	for i := 0; i < len(ms); i++ {
		for j := i + 1; j < len(ms); j++ {
			s := ms[i].mean + ms[j].mean
			overlap := ms[i].lo < ms[j].hi && ms[j].lo < ms[i].hi
			if s > 1.0 && s < 1.25 && overlap {
				cands = append(cands, cand{i, j, s})
			}
		}
	}
	sort.Slice(cands, func(a, b int) bool {
		return absf(cands[a].s-1.06) < absf(cands[b].s-1.06)
	})
	for _, c := range cands {
		if used[ms[c.i].mint] || used[ms[c.j].mint] {
			continue
		}
		used[ms[c.i].mint], used[ms[c.j].mint] = true, true
		// UP = side whose final price is higher only matters for resolution;
		// order by mint here, resolution decided in buildRound.
		out = append(out, mintPair{
			up: ms[c.i].fills, down: ms[c.j].fills,
			startMs: min64(ms[c.i].lo, ms[c.j].lo), endMs: max64(ms[c.i].hi, ms[c.j].hi),
		})
	}
	return out
}

// buildRound samples a paired round into Book ticks (LOCF). Resolution is the
// side whose last price is higher (converging toward the $1 payout). Spot/strike
// are left zero: tape mode uses the engine's book-only (RawEntry) path.
func buildRound(pr mintPair, cadenceSec float64) (*Round, bool) {
	id := fmt.Sprintf("BQ-%s", time.UnixMilli(pr.startMs).UTC().Format("1504"))
	step := int64(cadenceSec * 1000)
	if step <= 0 || pr.endMs <= pr.startMs {
		return nil, false
	}
	ui, di := 0, 0
	var upP, downP float64
	var ticks []paper.Tick
	for tMs := pr.startMs; tMs <= pr.endMs; tMs += step {
		for ui < len(pr.up) && parseMs(pr.up[ui].TimeISO) <= tMs {
			upP = pr.up[ui].Price()
			ui++
		}
		for di < len(pr.down) && parseMs(pr.down[di].TimeISO) <= tMs {
			downP = pr.down[di].Price()
			di++
		}
		if upP <= 0 || downP <= 0 {
			continue
		}
		ticks = append(ticks, paper.Tick{
			RoundID: id, SecondsToExpiry: float64(pr.endMs-tMs) / 1000,
			Book: &market.Book{RoundID: id, TsMs: tMs, ExpiryMs: pr.endMs,
				Up:   []market.Level{{Price: upP, Size: 1e6}},
				Down: []market.Level{{Price: downP, Size: 1e6}}},
		})
	}
	if len(ticks) < 2 {
		return nil, false
	}
	winner := market.Up
	if lastPrice(pr.down) > lastPrice(pr.up) {
		winner = market.Down
	}
	return &Round{Ticks: ticks, Resolution: paper.Resolution{RoundID: id, Winner: winner}}, true
}

func lastPrice(fs []bitquery.Fill) float64 {
	if len(fs) == 0 {
		return 0
	}
	return fs[len(fs)-1].Price()
}
func absf(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}
func min64(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}
func max64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}
