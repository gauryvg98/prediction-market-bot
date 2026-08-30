package paper

import (
	"testing"

	"github.com/gauryvg98/prediction-bot/internal/market"
	"github.com/gauryvg98/prediction-bot/internal/signal"
)

// permissive params so tests exercise the lifecycle, not the (separately tested) gate.
func testCfg() Config {
	c := DefaultConfig()
	c.Fees = market.Fees{}
	c.Signal = signal.Params{MinVolRatio: 0, MinTouchEdge: -1, MinEntryPrice: 0.05,
		MaxEntryPrice: 0.95, MinSecondsLeft: 10, MinLockEdge: 0.02}
	c.Stake = 10
	return c
}

func book(id string, up, dn float64, secsLeft float64) *market.Book {
	ms := int64(secsLeft * 1000)
	return &market.Book{RoundID: id, TsMs: 0, ExpiryMs: ms,
		Up: []market.Level{{up, 1e6}}, Down: []market.Level{{dn, 1e6}}}
}

func tick(id string, up, dn, spot, strike, secs, rv float64) Tick {
	return Tick{RoundID: id, Book: book(id, up, dn, secs), Spot: spot, Strike: strike,
		SecondsToExpiry: secs, RealizedVol: rv}
}

// A round that enters cheap, then hedges when the other side falls: must MERGE
// with a positive banked P&L and zero residual.
func TestLockThenMergeBanksProfit(t *testing.T) {
	e := New(testCfg())
	// enter DOWN at 0.25 (spot above strike, so DOWN is the cheap away side)
	e.OnTick(tick("R1", 0.81, 0.25, 110500, 110000, 200, 3e-4))
	// spot reverts below strike: DOWN leg now winning, UP offered cheap -> hedge
	evs := e.OnTick(tick("R1", 0.30, 0.75, 109500, 110000, 120, 3e-4))
	var merged *Event
	for i := range evs {
		if evs[i].Kind == Merge {
			merged = &evs[i]
		}
	}
	if merged == nil {
		t.Fatalf("expected a MERGE, got %v", evs)
	}
	if merged.PnL <= 0 {
		t.Errorf("merge should bank positive P&L, got %+.4f", merged.PnL)
	}
	if e.Ledger.Locks != 1 || e.Ledger.RealizedPn <= 0 {
		t.Errorf("ledger: locks=%d pnl=%+.4f", e.Ledger.Locks, e.Ledger.RealizedPn)
	}
}

// A round that enters and never gets a hedge must expire naked and, when it
// loses, book the ENTIRE stake -- the tail the strategy lives or dies on.
func TestNakedLossBooksWholeStake(t *testing.T) {
	e := New(testCfg())
	e.OnTick(tick("R2", 0.81, 0.25, 110500, 110000, 200, 3e-4)) // enter DOWN@0.25
	// price never comes back; deadline passes -> Abandon -> Naked
	e.OnTick(tick("R2", 0.90, 0.14, 111500, 110000, 15, 3e-4))
	evs := e.OnResolve(Resolution{RoundID: "R2", Winner: market.Up}) // DOWN loses
	if len(evs) != 1 || evs[0].Kind != Settle {
		t.Fatalf("expected SETTLE, got %v", evs)
	}
	if evs[0].PnL != -10 {
		t.Errorf("naked loss should be the whole $10 stake, got %+.4f", evs[0].PnL)
	}
	if e.Ledger.NakedLoss != 1 || e.Ledger.WorstRound != -10 {
		t.Errorf("ledger: nakedLoss=%d worst=%+.2f", e.Ledger.NakedLoss, e.Ledger.WorstRound)
	}
}

// The gate's default is NO: a round with no volatility edge must never enter.
func TestNoEdgeNeverEnters(t *testing.T) {
	c := DefaultConfig() // strict defaults
	e := New(c)
	for s := 250.0; s > 30; s -= 5 {
		e.OnTick(tick("R3", 0.53, 0.53, 110000, 110000, s, 1e-6)) // ATM, ~no vol
	}
	if e.Ledger.Entered != 0 {
		t.Errorf("strict gate should not enter a no-edge round, entered=%d", e.Ledger.Entered)
	}
}

// Merging must leave no residual when payouts are equalized -- the property that
// makes the whole position recyclable, which matters most on a small bankroll.
func TestEqualizedHedgeLeavesNoResidual(t *testing.T) {
	e := New(testCfg())
	e.OnTick(tick("R4", 0.80, 0.25, 110500, 110000, 200, 3e-4))
	e.OnTick(tick("R4", 0.30, 0.72, 109500, 110000, 120, 3e-4))
	for _, ev := range e.Ledger.Events {
		if ev.Kind == Merge && ev.Note != "residual=0.0000" {
			t.Errorf("equalized hedge should leave zero residual, got %q", ev.Note)
		}
	}
}
