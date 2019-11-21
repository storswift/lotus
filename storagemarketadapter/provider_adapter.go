package storagemarketadapter

import (
	"bytes"
	"context"

	"github.com/ipfs/go-cid"
	unixfile "github.com/ipfs/go-unixfs/file"
	"go.uber.org/fx"
	"golang.org/x/xerrors"

	"github.com/filecoin-project/lotus/api"
	"github.com/filecoin-project/lotus/chain/actors"
	"github.com/filecoin-project/lotus/chain/address"
	"github.com/filecoin-project/lotus/chain/types"
	"github.com/filecoin-project/lotus/lib/padreader"
	"github.com/filecoin-project/lotus/node/impl/full"
	"github.com/filecoin-project/lotus/node/impl/market"
	"github.com/filecoin-project/lotus/node/modules/dtypes"
	"github.com/filecoin-project/lotus/storage/sectorblocks"
	"github.com/filecoin-project/lotus/storagemarket"
)

type ProviderNodeAdapter struct {
	fx.In

	full.ChainAPI
	full.StateAPI
	market.MarketAPI

	Common

	// this goes away with the data transfer module
	dag dtypes.StagingDAG

	secb *sectorblocks.SectorBlocks
}

func NewProviderNodeAdapter(dag dtypes.StagingDAG, secb *sectorblocks.SectorBlocks) storagemarket.StorageProviderNode {
	return &ProviderNodeAdapter{
		dag:  dag,
		secb: secb,
	}
}

func (n *ProviderNodeAdapter) PublishDeals(ctx context.Context, deal storagemarket.MinerDeal) (storagemarket.DealID, cid.Cid, error) {
	log.Info("publishing deal")

	storageDeal := actors.StorageDeal{
		Proposal: deal.Proposal,
	}

	worker, err := n.StateMinerWorker(ctx, deal.Proposal.Provider, nil)
	if err != nil {
		return 0, cid.Undef, err
	}

	if err := api.SignWith(ctx, n.WalletSign, worker, &storageDeal); err != nil {
		return 0, cid.Undef, xerrors.Errorf("signing storage deal failed: ", err)
	}

	params, err := actors.SerializeParams(&actors.PublishStorageDealsParams{
		Deals: []actors.StorageDeal{storageDeal},
	})
	if err != nil {
		return 0, cid.Undef, xerrors.Errorf("serializing PublishStorageDeals params failed: ", err)
	}

	// TODO: We may want this to happen after fetching data
	smsg, err := n.MpoolPushMessage(ctx, &types.Message{
		To:       actors.StorageMarketAddress,
		From:     worker,
		Value:    types.NewInt(0),
		GasPrice: types.NewInt(0),
		GasLimit: types.NewInt(1000000),
		Method:   actors.SMAMethods.PublishStorageDeals,
		Params:   params,
	})
	if err != nil {
		return 0, cid.Undef, err
	}
	r, err := n.StateWaitMsg(ctx, smsg.Cid())
	if err != nil {
		return 0, cid.Undef, err
	}
	if r.Receipt.ExitCode != 0 {
		return 0, cid.Undef, xerrors.Errorf("publishing deal failed: exit %d", r.Receipt.ExitCode)
	}
	var resp actors.PublishStorageDealResponse
	if err := resp.UnmarshalCBOR(bytes.NewReader(r.Receipt.Return)); err != nil {
		return 0, cid.Undef, err
	}
	if len(resp.DealIDs) != 1 {
		return 0, cid.Undef, xerrors.Errorf("got unexpected number of DealIDs from")
	}

	return storagemarket.DealID(resp.DealIDs[0]), smsg.Cid(), nil
}

func (n *ProviderNodeAdapter) OnDealComplete(ctx context.Context, deal storagemarket.MinerDeal, piecePath string) (uint64, error) {
	root, err := n.dag.Get(ctx, deal.Ref)
	if err != nil {
		return 0, xerrors.Errorf("failed to get file root for deal: %s", err)
	}

	// TODO: abstract this away into ReadSizeCloser + implement different modes
	node, err := unixfile.NewUnixfsFile(ctx, n.dag, root)
	if err != nil {
		return 0, xerrors.Errorf("cannot open unixfs file: %s", err)
	}

	uf, ok := node.(sectorblocks.UnixfsReader)
	if !ok {
		// we probably got directory, unsupported for now
		return 0, xerrors.Errorf("unsupported unixfs file type")
	}

	// TODO: uf.Size() is user input, not trusted
	// This won't be useful / here after we migrate to putting CARs into sectors
	size, err := uf.Size()
	if err != nil {
		return 0, xerrors.Errorf("getting unixfs file size: %w", err)
	}
	if padreader.PaddedSize(uint64(size)) != deal.Proposal.PieceSize {
		return 0, xerrors.Errorf("deal.Proposal.PieceSize didn't match padded unixfs file size")
	}

	sectorID, err := n.secb.AddUnixfsPiece(ctx, deal.Ref, uf, deal.DealID)
	if err != nil {
		return 0, xerrors.Errorf("AddPiece failed: %s", err)
	}
	log.Warnf("New Sector: %d", sectorID)

	return sectorID, nil
}

func (n *ProviderNodeAdapter) ListProviderDeals(ctx context.Context, addr address.Address) ([]actors.OnChainDeal, error) {
	allDeals, err := n.StateMarketDeals(ctx, nil)
	if err != nil {
		return nil, err
	}

	var out []actors.OnChainDeal

	for _, deal := range allDeals {
		if deal.Deal.Proposal.Provider == addr {
			out = append(out, deal)
		}
	}

	return out, nil
}

func (n *ProviderNodeAdapter) GetMinerWorker(ctx context.Context, miner address.Address) (address.Address, error) {
	return n.StateMinerWorker(ctx, miner, nil)
}

func (n *ProviderNodeAdapter) SignBytes(ctx context.Context, signer address.Address, b []byte) (*types.Signature, error) {
	return n.WalletSign(ctx, signer, b)
}

func (n *ProviderNodeAdapter) EnsureFunds(ctx context.Context, addr address.Address, amt types.BigInt) error {
	return n.MarketEnsureAvailable(ctx, addr, amt)
}

var _ storagemarket.StorageProviderNode = &ProviderNodeAdapter{}
