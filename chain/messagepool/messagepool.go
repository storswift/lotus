package messagepool

import (
	"bytes"
	"context"
	"errors"
	"sort"
	"sync"
	"time"

	lru "github.com/hashicorp/golang-lru"
	"github.com/ipfs/go-cid"
	"github.com/ipfs/go-datastore"
	"github.com/ipfs/go-datastore/namespace"
	"github.com/ipfs/go-datastore/query"
	logging "github.com/ipfs/go-log"
	pubsub "github.com/libp2p/go-libp2p-pubsub"
	lps "github.com/whyrusleeping/pubsub"
	"go.uber.org/multierr"
	"golang.org/x/xerrors"

	"github.com/filecoin-project/lotus/api"
	"github.com/filecoin-project/lotus/build"
	"github.com/filecoin-project/lotus/chain"
	"github.com/filecoin-project/lotus/chain/address"
	"github.com/filecoin-project/lotus/chain/stmgr"
	"github.com/filecoin-project/lotus/chain/store"
	"github.com/filecoin-project/lotus/chain/types"
	"github.com/filecoin-project/lotus/node/modules/dtypes"
)

var log = logging.Logger("messagepool")

var (
	ErrMessageTooBig = errors.New("message too big")

	ErrMessageValueTooHigh = errors.New("cannot send more filecoin than will ever exist")

	ErrNonceTooLow = errors.New("message nonce too low")

	ErrNotEnoughFunds = errors.New("not enough funds to execute transaction")

	ErrInvalidToAddr = errors.New("message had invalid to address")
)

const (
	msgTopic = "/fil/messages"

	localMsgsDs = "/mpool/local"

	localUpdates = "update"
)

type MessagePool struct {
	lk sync.Mutex

	closer  chan struct{}
	repubTk *time.Ticker

	localAddrs map[address.Address]struct{}

	pending      map[address.Address]*msgSet
	pendingCount int

	curTsLk sync.RWMutex
	curTs   *types.TipSet

	api MpoolProvider

	minGasPrice types.BigInt

	maxTxPoolSize int

	blsSigCache *lru.TwoQueueCache

	changes *lps.PubSub

	localMsgs datastore.Datastore
}

type msgSet struct {
	msgs       map[uint64]*types.SignedMessage
	nextNonce  uint64
	curBalance types.BigInt
}

func newMsgSet() *msgSet {
	return &msgSet{
		msgs: make(map[uint64]*types.SignedMessage),
	}
}

func (ms *msgSet) add(m *types.SignedMessage) error {
	if len(ms.msgs) == 0 || m.Message.Nonce >= ms.nextNonce {
		ms.nextNonce = m.Message.Nonce + 1
	}
	if _, has := ms.msgs[m.Message.Nonce]; has {
		if m.Cid() != ms.msgs[m.Message.Nonce].Cid() {
			log.Error("Add with duplicate nonce")
			return xerrors.Errorf("message to %s with nonce %d already in mpool")
		}
	}
	ms.msgs[m.Message.Nonce] = m

	return nil
}

type MpoolProvider interface {
	SubscribeHeadChanges(func(rev, app []*types.TipSet) error)
	PutMessage(m store.ChainMsg) (cid.Cid, error)
	PubSubPublish(string, []byte) error
	StateGetActor(address.Address, *types.TipSet) (*types.Actor, error)
	MessagesForBlock(*types.BlockHeader) ([]*types.Message, []*types.SignedMessage, error)
	MessagesForTipset(*types.TipSet) ([]store.ChainMsg, error)
}

type mpoolProvider struct {
	sm *stmgr.StateManager
	ps *pubsub.PubSub
}

func NewMpoolProvider(sm *stmgr.StateManager, ps *pubsub.PubSub) MpoolProvider {
	return &mpoolProvider{sm, ps}
}

func (mpp *mpoolProvider) SubscribeHeadChanges(cb func(rev, app []*types.TipSet) error) {
	mpp.sm.ChainStore().SubscribeHeadChanges(cb)
}

func (mpp *mpoolProvider) PutMessage(m store.ChainMsg) (cid.Cid, error) {
	return mpp.sm.ChainStore().PutMessage(m)
}

func (mpp *mpoolProvider) PubSubPublish(k string, v []byte) error {
	return mpp.ps.Publish(k, v)
}

func (mpp *mpoolProvider) StateGetActor(addr address.Address, ts *types.TipSet) (*types.Actor, error) {
	return mpp.sm.GetActor(addr, ts)
}

func (mpp *mpoolProvider) MessagesForBlock(h *types.BlockHeader) ([]*types.Message, []*types.SignedMessage, error) {
	return mpp.sm.ChainStore().MessagesForBlock(h)
}

