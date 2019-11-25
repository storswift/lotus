package retrieval

import (
	"github.com/ipfs/go-cid"
	"github.com/ipld/go-ipld-prime/traversal/selector"
	"github.com/libp2p/go-libp2p-core/peer"

	"github.com/filecoin-project/lotus/api"
	"github.com/filecoin-project/lotus/chain/address"
	"github.com/filecoin-project/lotus/chain/types"
)

const ProtocolID = "/fil/retrieval/0.0.1"          // TODO: spec
const QueryProtocolID = "/fil/retrieval/qry/0.0.1" // TODO: spec

type BigInt = types.BigInt

type ClientDealState struct {
	DealProposal
	Status        Status
	Sender        peer.ID
	TotalReceived uint64
	FundsSpent    BigInt
}

type ClientEvent int

const (
	ClientOpen          ClientEvent = iota
	ClientFundsExpended             // when totalFunds is expended
	ClientProgress
	ClientError
	ClientComplete
)

type ClientSubscriber func(event ClientEvent, state ClientDealState)

type RetrievalClient interface {
	// V0
	FindProviders(pieceCID cid.CID) []RetrievalPeer
	Query(
		p RetrievalPeer,
		pieceCID cid.Cid,
		payloadCID cid.CID,
		selector selector.Selector,
		maxPricePerByte BigInt,
	) QueryResponse
	Retrieve(
		pieceCid cid.CID,
		payloadCID cid.CID,
		selector selector.Selector,
		pricePerByte BigInt,
		totalFunds BigInt,
		miner peer.ID,
		clientWallet address.Address,
		minerWallet address.Address,
	) DealID
	SubscribeToEvents(subscriber ClientSubscriber)

	// V1
	AddMoreFunds(id DealID, amount BigInt) error
	CancelDeal(id DealID) error
	RetrievalStatus(id DealID)
	ListDeals() map[DealID]ClientDealState
}

type ProviderDealState struct {
	DealProposal
	Status        Status
	Receiver      peer.ID
	TotalSent     uint64
	FundsReceived BigInt
}

type ProviderEvent int
 
const (
	ProviderOpen ProviderEvent = iota
	ProviderProgress
	ProviderError
	ProviderComplete
)

type ProviderDealID struct {
	From libp2p.PeerID
	ID   DealID
}

type ProviderSubscriber func(event ProviderEvent, state ProviderDealState)

type RetrievalProvider interface {
	// V0
	SetPricePerByte(price BigInt)
	SubscribeToEvents(subscriber ProviderSubscriber)

	// V1
	SetPricePerUnseal(price BigInt)
	ListDeals() map[ProviderDealID]ProviderDealState
}

type RetrievalPeer struct {
	Address address.Address
	ID      peer.ID // optional
}

type PeerResolver interface {
	GetPeers(data cid.Cid) ([]RetrievalPeer, error) // TODO: channel
}

type QueryResponseStatus uint64

const (
	Available QueryResponseStatus = iota
	Unavailable
)

type Query struct {
	Piece cid.Cid
	// TODO: payment
}

type QueryResponse struct {
	Status QueryResponseStatus

	Size uint64 // TODO: spec
	// TODO: unseal price (+spec)
	// TODO: sectors to unseal
	// TODO: address to send money for the deal?
	MinPrice types.BigInt
}

type Status int

const (
	Accepted Status = iota
	Failed
	Rejected
	Unsealing
	FundsNeeded
	Ongoing
	Completed
	DealNotFound
)

type DealID uint64

type Unixfs0Offer struct {
	Offset uint64
	Size   uint64
}

type RetParams struct {
	Unixfs0 *Unixfs0Offer
}

type DealProposal struct {
	Payment api.PaymentInfo

	Ref    cid.Cid
	Params RetParams
}

type DealResponse struct {
	Status  uint64
	Message string
}

type Block struct { // TODO: put in spec
	Prefix []byte // TODO: fix cid.Prefix marshaling somehow
	Data   []byte
}
