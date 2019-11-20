package modules

import (
	"context"

	"golang.org/x/xerrors"

	"github.com/filecoin-project/lotus/chain/actors"
	"github.com/filecoin-project/lotus/chain/address"
	"github.com/filecoin-project/lotus/chain/types"
	"github.com/filecoin-project/lotus/node/impl/full"
	"github.com/filecoin-project/lotus/storagemarket"
)

type ClientNodeAdapter struct {
	full.ChainAPI
	full.StateAPI
	full.MpoolAPI
}

func NewClientNodeAdapter(chain full.ChainAPI, state full.StateAPI, mpool full.MpoolAPI) storagemarket.StorageClientNode {
	return &ClientNodeAdapter{
		ChainAPI: chain,
		StateAPI: state,
		MpoolAPI: mpool,
	}
}

func (n *ClientNodeAdapter) MostRecentStateId(ctx context.Context) (storagemarket.StateKey, error) {
	return n.ChainHead(ctx)
}

func (n *ClientNodeAdapter) ListStorageProviders(ctx context.Context) ([]*storagemarket.StorageProviderInfo, error) {
	ts, err := n.ChainHead(ctx)
	if err != nil {
		return nil, err
	}

	addresses, err := n.StateListMiners(ctx, ts)
	if err != nil {
		return nil, err
	}

	var out []*storagemarket.StorageProviderInfo

	for _, addr := range addresses {
		workerAddr, err := n.StateMinerWorker(ctx, addr, ts)
		if err != nil {
			return nil, err
		}

		sectorSize, err := n.StateMinerSectorSize(ctx, addr, ts)
		if err != nil {
			return nil, err
		}

		peerId, err := n.StateMinerPeerID(ctx, addr, ts)
		if err != nil {
			return nil, err
		}

		out = append(out, &storagemarket.StorageProviderInfo{
			Address:    addr,
			Worker:     workerAddr,
			SectorSize: sectorSize,
			PeerID:     peerId,
		})
	}

	return out, nil
}

func (n *ClientNodeAdapter) ListClientDeals(ctx context.Context, addr address.Address) ([]storagemarket.StorageDeal, error) {
	allDeals, err := n.StateMarketDeals(ctx, nil)
	if err != nil {
		return nil, err
	}

	var out []actors.OnChainDeal

	for _, deal := range allDeals {
		if deal.Deal.Proposal.Client == addr {
			out = append(out, deal)
		}
	}

	return out, nil
}

func (n *ClientNodeAdapter) GetBalance(ctx context.Context, addr address.Address) (storagemarket.Balance, error) {
	bal, err := n.StateMarketBalance(ctx, addr, nil)
	if err != nil {
		return storagemarket.Balance{}, err
	}

	return bal, nil
}

// Adds funds with the StorageMinerActor for a storage participant.  Used by both providers and clients.
func (n *ClientNodeAdapter) AddFunds(ctx context.Context, addr address.Address, amount storagemarket.BigInt) error {
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
		return xerrors.Errorf("adding funds to storage market participant failed: exit %d", r.Receipt.ExitCode)
	}

	return nil
}

var _ storagemarket.StorageClientNode = &ClientNodeAdapter{}
