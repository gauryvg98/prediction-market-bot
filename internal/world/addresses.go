// Package world holds the on-chain constants for World -- the Solana protocol
// behind Phantom's BTC up/down markets. Phantom is only a front-end; nothing
// here talks to it.
//
// SOURCE AND TRUST. These come from public independent research
// (github.com/chainstacklabs/world-xyz-research), verified 2026-07-10, which
// reverses World from mainnet and publishes a reproduction command per claim. It
// is NOT official. Every address must be re-verified against mainnet before it
// carries money -- and re-verified periodically, because all four programs are
// upgradeable and none is immutable. `cmd/probe` exists to do exactly that.
package world

// Programs.
const (
	// PrediCt is World's market program: split/merge complete sets,
	// determine_outcome, redeem_outcome_for_user, close_market. Anchor, IDL
	// withheld -- which does not matter, since price is recoverable from token
	// balance deltas alone.
	PrediCt = "prediCtPZCttYMvm2W3PtxmMxLmT1dtN7riU6Cxh6tM"

	// JanusFI is a market-maker AMM that CPIs prediCt.split/merge. It shares
	// prediCt's upgrade key, so it is World's in-house maker.
	JanusFI = "JanusXpm3gsW3c9ErNoUgHppL8dGLvZKB7uekkJEYFP"

	// BisonFI is the second maker, independently controlled.
	BisonFI = "2DNbzPochEcyCcWMbL4d9S3u9QqQEj5bbe6cSZFvKsbh"

	// DFlow is the RFQ router connecting a wallet to the makers. Third-party.
	// Single-leg buys and sells route through here, and its Trading API is the
	// gate on all live execution.
	DFlow = "DF1ow4tspfHX9JwWJsAb9epbkA8hmpSEAtxXy1V27QBH"
)

// CASH is the collateral: a Token-2022 stablecoin issued by Bridge (Stripe).
//
// NOT a World asset. The issuer holds freeze and clawback authority over every
// CASH account, independent of anything World does.
const (
	CashMint     = "CASHx9KJUStyftLFWGvEVf59SGeG9sh5FfcnZMVPCASH"
	CashDecimals = 6
)

// OperatorKey creates, resolves, pays out, and closes every market.
//
// Worth stating plainly: `determine_outcome` writes a 2-byte winner and carries
// no oracle account, price feed, or proof. Chainlink feeds this key off-chain;
// the chain cannot tell a Chainlink outcome from any other use of the key. There
// is no dispute window and no refund instruction. Fund a bot wallet with the
// working bankroll and nothing more.
const OperatorKey = "DDucv2DeUsTsg1rfAcWAnUSUVpqfdHEzxX66ARB2JYVg"

// Anchor instruction discriminators, sha256("global:<snake_name>")[:8].
// Used to classify instructions without the withheld IDL.
var (
	DiscInitializeMarket = [8]byte{0x23, 0x23, 0xbd, 0xc1, 0x9b, 0x30, 0xaa, 0xcb}
	DiscSplit            = [8]byte{0x7c, 0xbd, 0x1b, 0x2b, 0xd8, 0x28, 0x93, 0x42}
	DiscMerge            = [8]byte{0x94, 0x8d, 0xec, 0x2f, 0xae, 0x7e, 0x45, 0x6f}
	DiscDetermineOutcome = [8]byte{0x18, 0x71, 0x16, 0x66, 0x31, 0x95, 0x6d, 0x52}
	DiscCloseMarket      = [8]byte{0x58, 0x9a, 0xf8, 0xba, 0x30, 0x0e, 0x7b, 0xf4}
	DiscMarketAccount    = [8]byte{0xdb, 0xbe, 0xd5, 0x37, 0x00, 0xe3, 0xc6, 0x9a}
)

// Market account layout (prediCt-owned, 320 bytes). Offsets 8-168 are directly
// verified; the tail is inferred and must be confirmed before being relied on.
const (
	MarketAccountSize = 320

	OffDiscriminator = 0   // 8  bytes
	OffCreator       = 8   // 32 -- the operator key
	OffCashMint      = 40  // 32
	OffYesMint       = 72  // 32 -- the UP outcome mint
	OffNoMint        = 104 // 32 -- the DOWN outcome mint
	OffCashVault     = 136 // 32
	OffConfig        = 168 // ~32, partially decoded
	OffStartMs       = 256 // 8, milliseconds  (INFERRED -- verify)
	OffEndMs         = 264 // 8, milliseconds  (INFERRED -- verify)
)

// The protocol invariant the whole strategy rests on:
//
//	1 CASH  <->  1 UP + 1 DOWN     (split / merge: exact, reversible, fee-free)
//	at resolution: winning token -> 1 CASH, losing token -> 0
//
// prediCt contains no pricing math at all. It is a pure conditional-token vault,
// and split/merge are PERMISSIONLESS -- no maker, no spread, no allowlist. That
// is what makes "both legs for less than 1.00" a guarantee enforced by the vault
// rather than a promise from a counterparty.
