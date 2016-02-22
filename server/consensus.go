package server

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math/big"
	"sync"
	"time"

	"github.com/cfromknecht/857coin/coin"
	db "github.com/syndtr/goleveldb/leveldb"
	"github.com/syndtr/goleveldb/leveldb/util"
)

const (
	targetBlockInterval      = 10 * time.Minute
	difficultyRetargetLength = 24 * time.Hour
	difficultyRetargetWindow = uint32(difficultyRetargetLength / targetBlockInterval)

	BlockchainPath = "blockchain.db"

	HeaderBucket = "HEADER-"
	BlockBucket  = "BLOCK-"

	MinimumDifficulty = 230551
)

var genesisHeader coin.Header

var (
	ErrNoBlockAtHeight = func(i uint32) error {
		return fmt.Errorf("block at height %d not found in heightToHash map", i)
	}

	ErrHeaderExhausted = errors.New("exhausted all possible nonces")
)

type (
	blockchain struct {
		sync.Mutex

		head           processedHeader
		currDifficulty uint64

		scores       map[string]int
		heightToHash map[uint32]coin.Hash

		db *db.DB
	}

	compositeBlock struct {
		Header coin.Header `json:"header"`
		Block  coin.Block  `json:"block"`
	}

	processedHeader struct {
		Header          coin.Header `json:"header"`
		BlockHeight     uint32      `json:"blockheight"`
		IsMainChain     bool        `json:"ismainchain"`
		TotalDifficulty uint64      `json:"totaldiff"`
	}
)

func newBlockchain() (*blockchain, error) {
	bc := &blockchain{
		currDifficulty: MinimumDifficulty,
	}
	bc.initDB()

	if err := bc.loadScores(); err != nil {
		return nil, err
	}

	if err := bc.loadHeightToHash(); err != nil {
		return nil, err
	}

	// Mine genesis block if necessary
	if _, ok := bc.heightToHash[0]; !ok {
		fmt.Println("Mining genesis block...")
		if err := bc.mineGenesisBlock(); err != nil {
			return nil, err
		}
	}

	return bc, nil
}

/*
 * Initialization
 */

func (bc *blockchain) initDB() {
	bcdb, err := db.OpenFile(BlockchainPath, nil)
	if err != nil {
		log.Println(err)
		panic("unable to open blockchain database")
	}
	bc.db = bcdb
}

func (bc *blockchain) mineGenesisBlock() error {
	gh := coin.Header{
		MerkleRoot: coin.Hash{0xe3, 0xb0, 0xc4, 0x42, 0x98, 0xfc, 0x1c, 0x14, 0x9a, 0xfb,
			0xf4, 0xc8, 0x99, 0x6f, 0xb9, 0x24, 0x27, 0xae, 0x41, 0xe4, 0x64, 0x9b,
			0x93, 0x4c, 0xa4, 0x95, 0x99, 0x1b, 0x78, 0x52, 0xb8, 0x55},
		Difficulty: MinimumDifficulty,
		Timestamp:  time.Now(),
	}

	for i := uint64(0); i < gh.Difficulty; i++ {
		gh.Nonces[0] = i
		for j := uint64(0); j < gh.Difficulty; j++ {
			gh.Nonces[1] = j
			for k := uint64(0); k < gh.Difficulty; k++ {
				gh.Nonces[2] = k
				if nil == gh.Valid("") {
					fmt.Println("genesis header found:", gh)
					genesisHeader = gh
					return bc.AddBlock(gh, "")
				}
			}
		}
	}

	return ErrHeaderExhausted
}

func (bc *blockchain) loadScores() error {
	bc.scores = make(map[string]int)

	// Iterate over all headers, add to score if version 0
	iter := bc.db.NewIterator(util.BytesPrefix([]byte(HeaderBucket)), nil)
	for iter.Next() {
		// Load header
		headerBytes := iter.Value()
		var pheader processedHeader
		if err := json.Unmarshal(headerBytes, &pheader); err != nil {
			return err
		}

		// Add to score map if version 0
		if pheader.Header.Version == 0 {
			// Load block data and convert to string
			teamname, err := bc.getBlock(pheader.Header.Sum())
			if err != nil {
				continue
			}

			// Increment team's score
			if total, ok := bc.scores[teamname]; ok {
				bc.scores[teamname] = total + 1
			} else {
				bc.scores[teamname] = 1
			}
		}
	}

	return nil
}

