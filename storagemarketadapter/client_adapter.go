package storagemarketadapter

import (
	"context"

	"go.uber.org/fx"

	"github.com/filecoin-project/lotus/chain/actors"
	"github.com/filecoin-project/lotus/chain/address"
	"github.com/filecoin-project/lotus/node/impl/full"
	"github.com/filecoin-project/lotus/storagemarket"
)

type ClientNodeAdapter struct {
	fx.In

	Common

	full.StateAPI
}

func NewClientNodeAdapter(chain full.ChainAPI, state full.StateAPI, mpool full.MpoolAPI) storagemarket.StorageClientNode {
	return &ClientNodeAdapter{
		StateAPI: state,
	}
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

var _ storagemarket.StorageClientNode = &ClientNodeAdapter{}
