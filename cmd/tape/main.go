// Command tape pulls the REAL World tape from Bitquery and reconstructs priced
// outcome-token trades -- proof that live market data flows into this system.
//
//	BITQUERY_TOKEN=... go run ./cmd/tape -minutes 8 -trades 40
//
// Each printed line is a real fill: price = CASH / outcome-tokens, cost per $1
// of payout. Grouped by mint, these are the per-side price series the strategy
// reasons over; complementary mints (prices summing to ~1+vig) are the two sides
// of one round.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"sort"
	"time"

	"github.com/gauryvg98/prediction-bot/internal/bitquery"
)

func main() {
	minutes := flag.Int("minutes", 8, "look-back window")
	cashLimit := flag.Int("cash", 150, "CASH legs to scan")
	trades := flag.Int("trades", 40, "max trades to price (1 query each)")
	flag.Parse()
	token := os.Getenv("BITQUERY_TOKEN")
	if token == "" {
		fmt.Fprintln(os.Stderr, "set BITQUERY_TOKEN")
		os.Exit(1)
	}
	c := bitquery.New(token)
	since := time.Now().UTC().Add(-time.Duration(*minutes) * time.Minute).Format("2006-01-02T15:04:05Z")

	fmt.Printf("pulling live World tape since %s ...\n", since)
	fills, err := c.RecentFills(context.Background(), since, *cashLimit, *trades)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	fmt.Printf("priced %d real fills\n\n", len(fills))

	fmt.Printf("%-9s %-5s %8s %10s  %-12s\n", "time", "side", "price", "cash", "mint")
	byMint := map[string][]float64{}
	for _, f := range fills {
		p := f.Price()
		byMint[f.Mint] = append(byMint[f.Mint], p)
		dir := "BUY"
		if !f.Buy {
			dir = "SELL"
		}
		fmt.Printf("%-9s %-5s %8.4f %10.2f  %s..\n", f.TimeISO[11:19], dir, p, f.Cash, f.Mint[:10])
	}

	// group into per-side price series, then find complementary pairs = rounds
	type ms struct {
		mint string
		mean float64
		n    int
	}
	var mints []ms
	for m, ps := range byMint {
		var s float64
		for _, p := range ps {
			s += p
		}
		mints = append(mints, ms{m, s / float64(len(ps)), len(ps)})
	}
	sort.Slice(mints, func(i, j int) bool { return mints[i].n > mints[j].n })
	fmt.Printf("\nper-side price series (%d distinct outcome mints):\n", len(mints))
	for _, m := range mints {
		fmt.Printf("  %s..  mean %.4f  (%d fills)\n", m.mint[:12], m.mean, m.n)
	}
	// complementary-mint pairing: two sides of a round sum to ~1+vig
	fmt.Println("\ncandidate round pairs (prices sum near 1, i.e. UP+DOWN of one round):")
	found := 0
	for i := 0; i < len(mints); i++ {
		for j := i + 1; j < len(mints); j++ {
			s := mints[i].mean + mints[j].mean
			if s > 1.0 && s < 1.25 {
				fmt.Printf("  %s.. + %s..  S=%.4f (overround %+.3f)\n",
					mints[i].mint[:8], mints[j].mint[:8], s, s-1)
				found++
			}
		}
	}
	if found == 0 {
		fmt.Println("  (none in this window -- need overlapping fills on both sides of a live round)")
	}
}
