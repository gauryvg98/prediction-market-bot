package bitquery

import "testing"

// priceTx is the pure heart of tape reconstruction: given one transaction's
// legs, isolate the outcome-token leg (Token-2022, non-CASH) and price it
// against the CASH paid. These tests pin that logic without any network.

func leg(mint, program string, amt float64, sender, receiver, signer string) Transfer {
	return Transfer{Mint: mint, ProgramId: program, Amount: amt,
		Sender: sender, Receiver: receiver, Signer: signer, Signature: "SIG", TimeISO: "T"}
}

func TestPriceTxIsolatesTheOutcomeLeg(t *testing.T) {
	// A realistic multi-hop buy: taker pays CASH, receives outcome tokens, with
	// a USDC ramp hop and a wrapped-SOL leg as noise that MUST be ignored.
	taker := "TAKER"
	legs := []Transfer{
		leg(CashMint, Token2022, 10.0, taker, "vault", taker),                 // CASH paid
		leg("USDCmint", "TokenkegQfeZyiNwAJbNbGKPFXCWuBvf9Ss623VQ5DA", 10.0, "vault", "ramp", taker), // classic SPL ramp -> ignore
		leg("So11111111111111111111111111111111111111112", "system", 0.5, taker, "wsol", taker),      // SOL -> ignore
		leg("OUTCOMEmint", Token2022, 40.0, "maker", taker, taker),            // outcome tokens received
	}
	f, ok := priceTx(legs)
	if !ok {
		t.Fatal("expected a priced fill")
	}
	if f.Mint != "OUTCOMEmint" {
		t.Errorf("picked wrong leg: %s", f.Mint)
	}
	if got := f.Price(); got < 0.24 || got > 0.26 {
		t.Errorf("price = %.4f, want ~0.25 (10 CASH / 40 tokens)", got)
	}
	if !f.Buy {
		t.Error("taker received the outcome tokens -> should be a BUY")
	}
}

func TestPriceTxRejectsWhenNoOutcomeLeg(t *testing.T) {
	// Only CASH and USDC -- no Token-2022 outcome leg. Must reconstruct nothing
	// rather than mis-price a ramp hop as a trade.
	legs := []Transfer{
		leg(CashMint, Token2022, 5.0, "a", "b", "a"),
		leg("USDCmint", "TokenkegQfeZyiNwAJbNbGKPFXCWuBvf9Ss623VQ5DA", 5.0, "b", "c", "a"),
	}
	if _, ok := priceTx(legs); ok {
		t.Error("should not price a transaction with no outcome-token leg")
	}
}

func TestPriceTxRejectsOutOfBandPrice(t *testing.T) {
	// An outcome leg implying price >= 1 is not a real binary fill.
	legs := []Transfer{
		leg(CashMint, Token2022, 10.0, "t", "v", "t"),
		leg("OUTCOMEmint", Token2022, 5.0, "m", "t", "t"), // 10/5 = 2.0, impossible
	}
	if _, ok := priceTx(legs); ok {
		t.Error("price 2.0 is out of (0,1) and must be rejected")
	}
}
