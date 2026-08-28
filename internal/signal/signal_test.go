package signal

import (
	"math"
	"strings"
	"testing"

	"github.com/gauryvg98/prediction-bot/internal/market"
	"github.com/gauryvg98/prediction-bot/internal/vol"
)

// Build a book whose UP leg prices at exactly `pUp` given a chosen implied vol,
// so every scenario is internally consistent rather than hand-waved.
func scenario(spot, strike, seconds, impliedVol, overround, realizedVol float64) Inputs {
	pUp := vol.NormCDF(math.Log(spot/strike) / (impliedVol * math.Sqrt(seconds)))
	pDown := (1 + overround) - pUp
	return Inputs{
		Spot: spot, Strike: strike, SecondsToExpiry: seconds,
		RealizedVol: realizedVol,
		Fees:        market.Fees{StakeRate: 0.025},
		Book: &market.Book{
			RoundID: "r1", ExpiryMs: int64(seconds * 1000),
			Up:   []market.Level{{Price: pUp, Size: 1000}},
			Down: []market.Level{{Price: pDown, Size: 1000}},
		},
	}
}

const (
	strike     = 80000.0
	impliedVol = 1.515e-4 // ~85% annualized -- plausible for short-dated BTC
)

func TestEntersWhenRealizedVolBeatsImplied(t *testing.T) {
	// Spot below strike, so UP is the cheap underdog; realized vol running
	// hotter than implied means the market is underpricing movement.
	in := scenario(79800, strike, 600, impliedVol, 0.025, 2.0e-4)
	d := Evaluate(in, DefaultParams())
	if !d.Enter {
		t.Fatalf("expected entry, skipped: %s", d.Reason)
	}
	if d.Side != market.Up {
		t.Fatalf("expected the cheap UP leg, got %v", d.Side)
	}
	if d.VolRatio <= 1 {
		t.Fatalf("entry requires realized > implied, got ratio %v", d.VolRatio)
	}
	if d.TouchProb <= d.FairCeiling {
		t.Fatalf("entry requires beating the fair ceiling: %v vs %v",
			d.TouchProb, d.FairCeiling)
	}
}

func TestSkipsWhenTheMarketIsNotUnderpricingMovement(t *testing.T) {
	// Same setup, but realized vol BELOW implied. This is the common case, and
	// the correct answer is to sit out.
	in := scenario(79800, strike, 600, impliedVol, 0.025, 1.0e-4)
	d := Evaluate(in, DefaultParams())
	if d.Enter {
		t.Fatal("must not enter when the market prices movement richly")
	}
	if !strings.Contains(d.Reason, "vol ratio") {
		t.Fatalf("skip should name the vol ratio, got %q", d.Reason)
	}
}

func TestSkipsLateInTheRound(t *testing.T) {
	// The move needs time to happen; a late entry cannot travel to the barrier.
	in := scenario(79800, strike, 30, impliedVol, 0.025, 3.0e-4)
	if d := Evaluate(in, DefaultParams()); d.Enter {
		t.Fatal("must not enter with 30s left")
	}
}

func TestSkipsAnEntryTooRichToHedge(t *testing.T) {
	// Spot far above strike makes UP expensive; buying a near-favourite leaves
	// no room for any hedge to lock.
	in := scenario(80600, strike, 600, impliedVol, 0.025, 5.0e-4)
	p := DefaultParams()
	d := Evaluate(in, p)
	if d.Enter && d.EntryPrice > p.MaxEntryPrice {
		t.Fatalf("entered above the price band at %v", d.EntryPrice)
	}
}

func TestDefaultIsToSkip(t *testing.T) {
	if d := Evaluate(Inputs{}, DefaultParams()); d.Enter {
		t.Fatal("an empty input must never produce a trade")
	}
}

func TestIncompleteBookIsSkipped(t *testing.T) {
	in := scenario(79800, strike, 600, impliedVol, 0.025, 2.0e-4)
	in.Book.Down = nil
	if d := Evaluate(in, DefaultParams()); d.Enter {
		t.Fatal("a one-sided book cannot be priced")
	}
}

func TestZeroRealizedVolIsSkipped(t *testing.T) {
	in := scenario(79800, strike, 600, impliedVol, 0.025, 0)
	if d := Evaluate(in, DefaultParams()); d.Enter {
		t.Fatal("no vol estimate means no trade")
	}
}

func TestDecisionExplainsItself(t *testing.T) {
	// A skip that cannot say why cannot be tuned, and the per-round record is
	// what the measurement phase consumes.
	d := Evaluate(scenario(79800, strike, 600, impliedVol, 0.025, 1.0e-4), DefaultParams())
	if d.Reason == "" {
		t.Fatal("every decision must carry a reason")
	}
}

func TestStricterEdgeRequirementSkipsMore(t *testing.T) {
	in := scenario(79800, strike, 600, impliedVol, 0.025, 2.0e-4)
	loose := DefaultParams()
	strict := DefaultParams()
	strict.MinTouchEdge = 0.95 // effectively unreachable
	if !Evaluate(in, loose).Enter {
		t.Fatal("loose params should enter")
	}
	if Evaluate(in, strict).Enter {
		t.Fatal("a stricter edge bar must skip the same setup")
	}
}
