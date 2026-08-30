package feed

import (
	"fmt"
	"math"
	"math/rand"

	"github.com/gauryvg98/prediction-bot/internal/market"
	"github.com/gauryvg98/prediction-bot/internal/paper"
	"github.com/gauryvg98/prediction-bot/internal/vol"
)

// SynthConfig parameterizes the simulator.
//
// IMPORTANT, stated plainly: this simulator EXERCISES the machine; it does not
// PROVE the edge. With ImpliedBias = 1.0 the maker prices volatility correctly,
// so the strategy finds no edge and mostly skips -- the honest null, and the
// correct default. ImpliedBias < 1 makes the maker under-price movement (quotes
// as if the market is calmer than it is), which is the specific mispricing the
// strategy hunts; set it below 1 to watch the machinery capture a *simulated*
// inefficiency. Whether the REAL maker actually mis-prices is a question only the
// real tape answers -- never this file.
type SynthConfig struct {
	Rounds       int
	DurationSec  float64 // round length (300 = 5 min, 900 = 15 min)
	StepSec      float64 // observation cadence
	Spot0        float64 // starting BTC price
	TrueVol      float64 // actual path volatility, per sqrt-second
	ImpliedBias  float64 // maker implied vol = TrueVol * ImpliedBias (1.0 = fair)
	Overround    float64 // maker two-sided spread S-1
	VolClusterP  float64 // per-round probability of a high-vol ("swing") regime
	VolClusterX  float64 // vol multiplier during a swing regime
	Seed         int64
}

// DefaultSynth is fair by default (ImpliedBias 1.0): the strategy should mostly
// skip, and any lock rate should sit at the fair ceiling. That is the simulator
// telling the truth, not a disappointment.
func DefaultSynth() SynthConfig {
	return SynthConfig{
		Rounds: 200, DurationSec: 300, StepSec: 5, Spot0: 110_000,
		TrueVol: 1.6e-4, ImpliedBias: 1.0, Overround: 0.06,
		VolClusterP: 0.25, VolClusterX: 2.2, Seed: 1,
	}
}

// Synthetic is a deterministic GBM round generator.
type Synthetic struct {
	cfg  SynthConfig
	rng  *rand.Rand
	spot float64
	n    int
}

func NewSynthetic(cfg SynthConfig) *Synthetic {
	return &Synthetic{cfg: cfg, rng: rand.New(rand.NewSource(cfg.Seed)), spot: cfg.Spot0}
}

// makerBook models the two-sided quote: the fair binary price at the maker's
// IMPLIED vol, split into UP/DOWN with the overround shared evenly.
func makerBook(id string, spot, strike, secsLeft, impliedVol, over float64, tsMs, expMs int64) *market.Book {
	pUp := 0.5
	if secsLeft > 0 && impliedVol > 0 {
		pUp = vol.NormCDF(math.Log(spot/strike) / (impliedVol * math.Sqrt(secsLeft)))
	}
	half := over / 2
	up := clip01(pUp + half)
	dn := clip01((1 - pUp) + half)
	return &market.Book{
		RoundID: id, TsMs: tsMs, ExpiryMs: expMs,
		Up:   []market.Level{{Price: up, Size: 1e6}},
		Down: []market.Level{{Price: dn, Size: 1e6}},
	}
}

func clip01(x float64) float64 {
	if x < 1e-4 {
		return 1e-4
	}
	if x > 1-1e-4 {
		return 1 - 1e-4
	}
	return x
}

func (s *Synthetic) Next() (*Round, bool) {
	if s.n >= s.cfg.Rounds {
		return nil, false
	}
	s.n++
	c := s.cfg
	strike := s.spot // strike = oracle price at open, matching World's structure
	trueVol := c.TrueVol
	if s.rng.Float64() < c.VolClusterP {
		trueVol *= c.VolClusterX // a swing regime this round
	}
	impliedVol := trueVol * c.ImpliedBias

	id := fmt.Sprintf("SYN-%04d", s.n)
	steps := int(c.DurationSec / c.StepSec)
	spot := s.spot
	baseMs := int64(s.n) * int64(c.DurationSec) * 1000
	var ticks []paper.Tick
	for i := 0; i <= steps; i++ {
		secsLeft := c.DurationSec - float64(i)*c.StepSec
		tsMs := baseMs + int64(float64(i)*c.StepSec*1000)
		expMs := baseMs + int64(c.DurationSec*1000)
		// The strategy's realized-vol estimate is the true path vol (a perfect
		// estimator here; the real bot measures it from the spot stream).
		ticks = append(ticks, paper.Tick{
			RoundID: id, Spot: spot, Strike: strike, SecondsToExpiry: secsLeft,
			RealizedVol: trueVol,
			Book:        makerBook(id, spot, strike, secsLeft, impliedVol, c.Overround, tsMs, expMs),
		})
		// advance one GBM step (driftless)
		if i < steps {
			z := s.rng.NormFloat64()
			spot *= math.Exp(-0.5*trueVol*trueVol*c.StepSec + trueVol*math.Sqrt(c.StepSec)*z)
		}
	}
	winner := market.Up
	if spot <= strike {
		winner = market.Down
	}
	s.spot = spot // carry price across rounds -- a continuous tape, not resets
	return &Round{
		Ticks:      ticks,
		Resolution: paper.Resolution{RoundID: id, Winner: winner, SettlePrice: spot},
	}, true
}
