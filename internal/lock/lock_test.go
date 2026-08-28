package lock

import (
	"math"
	"testing"

	"github.com/gauryvg98/prediction-bot/internal/market"
)

func approx(t *testing.T, got, want, tol float64, what string) {
	t.Helper()
	if math.Abs(got-want) > tol {
		t.Fatalf("%s: got %v, want %v", what, got, want)
	}
}

// A real Phantom/World fill: equal ~$4.60 stakes at unequal prices, so the
// payouts came out unequal. Everything here is calibrated against it.
var (
	realDown = Leg{market.Down, 4.62, 15.26}
	realUp   = Leg{market.Up, 4.59, 18.22}
	real     = Position{Up: &realUp, Down: &realDown}
)

func TestTheRealPositionIsGenuinelyLocked(t *testing.T) {
	if !real.IsLocked() {
		t.Fatal("both legs on with S<1 must read as locked")
	}
	approx(t, real.Cost(), 9.21, 1e-9, "cost")
	approx(t, real.Floor(), 6.05, 1e-9, "floor")     // worst branch: DOWN wins
	approx(t, real.Ceiling(), 9.01, 1e-9, "ceiling") // best branch: UP wins
}

func TestFloorIsTheWorstBranchNotTheAverage(t *testing.T) {
	up := Leg{market.Up, 5, 50}
	down := Leg{market.Down, 5, 9}
	p := Position{Up: &up, Down: &down}
	if p.Floor() >= 0 || p.IsLocked() {
		t.Fatal("a position can look great on average and still not be locked")
	}
}

func TestMatchedPairsAreCashOnDemand(t *testing.T) {
	// prediCt mints/burns complete sets at par, permissionlessly -- so a matched
	// pair IS a dollar, callable now rather than at resolution.
	approx(t, real.CompleteSets(), 15.26, 1e-9, "complete sets")
	approx(t, real.RealizedOnMerge(), real.Floor(), 1e-9, "merge == floor, sooner")
}

func TestEqualDollarsLeavesAnUnmergeableResidual(t *testing.T) {
	approx(t, real.Residual(), 2.96, 1e-9, "residual")
}

func TestEqualizingPayoutsBeatsEqualDollars(t *testing.T) {
	h := realUp.Price()
	best := EqualizingHedgeCost(realDown.Payout, h)
	if best >= realUp.Cost {
		t.Fatalf("the real hedge (%v) should have been oversized vs %v", realUp.Cost, best)
	}
	improved := FloorAtHedgeCost(realDown, h, best)
	actual := FloorAtHedgeCost(realDown, h, realUp.Cost)
	approx(t, actual, real.Floor(), 1e-9, "actual floor")
	if improved <= actual {
		t.Fatalf("equalizing should raise the floor: %v vs %v", improved, actual)
	}
}

func TestEqualizingIsAGenuineMaximum(t *testing.T) {
	h := realUp.Price()
	best := EqualizingHedgeCost(realDown.Payout, h)
	peak := FloorAtHedgeCost(realDown, h, best)
	for _, d := range []float64{-2, -0.5, -0.05, 0.05, 0.5, 2} {
		if got := FloorAtHedgeCost(realDown, h, best+d); got > peak+1e-12 {
			t.Fatalf("floor at %v beat the claimed optimum", best+d)
		}
	}
}

func TestSingleLegHasNoCompleteSets(t *testing.T) {
	if (Position{Down: &realDown}).CompleteSets() != 0 {
		t.Fatal("one leg cannot form a complete set")
	}
}

func TestHedgeIsOnlyOfferedOnceTheEntryLegIsWinning(t *testing.T) {
	// The central identity: the barrier always sits ABOVE what we paid.
	f := market.Fees{StakeRate: 0.025}
	b := BarrierPrice(0.25, 0.025, f, 0.02)
	if b <= 0.25 {
		t.Fatalf("barrier %v must exceed the entry price", b)
	}
	// Widening the spread pushes the escape further away.
	if BarrierPrice(0.25, 0.10, f, 0.02) <= b {
		t.Fatal("a wider vig must move the barrier further out")
	}
}

func TestLockPaysTheSameEitherWay(t *testing.T) {
	pnl := LockPnL(100, 0.25, 0.33, market.Fees{})
	approx(t, pnl, 100-100*(0.25+0.33), 1e-9, "lock pnl")
}

func TestRequiredHedgeIsTheComplement(t *testing.T) {
	approx(t, RequiredHedgePrice(0.25, market.Fees{}, 0), 0.75, 1e-12, "required")
}
