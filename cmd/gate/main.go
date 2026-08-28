// Command gate demonstrates the entry decision on synthetic rounds.
//
// It exists to make the system's default visible: it skips. Most rounds do not
// clear the bar, and that is the design working, not a bug to tune away.
package main

import (
	"fmt"
	"math"

	"github.com/gauryvg98/prediction-bot/internal/market"
	"github.com/gauryvg98/prediction-bot/internal/signal"
	"github.com/gauryvg98/prediction-bot/internal/vol"
)

func scenario(name string, spot, strike, secs, iv, over, rv float64) {
	pUp := vol.NormCDF(math.Log(spot/strike) / (iv * math.Sqrt(secs)))
	in := signal.Inputs{
		Spot: spot, Strike: strike, SecondsToExpiry: secs, RealizedVol: rv,
		Fees: market.Fees{StakeRate: 0.025},
		Book: &market.Book{RoundID: name, ExpiryMs: int64(secs * 1000),
			Up:   []market.Level{{Price: pUp, Size: 1000}},
			Down: []market.Level{{Price: (1 + over) - pUp, Size: 1000}}},
	}
	d := signal.Evaluate(in, signal.DefaultParams())
	verdict := "SKIP"
	if d.Enter {
		verdict = "ENTER " + d.Side.String()
	}
	fmt.Printf("%-26s  %-11s  %s\n", name, verdict, d.Reason)
	if d.Enter {
		fmt.Printf("%-26s  %11s  implied %.0f%% ann / realized %.0f%% ann (%.2fx)\n",
			"", "", vol.Annualized(d.ImpliedVol)*100, vol.Annualized(d.RealizedVol)*100, d.VolRatio)
		fmt.Printf("%-26s  %11s  entry %.3f -> barrier %.3f (BTC %.0f), hedge must be <= %.3f\n",
			"", "", d.EntryPrice, d.BarrierPrice, d.BarrierSpot, d.RequiredHedg)
		fmt.Printf("%-26s  %11s  touch %.1f%% vs fair ceiling %.1f%%  ->  EDGE %+.1f%%\n",
			"", "", d.TouchProb*100, d.FairCeiling*100, d.Edge*100)
	}
	fmt.Println()
}

func main() {
	const K, IV = 80000.0, 1.515e-4 // ~85% annualized implied
	fmt.Printf("15-minute BTC round, strike $%.0f, maker spread 2.5%%\n\n", K)

	scenario("vol expansion, mid-round", 79800, K, 600, IV, 0.025, 2.0e-4)
	scenario("quiet market", 79800, K, 600, IV, 0.025, 1.0e-4)
	scenario("at the money", 80000, K, 600, IV, 0.025, 2.0e-4)
	scenario("too late in the round", 79800, K, 30, IV, 0.025, 3.0e-4)
	scenario("leg already too expensive", 80600, K, 600, IV, 0.025, 5.0e-4)
	scenario("violent expansion", 79700, K, 700, IV, 0.025, 3.0e-4)
}