func (mpp *mpoolProvider) MessagesForTipset(ts *types.TipSet) ([]store.ChainMsg, error) {
	return mpp.sm.ChainStore().MessagesForTipset(ts)
}

func NewMessagePool(api MpoolProvider, ds dtypes.MetadataDS) (*MessagePool, error) {
	cache, _ := lru.New2Q(build.BlsSignatureCacheSize)
	mp := &MessagePool{
		closer:        make(chan struct{}),
		repubTk:       time.NewTicker(build.BlockDelay * 10 * time.Second),
		localAddrs:    make(map[address.Address]struct{}),
		pending:       make(map[address.Address]*msgSet),
		minGasPrice:   types.NewInt(0),
		maxTxPoolSize: 5000,
		blsSigCache:   cache,
		changes:       lps.New(50),
		localMsgs:     namespace.Wrap(ds, datastore.NewKey(localMsgsDs)),
		api:           api,
	}

	if err := mp.loadLocal(); err != nil {
		return nil, xerrors.Errorf("loading local messages: %w", err)
	}

	go mp.repubLocal()

	api.SubscribeHeadChanges(func(rev, app []*types.TipSet) error {
		err := mp.HeadChange(rev, app)
		if err != nil {
			log.Errorf("mpool head notif handler error: %+v", err)
		}
		return err
	})

	return mp, nil
}

func (mp *MessagePool) Close() error {
	close(mp.closer)
	return nil
}

func (mp *MessagePool) repubLocal() {
	for {
		select {
		case <-mp.repubTk.C:
			mp.lk.Lock()
			msgs := make([]*types.SignedMessage, 0)
			for a := range mp.localAddrs {
				msgs = append(msgs, mp.pendingFor(a)...)
			}
			mp.lk.Unlock()

			var errout error
			for _, msg := range msgs {
				msgb, err := msg.Serialize()
				if err != nil {
					errout = multierr.Append(errout, xerrors.Errorf("could not serialize: %w", err))
					continue
				}

				err = mp.api.PubSubPublish(msgTopic, msgb)
				if err != nil {
					errout = multierr.Append(errout, xerrors.Errorf("could not publish: %w", err))
					continue
				}
			}

			if errout != nil {
				log.Errorf("errors while republishing: %+v", errout)
			}
		case <-mp.closer:
			mp.repubTk.Stop()
			return
		}
	}

}

func (mp *MessagePool) addLocal(m *types.SignedMessage, msgb []byte) error {
	mp.localAddrs[m.Message.From] = struct{}{}

	if err := mp.localMsgs.Put(datastore.NewKey(string(m.Cid().Bytes())), msgb); err != nil {
		return xerrors.Errorf("persisting local message: %w", err)
	}

	return nil
}

func (mp *MessagePool) Push(m *types.SignedMessage) error {
	msgb, err := m.Serialize()
	if err != nil {
		return err
	}

	if err := mp.Add(m); err != nil {
		return err
	}

	mp.lk.Lock()
	if err := mp.addLocal(m, msgb); err != nil {
		mp.lk.Unlock()
		return err
	}
	mp.lk.Unlock()

	return mp.api.PubSubPublish(msgTopic, msgb)
}

func (mp *MessagePool) Add(m *types.SignedMessage) error {
	// big messages are bad, anti DOS
	if m.Size() > 32*1024 {
		return xerrors.Errorf("mpool message too large (%dB): %w", m.Size(), ErrMessageTooBig)
	}

	if m.Message.To == address.Undef {
		return ErrInvalidToAddr
	}

	if !m.Message.Value.LessThan(types.TotalFilecoinInt) {
		return ErrMessageValueTooHigh
	}

	if err := m.Signature.Verify(m.Message.From, m.Message.Cid().Bytes()); err != nil {
		log.Warnf("mpooladd signature verification failed: %s", err)
		return err
	}

	snonce, err := mp.getStateNonce(m.Message.From)
	if err != nil {
		return xerrors.Errorf("failed to look up actor state nonce: %w", err)
	}

	if snonce > m.Message.Nonce {
		return xerrors.Errorf("minimum expected nonce is %d: %w", snonce, ErrNonceTooLow)
	}

	balance, err := mp.getStateBalance(m.Message.From)
	if err != nil {
		return xerrors.Errorf("failed to check sender balance: %w", err)
	}

	if balance.LessThan(m.Message.RequiredFunds()) {
		return xerrors.Errorf("not enough funds (required: %s, balance: %s): %w", types.FIL(m.Message.RequiredFunds()), types.FIL(balance), ErrNotEnoughFunds)
	}

	mp.lk.Lock()
	defer mp.lk.Unlock()

	return mp.addLocked(m)
}

