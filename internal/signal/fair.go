// fair.go — the entry model: is the maker's price lagging the true (Chainlink)
// probability, and by enough to be worth taking?
//
// The strategy in one comparison. Momentum moves BTC; the true probability of
// each side moves with it. If the maker is slow to reprice, a side sells BELOW
// its true probability -- that is the cheap, momentum-favored leg. Buy it. Later,
// once the other side is cheap enough that the pair costs under $1, lock.
//
// If the maker tracks the true price tightly (fair ~= maker every tick), this
// returns SKIP forever -- which is the honest verdict that there is no edge.
package signal

import (
	"fmt"
	"math"

	"github.com/gauryvg98/prediction-bot/internal/market"
	"github.com/gauryvg98/prediction-bot/internal/vol"
)

// FairProbUp is the true P(spot_T > strike) under driftless GBM, short-horizon
// form (the sigma^2*T/2 drift term is negligible over minutes). This is the
// number the maker's UP price SHOULD equal if it priced the current spot fairly.
func FairProbUp(spot, strike, secondsToExpiry, sigma float64) float64 {
	if sigma <= 0 || secondsToExpiry <= 0 || spot <= 0 || strike <= 0 {
		return 0.5
	}
	return vol.NormCDF(math.Log(spot/strike) / (sigma * math.Sqrt(secondsToExpiry)))
}

// FairParams gate the fair-edge entry.
type FairParams struct {
	MinEdge       float64 // maker must be this far below true fair to enter (covers vig + noise)
	MinEntryPrice float64 // don't chase a leg already near 0
	MaxEntryPrice float64 // don't buy a leg already near-certain (no room to lock)
	MinSeconds    float64 // the move needs time to be lockable
}

func DefaultFairParams() FairParams {
	return FairParams{MinEdge: 0.04, MinEntryPrice: 0.08, MaxEntryPrice: 0.75, MinSeconds: 20}
}

// FairDecision is the model's answer plus every number behind it.
type FairDecision struct {
	Enter   bool
	Side    market.Outcome
	FairUp  float64 // true P(up) from Chainlink
	Maker   float64 // maker's price for the chosen side
	Edge    float64 // fair - maker on the chosen side (how underpriced)
	Reason  string
}

// EvaluateFair decides one tick: enter the side the maker underprices most,
// if that underpricing clears MinEdge and the price sits in the entry band.
func EvaluateFair(spot, strike, secondsToExpiry, sigma, makerUp, makerDown float64, p FairParams) FairDecision {
	if secondsToExpiry < p.MinSeconds {
		return FairDecision{Reason: fmt.Sprintf("too-late %.0fs", secondsToExpiry)}
	}
	fairUp := FairProbUp(spot, strike, secondsToExpiry, sigma)
	edgeUp := fairUp - makerUp       // UP sold below its true probability
	edgeDown := (1 - fairUp) - makerDown

	side, maker, edge := market.Up, makerUp, edgeUp
	if edgeDown > edgeUp {
		side, maker, edge = market.Down, makerDown, edgeDown
	}
	d := FairDecision{Side: side, FairUp: fairUp, Maker: maker, Edge: edge}
	if maker < p.MinEntryPrice || maker > p.MaxEntryPrice {
		d.Reason = fmt.Sprintf("%s price %.3f out of band", side, maker)
		return d
	}
	if edge < p.MinEdge {
		d.Reason = fmt.Sprintf("%s edge %.3f < %.3f (maker not lagging enough)", side, edge, p.MinEdge)
		return d
	}
	d.Enter = true
	d.Reason = fmt.Sprintf("%s underpriced: fair-implied %.3f vs maker %.3f, edge %.3f",
		side, fairIfSide(fairUp, side), maker, edge)
	return d
}

func fairIfSide(fairUp float64, side market.Outcome) float64 {
	if side == market.Up {
		return fairUp
	}
	return 1 - fairUp
}
