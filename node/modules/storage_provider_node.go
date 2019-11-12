package modules

import (
	"bytes"
	"context"

	"github.com/ipfs/go-cid"
	"golang.org/x/xerrors"

	"github.com/filecoin-project/lotus/api"
	"github.com/filecoin-project/lotus/chain/actors"
	"github.com/filecoin-project/lotus/chain/address"
	"github.com/filecoin-project/lotus/chain/types"
	"github.com/filecoin-project/lotus/storagemarket"
)

type ProviderNodeAdapterAPI interface {
	ChainHead(context.Context) (*types.TipSet, error)
	MpoolPushMessage(context.Context, *types.Message) (*types.SignedMessage, error) // get nonce, sign, push

	StateWaitMsg(context.Context, cid.Cid) (*api.MsgWait, error)
	StateMarketBalance(context.Context, address.Address, *types.TipSet) (actors.StorageParticipantBalance, error)
	StateMinerWorker(context.Context, address.Address, *types.TipSet) (address.Address, error)

	WalletSign(context.Context, address.Address, []byte) (*types.Signature, error)
}

type ProviderNodeAdapter struct {
	api ProviderNodeAdapterAPI
}

func NewProviderNodeAdapter(full api.FullNode) storagemarket.StorageProviderNode {
	return &ProviderNodeAdapter{api: full}
}

func (n *ProviderNodeAdapter) MostRecentStateId(ctx context.Context) (storagemarket.StateKey, error) {
	ts, err := n.api.ChainHead(ctx)
	if err != nil {
		return nil, err
	}

	return ts, nil
}

// Adds funds with the StorageMinerActor for a storage participant.  Used by both providers and clients.
func (n *ProviderNodeAdapter) AddFunds(ctx context.Context, addr address.Address, amount storagemarket.BigInt) error {
	// (Provider Node API)
	smsg, err := n.api.MpoolPushMessage(ctx, &types.Message{
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

	r, err := n.api.StateWaitMsg(ctx, smsg.Cid())
	if err != nil {
		return err
	}

	if r.Receipt.ExitCode != 0 {
		return xerrors.Errorf("adding funds to storage miner market actor failed: exit %d", r.Receipt.ExitCode)
	}

	return nil
}

func (n *ProviderNodeAdapter) GetBalance(ctx context.Context, addr address.Address) (storagemarket.Balance, error) {
	bal, err := n.api.StateMarketBalance(ctx, addr, nil)
	if err != nil {
		return storagemarket.Balance{}, err
	}

	return bal, nil
}

func (n *ProviderNodeAdapter) PublishDeals(ctx context.Context, deal storagemarket.MinerDeal) (storagemarket.DealID, error) {
	log.Info("publishing deal")

	storageDeal := actors.StorageDeal{
		Proposal: deal.Proposal,
	}

	worker, err := n.api.StateMinerWorker(ctx, deal.Proposal.Provider, nil)
	if err != nil {
		return 0, err
	}

	if err := api.SignWith(ctx, n.api.WalletSign, worker, &storageDeal); err != nil {
		return 0, xerrors.Errorf("signing storage deal failed: ", err)
	}

	params, err := actors.SerializeParams(&actors.PublishStorageDealsParams{
		Deals: []actors.StorageDeal{storageDeal},
	})
	if err != nil {
		return 0, xerrors.Errorf("serializing PublishStorageDeals params failed: ", err)
	}

	// TODO: We may want this to happen after fetching data
	smsg, err := n.api.MpoolPushMessage(ctx, &types.Message{
		To:       actors.StorageMarketAddress,
		From:     worker,
		Value:    types.NewInt(0),
		GasPrice: types.NewInt(0),
		GasLimit: types.NewInt(1000000),
		Method:   actors.SMAMethods.PublishStorageDeals,
		Params:   params,
	})
	if err != nil {
		return 0, err
	}
	r, err := n.api.StateWaitMsg(ctx, smsg.Cid())
	if err != nil {
		return 0, err
	}
	if r.Receipt.ExitCode != 0 {
		return 0, xerrors.Errorf("publishing deal failed: exit %d", r.Receipt.ExitCode)
	}
	var resp actors.PublishStorageDealResponse
	if err := resp.UnmarshalCBOR(bytes.NewReader(r.Receipt.Return)); err != nil {
		return 0, err
	}
	if len(resp.DealIDs) != 1 {
		return 0, xerrors.Errorf("got unexpected number of DealIDs from")
	}

	return storagemarket.DealID(resp.DealIDs[0]), nil
}

var _ storagemarket.StorageProviderNode = &ProviderNodeAdapter{}
