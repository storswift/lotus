package test

import (
	"context"
	"testing"
	"time"

	logging "github.com/ipfs/go-log"
	"github.com/stretchr/testify/require"

	"github.com/filecoin-project/go-lotus/build"
	"github.com/filecoin-project/go-sectorbuilder/sealing_state"
)

func init() {
	logging.SetLogLevel("*", "INFO")
}

func (ts *testSuite) testMining(t *testing.T) {
	ctx := context.Background()
	apis, _ := ts.makeNodes(t, 1, []int{0})
	api := apis[0]

	h1, err := api.ChainHead(ctx)
	require.NoError(t, err)
	require.Equal(t, uint64(0), h1.Height())

	newHeads, err := api.ChainNotify(ctx)
	require.NoError(t, err)
	<-newHeads

	err = api.MineOne(ctx)
	require.NoError(t, err)

	<-newHeads

	h2, err := api.ChainHead(ctx)
	require.NoError(t, err)
	require.Equal(t, uint64(1), h2.Height())
}

func (ts *testSuite) testPost(t *testing.T) {
	require.NoError(t, build.GetParams(true))

	ctx := context.Background()
	apis, sminers := ts.makeNodes(t, 2, []int{0})
	api := apis[0]

	newHeads, err := api.ChainNotify(ctx)
	require.NoError(t, err)
	<-newHeads

	sect, err := sminers[0].StoreGarbageData(ctx)
	require.NoError(t, err)

	advance := func(n int) {
		for i := 0; i < n; i++ {
			err = api.MineOne(ctx)
			require.NoError(t, err)

			<-newHeads
		}
	}

	advance(1)

	loop:
	for {
		st, err := sminers[0].SectorsStatus(ctx, sect)
		require.NoError(t, err)

		switch st.State {
		case sealing_state.Sealing:
			continue
		case sealing_state.Sealed:
			 break loop
		default:
			t.Fatal("unexpected sealing state:", st.State, st.SealErrorMsg)
		}
	}

	time.Sleep(10 * time.Second)

	advance(2)

	minerAddr, err := sminers[0].ActorAddresses(ctx)
	require.NoError(t, err)

	ppe, err := api.StateMinerProvingPeriodEnd(ctx, minerAddr[0], nil)
	require.NoError(t, err)

	if ppe == 0 {
		t.Fatal("ppe was 0")
	}

	postRound := func() {
		advance(build.ProvingPeriodDuration - build.PoSTChallangeTime)

		time.Sleep(8 * time.Second) //ewwww

		advance(2)

		nppe, err := api.StateMinerProvingPeriodEnd(ctx, minerAddr[0], nil)
		require.NoError(t, err)

		if ppe == nppe {
			t.Fatal("ppe == nppe")
		}

		advance(build.PoSTChallangeTime)
	}

	postRound()
	postRound()

}

