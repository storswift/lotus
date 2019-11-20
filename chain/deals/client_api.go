package deals

import (
	"context"

	"github.com/ipfs/go-cid"

	"github.com/filecoin-project/lotus/chain/actors"
	"github.com/filecoin-project/lotus/chain/types"
	"github.com/filecoin-project/lotus/storagemarket"
)

func (c *Client) ListProviders(ctx context.Context) (<-chan storagemarket.StorageProviderInfo, error) {
	providers, err := c.scn.ListStorageProviders(ctx)
	if err != nil {
		return nil, err
	}

	out := make(chan storagemarket.StorageProviderInfo)

	go func() {
		for _, p := range providers {
			select {
			case out <- *p:
			case <-ctx.Done():
				return
			}

		}
	}()

	return out, nil
}

func (c *Client) ListDeals(ctx context.Context) ([]actors.OnChainDeal, error) {
	addr, err := c.w.GetDefault()
	if err != nil {
		return nil, err
	}

	return c.scn.ListClientDeals(ctx, addr)
}

func (c *Client) GetAsk(ctx context.Context, info storagemarket.StorageProviderInfo) (*storagemarket.StorageAsk, error) {
	return c.QueryAsk(ctx, info.PeerID, info.Address)
}

func (c *Client) ProposeStorageDeal(ctx context.Context, info *storagemarket.StorageProviderInfo, payloadCid cid.Cid, size uint64, proposalExpiration storagemarket.Epoch, duration storagemarket.Epoch, price storagemarket.TokenAmount, collateral storagemarket.TokenAmount) (*storagemarket.ProposeStorageDealResult, error) {
	addr, err := c.w.GetDefault()
	if err != nil {
		return nil, err
	}

	proposal := ClientDealProposal{
		Data:               payloadCid,
		PricePerEpoch:      types.NewInt(uint64(price)),
		ProposalExpiration: uint64(proposalExpiration),
		Duration:           uint64(duration),
		Client:             addr,
		ProviderAddress:    info.Address,
		MinerWorker:        info.Worker,
		MinerID:            info.PeerID,
	}

	_, err = c.Start(ctx, proposal)

	// TODO: fill this out
	result := &storagemarket.ProposeStorageDealResult{}

	return result, err
}

func (c *Client) GetPaymentEscrow(ctx context.Context) (storagemarket.Balance, error) {
	addr, err := c.w.GetDefault()
	if err != nil {
		return storagemarket.Balance{}, err
	}

	balance, err := c.scn.GetBalance(ctx, addr)

	return balance, err
}

func (c *Client) AddPaymentEscrow(ctx context.Context, amount types.BigInt) error {
	addr, err := c.w.GetDefault()
	if err != nil {
		return err
	}

	return c.scn.AddFunds(ctx, addr, amount)
}

var _ storagemarket.StorageClient = &Client{}