func (mp *MessagePool) addLocked(m *types.SignedMessage) error {
	log.Debugf("mpooladd: %s %s", m.Message.From, m.Message.Nonce)
	if m.Signature.Type == types.KTBLS {
		mp.blsSigCache.Add(m.Cid(), m.Signature)
	}

	if _, err := mp.api.PutMessage(m); err != nil {
		log.Warnf("mpooladd cs.PutMessage failed: %s", err)
		return err
	}

	if _, err := mp.api.PutMessage(&m.Message); err != nil {
		log.Warnf("mpooladd cs.PutMessage failed: %s", err)
		return err
	}

	mset, ok := mp.pending[m.Message.From]
	if !ok {
		mset = newMsgSet()
		mp.pending[m.Message.From] = mset
	}

	if err := mset.add(m); err != nil {
		log.Error(err)
	}

	mp.changes.Pub(api.MpoolUpdate{
		Type:    api.MpoolAdd,
		Message: m,
	}, localUpdates)
	return nil
}

func (mp *MessagePool) GetNonce(addr address.Address) (uint64, error) {
	mp.lk.Lock()
	defer mp.lk.Unlock()

	return mp.getNonceLocked(addr)
}

func (mp *MessagePool) getNonceLocked(addr address.Address) (uint64, error) {
	stateNonce, err := mp.getStateNonce(addr) // sanity check
	if err != nil {
		return 0, err
	}

	mset, ok := mp.pending[addr]
	if ok {
		if stateNonce > mset.nextNonce {
			log.Errorf("state nonce was larger than mset.nextNonce (%d > %d)", stateNonce, mset.nextNonce)

			return stateNonce, nil
		}

		return mset.nextNonce, nil
	}

	return stateNonce, nil
}

func (mp *MessagePool) getStateNonce(addr address.Address) (uint64, error) {
	// TODO: this method probably should be cached
	mp.curTsLk.RLock()
	defer mp.curTsLk.RUnlock()

	act, err := mp.api.StateGetActor(addr, mp.curTs)
	if err != nil {
		return 0, err
	}

	baseNonce := act.Nonce

	// TODO: the correct thing to do here is probably to set curTs to chain.head
	// but since we have an accurate view of the world until a head change occurs,
	// this should be fine
	if mp.curTs == nil {
		return baseNonce, nil
	}

	msgs, err := mp.api.MessagesForTipset(mp.curTs)
	if err != nil {
		return 0, xerrors.Errorf("failed to check messages for tipset: %w", err)
	}

	for _, m := range msgs {
		msg := m.VMMessage()
		if msg.From == addr {
			if msg.Nonce != baseNonce {
				return 0, xerrors.Errorf("tipset %s has bad nonce ordering", mp.curTs)
			}
			baseNonce++
		}
	}

	return baseNonce, nil
}

func (mp *MessagePool) getStateBalance(addr address.Address) (types.BigInt, error) {
	act, err := mp.api.StateGetActor(addr, nil)
	if err != nil {
		return types.EmptyInt, err
	}

	return act.Balance, nil
}

func (mp *MessagePool) PushWithNonce(addr address.Address, cb func(uint64) (*types.SignedMessage, error)) (*types.SignedMessage, error) {
	mp.lk.Lock()
	defer mp.lk.Unlock()
	if addr.Protocol() == address.ID {
		log.Warnf("Called pushWithNonce with ID address (%s) this might not be handled properly yet", addr)
	}

	nonce, err := mp.getNonceLocked(addr)
	if err != nil {
		return nil, err
	}

	msg, err := cb(nonce)
	if err != nil {
		return nil, err
	}

	msgb, err := msg.Serialize()
	if err != nil {
		return nil, err
	}

	if err := mp.addLocked(msg); err != nil {
		return nil, err
	}
	if err := mp.addLocal(msg, msgb); err != nil {
		log.Errorf("addLocal failed: %+v", err)
	}

	return msg, mp.api.PubSubPublish(msgTopic, msgb)
}

func (mp *MessagePool) Remove(from address.Address, nonce uint64) {
	mp.lk.Lock()
	defer mp.lk.Unlock()

	mset, ok := mp.pending[from]
	if !ok {
		return
	}

	if m, ok := mset.msgs[nonce]; ok {
		mp.changes.Pub(api.MpoolUpdate{
			Type:    api.MpoolRemove,
			Message: m,
		}, localUpdates)
	}

	// NB: This deletes any message with the given nonce. This makes sense
	// as two messages with the same sender cannot have the same nonce
	delete(mset.msgs, nonce)

	if len(mset.msgs) == 0 {
		delete(mp.pending, from)
	} else {
		var max uint64
		for nonce := range mset.msgs {
			if max < nonce {
				max = nonce
			}
		}
		if max < nonce {
			max = nonce // we could have not seen the removed message before
		}

		mset.nextNonce = max + 1
	}
}

