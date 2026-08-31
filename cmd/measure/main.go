// Command measure is the Phase 0 go/no-go: it runs the REAL strategy on REAL
// World rounds with REAL price context, and reports whether the observed lock
// rate beats the fair-market ceiling.
//
//	SOLANA_RPC_URL=... go run ./cmd/measure -mins 30
//
// It streams the Chainlink oracle (the price World resolves on) to accumulate
// history, periodically reads the on-chain trade tape from Helius, and evaluates
// each round that BOTH completed and is fully covered by the oracle history --
// through the real vol gate, the lock engine, and honest settlement. No
// transactions; nothing here can move funds.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/gauryvg98/prediction-bot/internal/feed"
	"github.com/gauryvg98/prediction-bot/internal/oracle"
	"github.com/gauryvg98/prediction-bot/internal/paper"
	"github.com/gauryvg98/prediction-bot/internal/solana"
)

func main() {
	mins := flag.Int("mins", 30, "how long to run the measurement")
	sigs := flag.Int("sigs", 600, "prediCt sigs per poll")
	poll := flag.Duration("poll", 60*time.Second, "trade-poll interval")
	dur := flag.Int("round", 300, "round duration seconds (5-min = 300)")
	flag.Parse()
	url := os.Getenv("SOLANA_RPC_URL")
	if url == "" {
		fmt.Fprintln(os.Stderr, "set SOLANA_RPC_URL (Helius)")
		os.Exit(1)
	}
	root, cancel := context.WithTimeout(context.Background(), time.Duration(*mins)*time.Minute)
	defer cancel()

	// 1) stream the Chainlink oracle, accumulating history, with reconnect.
	ocl := oracle.NewRecording()
	go func() {
		for root.Err() == nil {
			_ = ocl.Run(root)
			time.Sleep(time.Second)
		}
	}()
	fmt.Println("[measure] warming up oracle history (20s)...")
	time.Sleep(20 * time.Second)

	// 2) evaluate rounds as they complete, deduped, into one ledger.
	rpc := solana.New(url)
	eng := paper.New(paper.DefaultConfig()) // real vol gate (RawEntry off)
	seen := map[string]bool{}
	tick := time.NewTicker(*poll)
	defer tick.Stop()
	evalRound := func() {
		ctx, c := context.WithTimeout(root, 90*time.Second)
		defer c()
		fills, err := rpc.RecentFills(ctx, *sigs, 0)
		if err != nil {
			fmt.Printf("[measure] tape error: %v\n", err)
			return
		}
		rounds := feed.BuildOracleRounds(fills, ocl, *dur, 15)
		fresh := 0
		for _, r := range rounds {
			id := r.Ticks[0].RoundID
			if seen[id] {
				continue
			}
			seen[id] = true
			fresh++
			for _, t := range r.Ticks {
				eng.OnTick(t)
			}
			eng.OnResolve(r.Resolution)
		}
		lo, hi, _ := ocl.ChainlinkSpan()
		fmt.Printf("[measure] +%d new rounds (oracle covers %ds) | %s\n",
			fresh, (hi-lo)/1000, eng.Ledger.Summary())
	}
	evalRound()
	for {
		select {
		case <-root.Done():
			fmt.Println("\n==================== FINAL ====================")
			fmt.Println(eng.Ledger.Summary())
			return
		case <-tick.C:
			evalRound()
		}
	}
}
