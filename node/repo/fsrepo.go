package repo

import (
	"io"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"

	"github.com/ipfs/go-datastore"
	badger "github.com/ipfs/go-ds-badger"
	fslock "github.com/ipfs/go-fs-lock"
	"github.com/libp2p/go-libp2p-core/crypto"
	"github.com/multiformats/go-multiaddr"
	"golang.org/x/xerrors"

	"github.com/filecoin-project/go-lotus/node/config"
)

const (
	fsAPI       = "api"
	fsConfig    = "config.toml"
	fsDatastore = "datastore"
	fsLibp2pKey = "libp2p.priv"
	fsLock      = "repo.lock"
)

// FsRepo is struct for repo, use NewFS to create
type FsRepo struct {
	path string
}

var _ Repo = &FsRepo{}

// NewFS creates a repo instance based on a path on file system
func NewFS(path string) (*FsRepo, error) {
	return &FsRepo{
		path: path,
	}, nil
}

// APIEndpoint returns endpoint of API in this repo
func (fsr *FsRepo) APIEndpoint() (multiaddr.Multiaddr, error) {
	p := filepath.Join(fsr.path, fsAPI)
	f, err := os.Open(p)

	if os.IsNotExist(err) {
		return nil, ErrNoAPIEndpoint
	} else if err != nil {
		return nil, err
	}
	defer f.Close() //nolint: errcheck // Read only op

	data, err := ioutil.ReadAll(f)
	if err != nil {
		return nil, err
	}
	strma := string(data)
	strma = strings.TrimSpace(strma)

	apima, err := multiaddr.NewMultiaddr(strma)
	if err != nil {
		return nil, err
	}
	return apima, nil
}

// Lock acquires exclusive lock on this repo
func (fsr *FsRepo) Lock() (LockedRepo, error) {
	locked, err := fslock.Locked(fsr.path, fsLock)
	if err != nil {
		return nil, xerrors.Errorf("could not check lock status: %w", err)
	}
	if locked {
		return nil, ErrRepoAlreadyLocked
	}

	closer, err := fslock.Lock(fsr.path, fsLock)
	if err != nil {
		return nil, xerrors.Errorf("could not lock the repo: %w", err)
	}
	return &fsLockedRepo{
		path:   fsr.path,
		closer: closer,
	}, nil
}

type fsLockedRepo struct {
	path   string
	closer io.Closer
}

func (fsr *fsLockedRepo) Close() error {
	err := os.Remove(fsr.join(fsAPI))

	if err != nil && !os.IsNotExist(err) {
		return xerrors.Errorf("could not remove API file: %w", err)
	}

	err = fsr.closer.Close()
	fsr.closer = nil
	return err
}

// join joins path elements with fsr.path
func (fsr *fsLockedRepo) join(paths ...string) string {
	return filepath.Join(append([]string{fsr.path}, paths...)...)
}

func (fsr *fsLockedRepo) stillValid() error {
	if fsr.closer == nil {
		return ErrClosedRepo
	}
	return nil
}

func (fsr *fsLockedRepo) Datastore() (datastore.Datastore, error) {
	return badger.NewDatastore(fsr.join(fsDatastore), nil)
}

func (fsr *fsLockedRepo) Config() (*config.Root, error) {
	if err := fsr.stillValid(); err != nil {
		return nil, err
	}
	return config.FromFile(fsr.join(fsConfig))
}

func (fsr *fsLockedRepo) Libp2pIdentity() (crypto.PrivKey, error) {
	kpath := fsr.join(fsLibp2pKey)
	stat, err := os.Stat(kpath)

	if os.IsNotExist(err) {
		pk, err := genLibp2pKey()
		if err != nil {
			return nil, xerrors.Errorf("could not generate private key: %w", err)
		}
		pkb, err := pk.Bytes()
		if err != nil {
			return nil, xerrors.Errorf("could not serialize private key: %w", err)
		}
		err = ioutil.WriteFile(kpath, pkb, 0600)
		if err != nil {
			return nil, xerrors.Errorf("could not write private key: %w", err)
		}
	}

	if stat.Mode()&0066 != 0 {
		return nil, xerrors.New("libp2p identity has too wide access permissions, " +
			fsLibp2pKey + " should have permission 0600")
	}

	f, err := os.Open(kpath)
	if err != nil {
		return nil, xerrors.Errorf("could not open private key file: %w", err)
	}
	defer f.Close() //nolint: errcheck // read-only op

	pkbytes, err := ioutil.ReadAll(f)
	if err != nil {
		return nil, xerrors.Errorf("could not read private key file: %w", err)
	}

	pk, err := crypto.UnmarshalPrivateKey(pkbytes)
	if err != nil {
		return nil, xerrors.Errorf("could not unmarshal private key: %w", err)
	}
	return pk, nil
}

func (fsr *fsLockedRepo) SetAPIEndpoint(ma multiaddr.Multiaddr) error {
	if err := fsr.stillValid(); err != nil {
		return err
	}
	return ioutil.WriteFile(fsr.join(fsAPI), []byte(ma.String()), 0666)
}

func (fsr *fsLockedRepo) Wallet() (interface{}, error) {
	panic("not implemented")
}