func (mp *MessagePool) Pending() []*types.SignedMessage {
	mp.lk.Lock()
	defer mp.lk.Unlock()
	out := make([]*types.SignedMessage, 0)
	for a := range mp.pending {
		out = append(out, mp.pendingFor(a)...)
	}

	return out
}

func (mp *MessagePool) pendingFor(a address.Address) []*types.SignedMessage {
	mset := mp.pending[a]
	if mset == nil || len(mset.msgs) == 0 {
		return nil
	}

	set := make([]*types.SignedMessage, 0, len(mset.msgs))

	for _, m := range mset.msgs {
		set = append(set, m)
	}

	sort.Slice(set, func(i, j int) bool {
		return set[i].Message.Nonce < set[j].Message.Nonce
	})

	return set
}

func (mp *MessagePool) HeadChange(revert []*types.TipSet, apply []*types.TipSet) error {
	mp.curTsLk.Lock()
	defer mp.curTsLk.Unlock()

	for _, ts := range revert {
		for _, b := range ts.Blocks() {
			bmsgs, smsgs, err := mp.api.MessagesForBlock(b)
			if err != nil {
				return xerrors.Errorf("failed to get messages for revert block %s(height %d): %w", b.Cid(), b.Height, err)
			}
			for _, msg := range smsgs {
				if err := mp.Add(msg); err != nil {
					log.Error(err) // TODO: probably lots of spam in multi-block tsets
				}
			}

			for _, msg := range bmsgs {
				smsg := mp.RecoverSig(msg)
				if smsg != nil {
					if err := mp.Add(smsg); err != nil {
						log.Error(err) // TODO: probably lots of spam in multi-block tsets
					}
				} else {
					log.Warnf("could not recover signature for bls message %s during a reorg revert", msg.Cid())
				}
			}
		}
		mp.curTs = ts
	}

	for _, ts := range apply {
		for _, b := range ts.Blocks() {
			bmsgs, smsgs, err := mp.api.MessagesForBlock(b)
			if err != nil {
				return xerrors.Errorf("failed to get messages for apply block %s(height %d) (msgroot = %s): %w", b.Cid(), b.Height, b.Messages, err)
			}
			for _, msg := range smsgs {
				mp.Remove(msg.Message.From, msg.Message.Nonce)
			}

			for _, msg := range bmsgs {
				mp.Remove(msg.From, msg.Nonce)
			}
		}
		mp.curTs = ts
	}

	return nil
}

func (mp *MessagePool) RecoverSig(msg *types.Message) *types.SignedMessage {
	val, ok := mp.blsSigCache.Get(msg.Cid())
	if !ok {
		return nil
	}
	sig, ok := val.(types.Signature)
	if !ok {
		log.Errorf("value in signature cache was not a signature (got %T)", val)
		return nil
	}

	return &types.SignedMessage{
		Message:   *msg,
		Signature: sig,
	}
}

func (mp *MessagePool) Updates(ctx context.Context) (<-chan api.MpoolUpdate, error) {
	out := make(chan api.MpoolUpdate, 20)
	sub := mp.changes.Sub(localUpdates)

	go func() {
		defer mp.changes.Unsub(sub, chain.LocalIncoming)

		for {
			select {
			case u := <-sub:
				select {
				case out <- u.(api.MpoolUpdate):
				case <-ctx.Done():
					return
				}
			case <-ctx.Done():
				return
			}
		}
	}()

	return out, nil
}

func (mp *MessagePool) loadLocal() error {
	res, err := mp.localMsgs.Query(query.Query{})
	if err != nil {
		return xerrors.Errorf("query local messages: %w", err)
	}

	for r := range res.Next() {
		if r.Error != nil {
			return xerrors.Errorf("r.Error: %w", r.Error)
		}

		var sm types.SignedMessage
		if err := sm.UnmarshalCBOR(bytes.NewReader(r.Value)); err != nil {
			return xerrors.Errorf("unmarshaling local message: %w", err)
		}

		if err := mp.Add(&sm); err != nil {
			if xerrors.Is(err, ErrNonceTooLow) {
				continue // todo: drop the message from local cache (if above certain confidence threshold)
			}

			return xerrors.Errorf("adding local message: %w", err)
		}
	}

	return nil
}
