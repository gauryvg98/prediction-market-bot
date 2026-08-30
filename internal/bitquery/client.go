// Package bitquery is a minimal Go client for Bitquery's Solana GraphQL API.
//
// It exists to pull the REAL World tape into the paper engine. Only what the bot
// needs: a POST to the EAP endpoint with a bearer token, and the two queries the
// tape reconstruction is built on (maker-side transfers, and round resolutions).
//
// Everything here is READ-ONLY market data. No key here can move funds; the
// token authenticates history and streaming, nothing else.
package bitquery

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

const Endpoint = "https://streaming.bitquery.io/eap"

// World program + token addresses the tape is filtered on.
const (
	PredictProgram = "prediCtPZCttYMvm2W3PtxmMxLmT1dtN7riU6Cxh6tM"
	CashMint       = "CASHx9KJUStyftLFWGvEVf59SGeG9sh5FfcnZMVPCASH"
	JanusMaker     = "JanusXpm3gsW3c9ErNoUgHppL8dGLvZKB7uekkJEYFP"
	BisonMaker     = "2DNbzPochEcyCcWMbL4d9S3u9QqQEj5bbe6cSZFvKsbh"
	Token2022      = "TokenzQdBNbLqP5VEhdkAS6EPFLC1PHnBqCXEpPxuEb"
	Operator       = "DDucv2DeUsTsg1rfAcWAnUSUVpqfdHEzxX66ARB2JYVg"
)

// Client talks to Bitquery. Construct with New; safe for reuse.
type Client struct {
	token string
	http  *http.Client
}

func New(token string) *Client {
	return &Client{token: token, http: &http.Client{Timeout: 60 * time.Second}}
}

// Query runs a GraphQL query and unmarshals data into out.
func (c *Client) Query(ctx context.Context, query string, out any) error {
	body, _ := json.Marshal(map[string]string{"query": query})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, Endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.token)
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("bitquery http %d: %s", resp.StatusCode, snippet(raw))
	}
	var env struct {
		Data   json.RawMessage   `json:"data"`
		Errors []json.RawMessage `json:"errors"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		return fmt.Errorf("decode: %w (body: %s)", err, snippet(raw))
	}
	if len(env.Errors) > 0 {
		return fmt.Errorf("bitquery graphql error: %s", env.Errors[0])
	}
	return json.Unmarshal(env.Data, out)
}

func snippet(b []byte) string {
	if len(b) > 240 {
		return string(b[:240])
	}
	return string(b)
}

// Transfer is one token movement, as the tape needs it.
type Transfer struct {
	TimeISO   string
	Signature string
	Signer    string
	Mint      string
	ProgramId string
	Amount    float64
	Sender    string
	Receiver  string
}

// makerTransfersQuery pulls both legs of trades routed through a maker in the
// last `minutes`: the CASH leg (taker -> maker) and the outcome-token leg
// (maker -> taker, freshly minted). Joining them by signature yields price.
func makerTransfersQuery(maker string, sinceISO string, limit int) string {
	return fmt.Sprintf(`{
      Solana {
        Transfers(
          where: {
            any: [
              { Transfer: { Receiver: { Address: { is: "%[1]s" } } } }
              { Transfer: { Sender:   { Address: { is: "%[1]s" } } } }
            ]
            Block: { Time: { since: "%[2]s" } }
          }
          limit: { count: %[3]d }
          orderBy: { descending: Block_Time }
        ) {
          Block { Time }
          Transaction { Signature Signer }
          Transfer {
            Amount
            Currency { MintAddress }
            Sender { Address }
            Receiver { Address }
          }
        }
      }
    }`, maker, sinceISO, limit)
}

type rawTransfers struct {
	Solana struct {
		Transfers []struct {
			Block       struct{ Time string }
			Transaction struct {
				Signature string
				Signer    string
			}
			Transfer struct {
				Amount   float64
				Currency struct{ MintAddress string }
				Sender   struct{ Address string }
				Receiver struct{ Address string }
			}
		}
	}
}

// MakerTransfers fetches recent trade legs routed through the given maker.
func (c *Client) MakerTransfers(ctx context.Context, maker, sinceISO string, limit int) ([]Transfer, error) {
	var r rawTransfers
	if err := c.Query(ctx, makerTransfersQuery(maker, sinceISO, limit), &r); err != nil {
		return nil, err
	}
	out := make([]Transfer, 0, len(r.Solana.Transfers))
	for _, t := range r.Solana.Transfers {
		out = append(out, Transfer{
			TimeISO: t.Block.Time, Signature: t.Transaction.Signature, Signer: t.Transaction.Signer,
			Mint: t.Transfer.Currency.MintAddress, Amount: t.Transfer.Amount,
			Sender: t.Transfer.Sender.Address, Receiver: t.Transfer.Receiver.Address,
		})
	}
	return out, nil
}
