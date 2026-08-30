package feed

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/gauryvg98/prediction-bot/internal/bitquery"
)

// storedFill is one real fill persisted to the local tape file.
type storedFill struct {
	TimeISO string  `json:"t"`
	Sig     string  `json:"sig"`
	Mint    string  `json:"mint"`
	Cash    float64 `json:"cash"`
	Tokens  float64 `json:"tok"`
	Buy     bool    `json:"buy"`
}

func key(f bitquery.Fill) string { return f.Sig + "|" + f.Mint }

// Collect runs one pull and APPENDS newly-seen real fills to path (JSONL),
// deduped by signature+mint. Returns how many new fills were written and the
// running distinct-mint count in the file.
func Collect(ctx context.Context, bq *bitquery.Client, path string, minutes int) (added, mints int, err error) {
	seen, _ := loadKeys(path)
	now := time.Now().UnixMilli()
	since := time.UnixMilli(now).UTC().Add(-time.Duration(minutes) * time.Minute).Format(time.RFC3339)
	till := time.UnixMilli(now + 60000).UTC().Format(time.RFC3339)
	fills, err := bq.RecentFillsBatched(ctx, since, till, 800)
	if err != nil {
		return 0, 0, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return 0, 0, err
	}
	defer f.Close()
	w := bufio.NewWriter(f)
	for _, fl := range fills {
		if seen[key(fl)] {
			continue
		}
		seen[key(fl)] = true
		b, _ := json.Marshal(storedFill{fl.TimeISO, fl.Sig, fl.Mint, fl.Cash, fl.Tokens, fl.Buy})
		fmt.Fprintln(w, string(b))
		added++
	}
	w.Flush()
	all, _ := loadFills(path)
	ms := map[string]bool{}
	for _, fl := range all {
		ms[fl.Mint] = true
	}
	return added, len(ms), nil
}

func loadKeys(path string) (map[string]bool, error) {
	fills, err := loadFills(path)
	m := map[string]bool{}
	for _, f := range fills {
		m[f.Sig+"|"+f.Mint] = true
	}
	return m, err
}

func loadFills(path string) ([]bitquery.Fill, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []bitquery.Fill
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		var s storedFill
		if json.Unmarshal(sc.Bytes(), &s) == nil {
			out = append(out, bitquery.Fill{TimeISO: s.TimeISO, Sig: s.Sig, Mint: s.Mint,
				Cash: s.Cash, Tokens: s.Tokens, Buy: s.Buy})
		}
	}
	return out, nil
}

// LoadFile builds rounds from an accumulated local tape file -- the replay path.
func LoadFile(path string, cadenceSec float64) (*Bitquery, error) {
	fills, err := loadFills(path)
	if err != nil {
		return nil, err
	}
	series := map[string][]bitquery.Fill{}
	for _, f := range fills {
		series[f.Mint] = append(series[f.Mint], f)
	}
	fmt.Printf("[feed] %d accumulated fills across %d mints\n", len(fills), len(series))
	pairs := pairComplementary(series)
	fmt.Printf("[feed] paired %d round(s)\n", len(pairs))
	var rounds []*Round
	for _, pr := range pairs {
		if r, ok := buildRound(pr, cadenceSec); ok {
			rounds = append(rounds, r)
		}
	}
	fmt.Printf("[feed] built %d usable round(s)\n\n", len(rounds))
	if len(rounds) == 0 {
		return nil, fmt.Errorf("not enough accumulated data yet -- let the collector run longer")
	}
	return &Bitquery{rounds: rounds}, nil
}
