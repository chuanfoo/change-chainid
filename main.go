package main

import (
	"bytes"
	"encoding/base64"
	"encoding/hex"
	"github.com/cosmos/cosmos-sdk/store"
	_ "github.com/cosmos/iavl"
	sm "github.com/tendermint/tendermint/state"
	"regexp"

	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	cfg "github.com/tendermint/tendermint/config"
	"github.com/tendermint/tendermint/node"
	dbm "github.com/tendermint/tm-db"

	"github.com/tendermint/tendermint/crypto"
	"github.com/tendermint/tendermint/crypto/ed25519"
	tmproto "github.com/tendermint/tendermint/proto/tendermint/types"
	tmstore "github.com/tendermint/tendermint/store"
	tmtypes "github.com/tendermint/tendermint/types"

)

func usage() {
	s := `Usage: -home [chain data dir] -id [new chainId] -k [keysfile(keep one key per line)]`
	fmt.Println(s)
}

var (
	regexChainID         = `[a-z]{1,}`
	regexEIP155Separator = `_{1}`
	regexEIP155          = `[1-9][0-9]*`
	regexEpochSeparator  = `-{1}`
	regexEpoch           = `[1-9][0-9]*`
	ethermintChainID     = regexp.MustCompile(fmt.Sprintf(`^(%s)%s(%s)%s(%s)$`,
		regexChainID,
		regexEIP155Separator,
		regexEIP155,
		regexEpochSeparator,
		regexEpoch))
)

// IsValidChainID returns false if the given chain identifier is incorrectly formatted.
func IsValidChainID(chainID string) bool {
	if len(chainID) > 48 {
		return false
	}

	return ethermintChainID.MatchString(chainID)
}

//change chainId
func main() {
	home := flag.String("home", "", "home")
	chainId := flag.String("id", "", "chainId")
	keys := flag.String("k", "", "keys file")
	flag.Parse()
	if *home == "" || *chainId == "" || *keys == "" {
		usage()
		return
	}
	if !IsValidChainID(*chainId) {
		fmt.Println("invalid chainId")
		return
	}

	pvs, err := readKeys(*keys)
	if err != nil {
		fmt.Println(err)
		return
	}

	conf := cfg.DefaultConfig()
	if *home != "" {
		conf.RootDir = *home
	}

	d := filepath.Join(conf.DBDir(), "state.db")
	if !Exists(d) {
		fmt.Printf("path not exits %s\n", d)
		return
	}

	appStateDb, err := initAppDBs(conf, node.DefaultDBProvider)
	if err != nil {
		fmt.Println(err)
		return
	}
	cms := store.NewCommitMultiStore(appStateDb)
	cms.LoadLatestVersion()

	stateDB, blockStore, err := initDBs(conf, node.DefaultDBProvider)
	if err != nil {
		fmt.Println(err)
		return
	}

	genesisDocProvider := node.DefaultGenesisDocProviderFunc(conf)

	state, _, err := node.LoadStateFromDBOrGenesisDocProvider(stateDB, genesisDocProvider)
	if err != nil {
		fmt.Println(err)
		return
	}
	// save last hash
	oldChainId := state.ChainID
	if oldChainId == *chainId {
		fmt.Printf("Failed!\n%s = %s\nNothing to change!\n", oldChainId, *chainId)
		return
	}

	lastBlockHeight := state.LastBlockHeight
	lastCommit := blockStore.LoadSeenCommit(lastBlockHeight)

	for idx, _ := range lastCommit.Signatures {
		v := lastCommit.GetVote(int32(idx)).ToProto()
		found := false
		addr := strings.ToUpper(hex.EncodeToString(v.ValidatorAddress))
		for _, pv := range pvs {
			if bytes.Equal(pv.PrivKey.PubKey().Address().Bytes(), v.ValidatorAddress) {
				pv.SignVote(*chainId, v)
				lastCommit.Signatures[idx].Signature = v.Signature
				found = true
				break
			}
		}
		if !found {
			panic(any("Signer Not Found;Signer Address: " + addr))
		}
	}

	err = blockStore.SaveSeenCommit(lastBlockHeight, lastCommit)
	if err != nil {
		fmt.Println(err)
		return
	}
	// save state
	state.ChainID = *chainId
	state.AppHash = cms.LastCommitID().Hash
	stateStore := sm.NewStore(stateDB)
	err = stateStore.Save(state)
	if err != nil {
		fmt.Println(err)
		return
	}
	fmt.Printf("Success!\nChanged chainId from %s to %s\n", oldChainId, *chainId)
}

func initDBs(config *cfg.Config, dbProvider node.DBProvider) (stateDB dbm.DB, blockStore *tmstore.BlockStore, err error) {
	blockStoreDB, err := dbProvider(&node.DBContext{ID: "blockstore", Config: config})
	if err != nil {
		return
	}
	blockStore = tmstore.NewBlockStore(blockStoreDB)

	stateDB, err = dbProvider(&node.DBContext{ID: "state", Config: config})
	if err != nil {
		return
	}
	return
}

func initAppDBs(config *cfg.Config, dbProvider node.DBProvider) (stateDB dbm.DB, err error) {
	stateDB, err = dbProvider(&node.DBContext{ID: "application", Config: config})
	if err != nil {
		return
	}
	return
}

func Exists(filename string) bool {
	stat, err := os.Stat(filename)
	if err != nil && os.IsNotExist(err) {
		return false
	}

	return stat.IsDir()
}

func readKeys(keyFile string) (pvs []*MockPV, err error) {
	bz, err := os.ReadFile(keyFile)
	if err != nil {
		return
	}
	lines := strings.Split(string(bz), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		pv := NewMockPVFromBase64(line)
		pvs = append(pvs, &pv)
	}
	return
}

type MockPV struct {
	PrivKey crypto.PrivKey
}

func NewMockPVFromBase64(b64PrivateKeyStr string) MockPV {
	bz, err := base64.StdEncoding.DecodeString(b64PrivateKeyStr)
	if err != nil {
		panic(any(err))
	}
	return MockPV{ed25519.PrivKey(bz)}
}

// Implements PrivValidator.
func (pv MockPV) SignVote(chainID string, vote *tmproto.Vote) error {
	useChainID := chainID
	signBytes := tmtypes.VoteSignBytes(useChainID, vote)
	sig, err := pv.PrivKey.Sign(signBytes)
	if err != nil {
		return err
	}
	vote.Signature = sig
	return nil
}

// Implements PrivValidator.
func (pv MockPV) SignProposal(chainID string, proposal *tmproto.Proposal) error {
	useChainID := chainID
	signBytes := tmtypes.ProposalSignBytes(useChainID, proposal)
	sig, err := pv.PrivKey.Sign(signBytes)
	if err != nil {
		return err
	}
	proposal.Signature = sig
	return nil
}

//func getRoots(dbIns *dbm.GoLevelDB) (map[int64][]byte, error) {
//	roots := map[int64][]byte{}
//	rootKeyFormat := iavl.NewKeyFormat('r', 8)
//
//	prefixDB := dbm.NewPrefixDB(dbIns, []byte("s/k:"))
//
//	itr, _ := dbm.IteratePrefix(prefixDB, rootKeyFormat.Key())
//	defer itr.Close()
//
//	for ; itr.Valid(); itr.Next() {
//		var version int64
//		rootKeyFormat.Scan(itr.Key(), &version)
//		roots[version] = itr.Value()
//	}
//
//	return roots, nil
//}