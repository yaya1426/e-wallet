package transaction

import (
	"context"

	"github.com/google/uuid"
)

// WalletOfUser gives the wallet ID of a user, so the history endpoints need not import the
// wallet module (architecture §3.2). main adapts wallet.Service to it.
type WalletOfUser interface {
	WalletID(ctx context.Context, userID uuid.UUID) (uuid.UUID, error)
}

// Party is what the history shows about the other side of a P2P transfer (PRD-004 P2P-2).
type Party struct {
	Phone      string
	MaskedName string
}

// Parties resolves wallets to the parties that own them, one batch per page. main adapts the
// wallet and identity modules to it.
type Parties interface {
	ByWallet(ctx context.Context, walletIDs []uuid.UUID) (map[uuid.UUID]Party, error)
}
