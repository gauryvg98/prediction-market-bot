package oracle

import "sort"

// pricePoint is one (timeMs, price) sample.
type pricePoint struct {
	ms  int64
	val float64
}

// series is a time-sorted price history queryable by instant.
type series struct {
	pts []pricePoint
}

func (s *series) add(ms int64, val float64) {
	// append; keep sorted (RTDS delivers roughly in order, dumps are ordered)
	if n := len(s.pts); n > 0 && s.pts[n-1].ms <= ms {
		s.pts = append(s.pts, pricePoint{ms, val})
		return
	}
	i := sort.Search(len(s.pts), func(i int) bool { return s.pts[i].ms > ms })
	s.pts = append(s.pts, pricePoint{})
	copy(s.pts[i+1:], s.pts[i:])
	s.pts[i] = pricePoint{ms, val}
}

// at returns the price at or just before ms, and whether the history covers it.
func (s *series) at(ms int64) (float64, bool) {
	if len(s.pts) == 0 || ms < s.pts[0].ms {
		return 0, false
	}
	i := sort.Search(len(s.pts), func(i int) bool { return s.pts[i].ms > ms })
	if i == 0 {
		return 0, false
	}
	return s.pts[i-1].val, true
}

func (s *series) span() (int64, int64, bool) {
	if len(s.pts) == 0 {
		return 0, 0, false
	}
	return s.pts[0].ms, s.pts[len(s.pts)-1].ms, true
}

// ChainlinkAt / BinanceAt query the recorded history (needs recordHistory=true).
func (c *Client) ChainlinkAt(ms int64) (float64, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.clHist.at(ms)
}
func (c *Client) BinanceAt(ms int64) (float64, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.bnHist.at(ms)
}

// ChainlinkSpan reports the [min,max] ms the Chainlink history covers.
func (c *Client) ChainlinkSpan() (int64, int64, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.clHist.span()
}

// RealizedVol estimates per-sqrt-second vol from Chainlink over the trailing
// `windowSec` before ms -- the input the vol gate compares against implied.
func (c *Client) RealizedVol(ms int64, windowSec int) float64 {
	c.mu.RLock()
	defer c.mu.RUnlock()
	var rets []float64
	prev, ok := c.clHist.at(ms - int64(windowSec)*1000)
	if !ok {
		return 0
	}
	for k := windowSec - 5; k >= 0; k -= 5 {
		p, ok := c.clHist.at(ms - int64(k)*1000)
		if ok && prev > 0 && p > 0 {
			rets = append(rets, (p-prev)/prev)
			prev = p
		}
	}
	if len(rets) < 3 {
		return 0
	}
	var mean float64
	for _, r := range rets {
		mean += r
	}
	mean /= float64(len(rets))
	var v float64
	for _, r := range rets {
		v += (r - mean) * (r - mean)
	}
	v /= float64(len(rets) - 1)
	return sqrt(v) / sqrt(5) // 5s-return stdev -> per-sqrt-second
}

func sqrt(x float64) float64 {
	if x <= 0 {
		return 0
	}
	g := x
	for i := 0; i < 40; i++ {
		g = 0.5 * (g + x/g)
	}
	return g
}
