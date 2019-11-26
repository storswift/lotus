package validation

import (
	"context"

	"github.com/ipfs/go-cid"
	"golang.org/x/xerrors"

	vchain "github.com/filecoin-project/chain-validation/pkg/chain"
	vstate "github.com/filecoin-project/chain-validation/pkg/state"
	vactors "github.com/filecoin-project/chain-validation/pkg/state/actors"
	vaddress "github.com/filecoin-project/chain-validation/pkg/state/address"
	vtypes "github.com/filecoin-project/chain-validation/pkg/state/types"

	"github.com/filecoin-project/lotus/chain/actors"
	"github.com/filecoin-project/lotus/chain/address"
	"github.com/filecoin-project/lotus/chain/types"
)

type Signer interface {
	Sign(ctx context.Context, addr vaddress.Address, msg []byte) (*types.Signature, error)
}

type MessageFactory struct {
	signer Signer
}

var _ vchain.MessageFactory = &MessageFactory{}

func NewMessageFactory(signer Signer) *MessageFactory {
	return &MessageFactory{signer}
}

func (mf *MessageFactory) MakeMessage(from, to vaddress.Address, method vchain.MethodID, nonce uint64, value, gasPrice vtypes.BigInt, gasLimit vstate.GasUnit, params ...interface{}) (interface{}, error) {
	fromDec, err := address.NewFromBytes(from.Bytes())
	if err != nil {
		return nil, err
	}
	toDec, err := address.NewFromBytes(to.Bytes())
	if err != nil {
		return nil, err
	}
	valueDec := types.BigInt{value.Int}
	paramsDec, err := vstate.EncodeValues(params...)
	if err != nil {
		return nil, err
	}

	if int(method) >= len(methods) {
		return nil, xerrors.Errorf("No method name for method %v", method)
	}
	methodId := methods[method]
	msg := &types.Message{
		toDec,
		fromDec,
		nonce,
		valueDec,
		types.BigInt{gasPrice.Int},
		types.NewInt(uint64(gasLimit)),
		methodId,
		paramsDec,
	}

	return msg, nil
}

func (mf *MessageFactory) FromSingletonAddress(addr vactors.SingletonActorID) vaddress.Address {
	return fromSingletonAddress(addr)
}

func (mf *MessageFactory) FromActorCodeCid(code vactors.ActorCodeID) cid.Cid {
	return fromActorCode(code)
}

// Maps method enumeration values to method names.
// This will change to a mapping to method ids when method dispatch is updated to use integers.
var methods = []uint64{
	vchain.NoMethod: 0,
	vchain.InitExec: actors.IAMethods.Exec,

	vchain.StoragePowerConstructor:        actors.SPAMethods.Constructor,
	vchain.StoragePowerCreateStorageMiner: actors.SPAMethods.CreateStorageMiner,
	vchain.StoragePowerUpdatePower:        actors.SPAMethods.UpdateStorage,

	vchain.StorageMinerUpdatePeerID:  actors.MAMethods.UpdatePeerID,
	vchain.StorageMinerGetOwner:      actors.MAMethods.GetOwner,
	vchain.StorageMinerGetPower:      actors.MAMethods.GetPower,
	vchain.StorageMinerGetWorkerAddr: actors.MAMethods.GetWorkerAddr,
	vchain.StorageMinerGetPeerID:     actors.MAMethods.GetPeerID,
	vchain.StorageMinerGetSectorSize: actors.MAMethods.GetSectorSize,
	// More to follow...
}
