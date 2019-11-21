package storagemarketadapter

import (
	"context"

	logging "github.com/ipfs/go-log"
	"go.uber.org/fx"
	"golang.org/x/xerrors"

	"github.com/filecoin-project/lotus/chain/actors"
	"github.com/filecoin-project/lotus/chain/address"
	"github.com/filecoin-project/lotus/chain/types"
	"github.com/filecoin-project/lotus/node/impl/full"
	"github.com/filecoin-project/lotus/storagemarket"
)

var log = logging.Logger("storagemarketadapter")

// Node adapter functionality common between Client and Provider adapters
type Common struct {
	fx.In

	full.ChainAPI
	full.MpoolAPI
	full.StateAPI
}

func (n *Common) MostRecentStateId(ctx context.Context) (storagemarket.StateKey, error) {
	return n.ChainHead(ctx)
}

// Adds funds with the StorageMinerActor for a storage participant.  Used by both providers and clients.
func (n *Common) AddFunds(ctx context.Context, addr address.Address, amount storagemarket.BigInt) error {
	// (Provider Node API)
	smsg, err := n.MpoolPushMessage(ctx, &types.Message{
		To:       actors.StorageMarketAddress,
		From:     addr,
		Value:    amount,
		GasPrice: types.NewInt(0),
		GasLimit: types.NewInt(1000000),
		Method:   actors.SMAMethods.AddBalance,
	})
	if err != nil {
		return err
	}

	r, err := n.StateWaitMsg(ctx, smsg.Cid())
	if err != nil {
		return err
	}

	if r.Receipt.ExitCode != 0 {
		return xerrors.Errorf("adding funds to storage miner market actor failed: exit %d", r.Receipt.ExitCode)
	}

	return nil
}

func (n *Common) GetBalance(ctx context.Context, addr address.Address) (storagemarket.Balance, error) {
	bal, err := n.StateMarketBalance(ctx, addr, nil)
	if err != nil {
		return storagemarket.Balance{}, err
	}

	return bal, nil
}
