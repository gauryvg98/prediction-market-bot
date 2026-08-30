// Command collect accumulates the REAL World tape into a local file over time.
//
//	BITQUERY_TOKEN=... go run ./cmd/collect -out data/fills.jsonl -every 30s
//
// Per-round trade volume is sparse relative to total CASH movement, so a single
// burst rarely catches both sides of a round. This runs continuously, appending
// newly-seen real fills, until the accumulated tape is dense enough to replay
// through the paper engine (go run ./cmd/paper -source file).
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/gauryvg98/prediction-bot/internal/bitquery"
	"github.com/gauryvg98/prediction-bot/internal/feed"
)

func main() {
	out := flag.String("out", "data/fills.jsonl", "tape file to append to")
	every := flag.Duration("every", 30*time.Second, "poll interval")
	minutes := flag.Int("minutes", 20, "look-back per poll")
	rounds := flag.Int("rounds", 40, "stop after this many polls")
	flag.Parse()
	token := os.Getenv("BITQUERY_TOKEN")
	if token == "" {
		fmt.Fprintln(os.Stderr, "set BITQUERY_TOKEN")
		os.Exit(1)
	}
	bq := bitquery.New(token)
	total := 0
	for i := 0; i < *rounds; i++ {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		added, mints, err := feed.Collect(ctx, bq, *out, *minutes)
		cancel()
		if err != nil {
			fmt.Printf("[collect %d] error: %v\n", i, err)
		} else {
			total += added
			fmt.Printf("[collect %d] +%d fills (%d total in file, %d distinct mints)\n", i, added, total, mints)
		}
		time.Sleep(*every)
	}
}