func (bc *blockchain) loadHeightToHash() error {
	bc.heightToHash = make(map[uint32]coin.Hash)

	maxHeight := uint32(0)
	iter := bc.db.NewIterator(util.BytesPrefix([]byte(HeaderBucket)), nil)
	for iter.Next() {
		// Unmarshal processedHeader
		b := iter.Value()
		var pheader processedHeader
		err := json.Unmarshal(b, &pheader)
		if err != nil {
			return err
		}

		// Add to heightToHash map if header is in main chain
		if pheader.IsMainChain {
			bc.heightToHash[pheader.BlockHeight] = pheader.Header.Sum()
			if pheader.BlockHeight > maxHeight {
				maxHeight = pheader.BlockHeight
				bc.head = pheader
			}
		}
	}

	fmt.Println("heightToHash initialized:", len(bc.heightToHash))

	return nil
}

/*
 * Consensus Set
 */

func (bc *blockchain) AddBlock(h coin.Header, b coin.Block) error {
	bc.Lock()
	defer bc.Unlock()

	// Only process valid blocks
	if err := h.Valid(b); err != nil {
		return err
	}

	// Build processedHeader
	ph, err := bc.processHeader(h)
	if err != nil {
		return err
	}

	batch := &db.Batch{}
	remap := make(map[uint32]coin.Hash)
	if ph.TotalDifficulty > bc.head.TotalDifficulty {
		if err := bc.swapMainFork(ph, batch, remap); err != nil {
			return err
		}
	}

	headerBytes, err := json.Marshal(ph)
	if err != nil {
		return err
	}

	id := ph.Header.Sum()

	hid := bucket(HeaderBucket, id)
	bid := bucket(BlockBucket, id)
	batch.Put(hid, headerBytes)
	batch.Put(bid, []byte(b))

	if err := bc.db.Write(batch, nil); err != nil {
		return err
	}

	if ph.IsMainChain {
		for height, hash := range remap {
			bc.heightToHash[height] = hash
		}
		bc.heightToHash[ph.BlockHeight] = ph.Header.Sum()
		bc.head = *ph

		log.Printf("[Update Tip] height: %d diff: %d id: %s\n", ph.BlockHeight,
			ph.TotalDifficulty, ph.Header.Sum())

		// Adjust difficulty?
		diff, err := bc.newDifficultyTarget()
		if err != nil {
			return err
		}
		bc.currDifficulty = diff
	} else {
		log.Printf("[Block Recorded] height: %d diff: %d id: %s\n", ph.BlockHeight,
			ph.TotalDifficulty, ph.Header.Sum())
	}

	return nil
}

func (bc *blockchain) swapMainFork(ph *processedHeader, batch *db.Batch,
	remap map[uint32]coin.Hash) error {

	// Find most recent fork with main chain, starting from ph.  Memoize
	// intermediate headers

	if ph.BlockHeight == 0 {
		ph.IsMainChain = true
		return nil
	}

	sideheaders := []processedHeader{}
	sideph := *ph
	for {
		tempph, err := bc.getHeader(sideph.Header.ParentID)
		if err != nil {
			return err
		}
		sideph = *tempph

		if sideph.IsMainChain {
			break
		}

		sideheaders = append(sideheaders, sideph)
	}

	// Memoize headers in main fork
	mainheaders := []processedHeader{}
	for i := bc.head.BlockHeight; i > sideph.BlockHeight; i-- {
		id, ok := bc.heightToHash[i]
		if !ok {
			return ErrNoBlockAtHeight(i)
		}

		mainph, err := bc.getHeader(id)
		if err != nil {
			return err
		}

		mainheaders = append(mainheaders, *mainph)
	}

	// Revert main chain
	for i := len(mainheaders) - 1; i >= 0; i-- {
		mph := mainheaders[i]
		mph.IsMainChain = false

		headerBytes, err := json.Marshal(mph)
		if err != nil {
			return err
		}

		id := bucket(HeaderBucket, mph.Header.Sum())
		batch.Put(id, headerBytes)
	}

	// Apply side chain
	for i := len(sideheaders) - 1; i >= 0; i-- {
		sph := sideheaders[i]
		sph.IsMainChain = true

		headerBytes, err := json.Marshal(sph)
		if err != nil {
			return err
		}

		id := sph.Header.Sum()
		hid := bucket(HeaderBucket, sph.Header.Sum())
		batch.Put(hid, headerBytes)

		remap[sph.BlockHeight] = id
	}

	fmt.Println("Forking:", len(mainheaders), len(sideheaders))

	ph.IsMainChain = true

	return nil
}

