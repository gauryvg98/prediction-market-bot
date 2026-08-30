// Command paper runs the strategy end to end on paper -- no transactions, ever.
//
// It drives the paper engine from a feed (synthetic by default) and streams
// every enter/hedge/merge/settle as it happens, then prints the honest summary:
// lock rate beside naked expiries, net P&L, and the worst single round.
//
//	go run ./cmd/paper                 # fair market (honest null: mostly skips)
//	go run ./cmd/paper -bias 0.7       # maker under-prices vol -> machine lights up
//	go run ./cmd/paper -rounds 500 -v  # more rounds, verbose per-event log
//
// A high lock rate here is NOT proof of edge -- it is the machine working on
// SIMULATED data. The same binary, pointed at the real Bitquery tape, is what
// turns "it works" into a number.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/gauryvg98/prediction-bot/internal/bitquery"
	"github.com/gauryvg98/prediction-bot/internal/feed"
	"github.com/gauryvg98/prediction-bot/internal/paper"
)

func main() {
	rounds := flag.Int("rounds", 200, "number of synthetic rounds")
	bias := flag.Float64("bias", 1.0, "maker implied-vol bias (1.0 fair; <1 under-prices movement)")
	stake := flag.Float64("stake", 10, "paper CASH stake per entry")
	verbose := flag.Bool("v", false, "log every event, not just entries and locks")
	seed := flag.Int64("seed", 1, "RNG seed for the synthetic feed")
	source := flag.String("source", "synthetic", "synthetic | bitquery | file")
	file := flag.String("file", "data/fills.jsonl", "accumulated tape for -source file")
	minutes := flag.Int("minutes", 25, "bitquery: look-back window in minutes")
	flag.Parse()

	cfg := paper.DefaultConfig()
	cfg.Stake = *stake
	eng := paper.New(cfg)

	var src feed.Feed
	switch *source {
	case "bitquery":
		token := os.Getenv("BITQUERY_TOKEN")
		if token == "" {
			fmt.Fprintln(os.Stderr, "set BITQUERY_TOKEN for -source bitquery")
			os.Exit(1)
		}
		fmt.Printf("paper run on LIVE Bitquery tape (last %d min), stake $%.0f\n\n", *minutes, cfg.Stake)
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()
		cfg.RawEntry = true
		bq, err := feed.LoadBitquery(ctx, bitquery.New(token), *minutes, 15)
		if err != nil {
			fmt.Fprintln(os.Stderr, "load bitquery feed:", err)
			os.Exit(1)
		}
		src = bq
	case "file":
		cfg.RawEntry = true
		fmt.Printf("paper run on ACCUMULATED real tape %s, stake $%.0f\n\n", *file, cfg.Stake)
		bq, err := feed.LoadFile(*file, 15)
		if err != nil {
			fmt.Fprintln(os.Stderr, "load file feed:", err)
			os.Exit(1)
		}
		src = bq
	default:
		sc := feed.DefaultSynth()
		sc.Rounds = *rounds
		sc.ImpliedBias = *bias
		sc.Seed = *seed
		src = feed.NewSynthetic(sc)
		fmt.Printf("paper run: %d synthetic rounds, maker bias %.2f, stake $%.0f\n", sc.Rounds, sc.ImpliedBias, cfg.Stake)
		fmt.Printf("(bias 1.0 = fair market; the honest default is mostly SKIP)\n\n")
	}
	_ = rounds; _ = bias; _ = seed

	for {
		r, ok := src.Next()
		if !ok {
			break
		}
		for _, t := range r.Ticks {
			for _, ev := range eng.OnTick(t) {
				if *verbose || ev.Kind == paper.Enter || ev.Kind == paper.Merge {
					fmt.Println("  ", ev)
				}
			}
		}
		for _, ev := range eng.OnResolve(r.Resolution) {
			if *verbose || ev.Kind == paper.Settle {
				fmt.Println("  ", ev)
			}
		}
	}

	fmt.Printf("\n%s\n", eng.Ledger.Summary())
}
