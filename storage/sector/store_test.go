package sector

import (
	"context"
	"fmt"
	"io"
	"math/rand"
	"testing"

	"github.com/filecoin-project/lotus/build"
	"github.com/filecoin-project/lotus/lib/sectorbuilder"
	"github.com/ipfs/go-datastore"
)

func TestSectorStore(t *testing.T) {
	if err := build.GetParams(true); err != nil {
		t.Fatal(err)
	}

	sb, cleanup, err := sectorbuilder.TempSectorbuilder(1024)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()

	tktFn := func(context.Context) (*sectorbuilder.SealTicket, error) {
		return &sectorbuilder.SealTicket{
			BlockHeight: 17,
			TicketBytes: [32]byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 0, 1, 2},
		}, nil
	}

	ds := datastore.NewMapDatastore()

	store := NewStore(sb, ds, tktFn)

	pr := io.LimitReader(rand.New(rand.NewSource(17)), 300)
	sid, err := store.AddPiece("a", 300, pr, 1)
	if err != nil {
		t.Fatal(err)
	}
	fmt.Println(sid)
}