/*
 * Difficulty Retargeting
 */

func (s *blockchain) newDifficultyTarget() (uint64, error) {
	if s.head.BlockHeight%difficultyRetargetWindow != difficultyRetargetWindow-1 {
		return s.currDifficulty, nil
	}

	pastHeaderHeight := 1 + s.head.BlockHeight - difficultyRetargetWindow
	pastHeaderID, ok := s.heightToHash[pastHeaderHeight]
	if !ok {
		return 0, fmt.Errorf("unknown retargeting ID")
	}

	pastHeader, err := s.getHeader(pastHeaderID)
	if err != nil {
		return 0, fmt.Errorf("unknown retargeting header")
	}

	head := s.head.Header
	windowTime := head.Timestamp.Sub(pastHeader.Header.Timestamp) / time.Second
	windowDifficulty := s.head.Header.Difficulty

	newDiffMin := uint64(targetBlockInterval) * windowDifficulty / uint64(windowTime)
	// Clamp to maximum of 4x increase/decrease
	if newDiffMin > 4*windowDifficulty {
		newDiffMin = 4 * windowDifficulty
	} else if newDiffMin < windowDifficulty/4 {
		newDiffMin = windowDifficulty / 4
	}
	// Increase by 1.5 to predict increase in mining activity
	newDiffMin = 3 * newDiffMin / 2

	return findNextPrime(newDiffMin)
}

func findNextPrime(nmin uint64) (uint64, error) {
	n := new(big.Int).SetUint64(nmin)
	for {
		if n.ProbablyPrime(4) {
			return n.Uint64(), nil
		}

		b := make([]byte, 1)
		if _, err := rand.Read(b); err != nil {
			return 0, err
		}
		bInt := new(big.Int).SetBytes(b)
		n.Add(n, bInt)
	}
}

/*
 * Header Metadata
 */

func (bc *blockchain) processHeader(h coin.Header) (*processedHeader, error) {
	if len(bc.heightToHash) == 0 {
		// Process genesis header
		return &processedHeader{
			Header:          h,
			BlockHeight:     0,
			TotalDifficulty: h.Difficulty,
			IsMainChain:     true,
		}, nil
	} else {
		// Check that block extends existing header
		prevHeader, err := bc.getHeader(h.ParentID)
		if err != nil {
			return nil, err
		}

		return &processedHeader{
			Header:          h,
			BlockHeight:     prevHeader.BlockHeight + 1,
			TotalDifficulty: prevHeader.TotalDifficulty + h.Difficulty,
			IsMainChain:     false,
		}, nil
	}
}

/*
 * Header/Block Database Wrappers (GET/PUT)
 */

func (bc *blockchain) getHeader(h coin.Hash) (*processedHeader, error) {
	id := bucket(HeaderBucket, h)
	headerBytes, err := bc.db.Get(id, nil)
	if err != nil {
		return nil, err
	}

	var pheader *processedHeader
	err = json.Unmarshal(headerBytes, &pheader)

	return pheader, err
}

func (bc *blockchain) putHeader(ph processedHeader) error {
	headerJson, err := json.Marshal(ph)
	if err != nil {
		return err
	}
	id := bucket(HeaderBucket, ph.Header.Sum())

	return bc.db.Put(id, headerJson, nil)
}

func (bc *blockchain) getBlock(h coin.Hash) (string, error) {
	id := bucket(BlockBucket, h)
	blockBytes, err := bc.db.Get(id, nil)
	if err != nil {
		return "", err
	}

	return string(blockBytes), nil
}

func (bc *blockchain) putBlock(h coin.Hash, b coin.Block) error {
	id := bucket(BlockBucket, h)
	return bc.db.Put(id, []byte(b), nil)
}

/*
 * Hash ID Bucketing
 */

func bucket(b string, h coin.Hash) []byte {
	return append([]byte(b), h[:]...)
}
