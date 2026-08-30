package feed

import (
	"context"
	"fmt"
	"sort"

	"github.com/gauryvg98/prediction-bot/internal/market"
	"github.com/gauryvg98/prediction-bot/internal/paper"
	"github.com/gauryvg98/prediction-bot/internal/solana"
)

// LoadSolana builds real rounds by reading the World tape straight from a Solana
// node (Helius). Contamination-free: trade sigs come from prediCt, prices from
// the taker's own balance deltas. Pairs complementary mints into rounds.
func LoadSolana(ctx context.Context, c *solana.Client, sigCount, paceMs int, cadenceSec float64) (*Bitquery, error) {
	fmt.Printf("[feed] reading %d prediCt sigs from chain...\n", sigCount)
	fills, err := c.RecentFills(ctx, sigCount, paceMs)
	if err != nil {
		return nil, fmt.Errorf("recent fills: %w", err)
	}
	series := map[string][]solana.Fill{}
	for _, f := range fills {
		series[f.Mint] = append(series[f.Mint], f)
	}
	fmt.Printf("[feed] %d real fills across %d World outcome mints\n", len(fills), len(series))
	for m, fs := range series {
		if len(fs) >= 2 {
			var sum float64
			for _, f := range fs { sum += f.Price() }
			fmt.Printf("    %s.. n=%d mean=%.3f span=%ds\n", m[:10], len(fs), sum/float64(len(fs)), (fs[len(fs)-1].TimeMs-fs[0].TimeMs)/1000)
		}
	}

	pairs := pairBySibling(fills)
	fmt.Printf("[feed] paired %d round(s) by complete-set co-occurrence\n", len(pairs))
	var rounds []*Round
	for _, pr := range pairs {
		if r, ok := buildSolanaRound(pr, cadenceSec); ok {
			rounds = append(rounds, r)
		}
	}
	fmt.Printf("[feed] built %d usable round(s)\n\n", len(rounds))
	if len(rounds) == 0 {
		return nil, fmt.Errorf("no complete round in this pull (try -sigs larger)")
	}
	return &Bitquery{rounds: rounds}, nil
}

type solPair struct {
	up, down       []solana.Fill
	startMs, endMs int64
}

// pairBySibling groups fills into rounds using the sibling mint each buy reveals
// (a complete set touches both sides). Two mints in the same round are linked by
// appearing as (Mint, Sibling) in some trade -- definitive, not price-inferred.
func pairBySibling(fills []solana.Fill) []solPair {
	// union-find over mints linked by sibling
	parent := map[string]string{}
	var find func(string) string
	find = func(x string) string {
		if parent[x] == "" { parent[x] = x }
		if parent[x] != x { parent[x] = find(parent[x]) }
		return parent[x]
	}
	union := func(a, b string) { if a != "" && b != "" { parent[find(a)] = find(b) } }
	byMint := map[string][]solana.Fill{}
	for _, f := range fills {
		byMint[f.Mint] = append(byMint[f.Mint], f)
		find(f.Mint)
		if f.Sibling != "" { union(f.Mint, f.Sibling) }
	}
	// gather rounds: groups of mints; keep those with exactly 2 sides that both traded
	groups := map[string][]string{}
	for m := range byMint { groups[find(m)] = append(groups[find(m)], m) }
	var out []solPair
	for _, mints := range groups {
		if len(mints) != 2 { continue }
		a, b := byMint[mints[0]], byMint[mints[1]]
		if len(a) == 0 || len(b) == 0 { continue }
		lo, hi := int64(1)<<62, int64(0)
		for _, f := range append(append([]solana.Fill{}, a...), b...) {
			if f.TimeMs < lo { lo = f.TimeMs }
			if f.TimeMs > hi { hi = f.TimeMs }
		}
		out = append(out, solPair{up: a, down: b, startMs: lo, endMs: hi})
	}
	return out
}

func pairSolana(series map[string][]solana.Fill) []solPair {
	type mm struct {
		mint   string
		mean   float64
		lo, hi int64
		fills  []solana.Fill
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
			if f.TimeMs < lo {
				lo = f.TimeMs
			}
			if f.TimeMs > hi {
				hi = f.TimeMs
			}
		}
		ms = append(ms, mm{m, sum / float64(len(fs)), lo, hi, fs})
	}
	type cand struct{ i, j int; s float64 }
	var cands []cand
	for i := 0; i < len(ms); i++ {
		for j := i + 1; j < len(ms); j++ {
			s := ms[i].mean + ms[j].mean
			overlap := ms[i].lo <= ms[j].hi && ms[j].lo <= ms[i].hi
			if s > 1.0 && s < 1.25 && overlap {
				cands = append(cands, cand{i, j, s})
			}
		}
	}
	sort.Slice(cands, func(a, b int) bool { return absf(cands[a].s-1.06) < absf(cands[b].s-1.06) })
	var out []solPair
	used := map[string]bool{}
	for _, c := range cands {
		if used[ms[c.i].mint] || used[ms[c.j].mint] {
			continue
		}
		used[ms[c.i].mint], used[ms[c.j].mint] = true, true
		out = append(out, solPair{up: ms[c.i].fills, down: ms[c.j].fills,
			startMs: min64(ms[c.i].lo, ms[c.j].lo), endMs: max64(ms[c.i].hi, ms[c.j].hi)})
	}
	return out
}

func buildSolanaRound(pr solPair, cadenceSec float64) (*Round, bool) {
	id := fmt.Sprintf("WLD-%d", pr.startMs/1000%100000)
	step := int64(cadenceSec * 1000)
	if step <= 0 || pr.endMs <= pr.startMs {
		return nil, false
	}
	sort.Slice(pr.up, func(i, j int) bool { return pr.up[i].TimeMs < pr.up[j].TimeMs })
	sort.Slice(pr.down, func(i, j int) bool { return pr.down[i].TimeMs < pr.down[j].TimeMs })
	ui, di := 0, 0
	var upP, downP float64
	var ticks []paper.Tick
	for tMs := pr.startMs; tMs <= pr.endMs; tMs += step {
		for ui < len(pr.up) && pr.up[ui].TimeMs <= tMs {
			upP = pr.up[ui].Price()
			ui++
		}
		for di < len(pr.down) && pr.down[di].TimeMs <= tMs {
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
	if lastSol(pr.down) > lastSol(pr.up) {
		winner = market.Down
	}
	return &Round{Ticks: ticks, Resolution: paper.Resolution{RoundID: id, Winner: winner}}, true
}

func lastSol(fs []solana.Fill) float64 {
	if len(fs) == 0 {
		return 0
	}
	return fs[len(fs)-1].Price()
}
