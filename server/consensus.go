package server

import (
	"crypto/sha256"
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

	//	MinimumDifficulty = 7
	//	MinimumDifficulty = 15485863
	MinimumDifficulty = 67867967
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
	msg := "Never roll your own crypto."

	gh := coin.Header{
		MerkleRoot: sha256.Sum256([]byte(msg)),
		Difficulty: MinimumDifficulty,
		Timestamp:  time.Now().Unix(),
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
					return bc.AddBlock(gh, coin.Block(msg))
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

	maxDifficulty := uint64(0)
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
			id := pheader.Header.Sum()
			bc.heightToHash[pheader.BlockHeight] = id

			if pheader.TotalDifficulty > maxDifficulty {
				maxDifficulty = pheader.TotalDifficulty
				bc.head = pheader
			}
		}
	}

	// Calculate Difficulty
	diff, err := bc.currDifficultyTarget()
	if err != nil {
		return err
	}
	bc.currDifficulty = diff

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

	return bc.extendChain(ph, b)
}

func (bc *blockchain) extendChain(ph *processedHeader, b coin.Block) error {
	batch := &db.Batch{}

	if ph.BlockHeight == 0 {
		ph.IsMainChain = true

	} else if ph.TotalDifficulty > bc.head.TotalDifficulty {
		if err := bc.forkMainChain(ph, b, batch); err != nil {
			return err
		}
	}

	headerBytes, err := json.Marshal(ph)
	if err != nil {
		return err
	}

	// Write current header and block
	id := ph.Header.Sum()
	hid := bucket(HeaderBucket, id)
	bid := bucket(BlockBucket, id)
	batch.Put(hid, headerBytes)
	batch.Put(bid, []byte(b))

	if err := bc.db.Write(batch, nil); err != nil {
		return err
	}

	if !ph.IsMainChain {
		log.Printf("[Side Chain] height: %d diff: %d id: %s time: %d\n",
			ph.BlockHeight, ph.TotalDifficulty, ph.Header.Sum(), ph.Header.Timestamp)
		return nil
	}

	if err := bc.loadHeightToHash(); err != nil {
		return err
	}

	log.Printf("[Main Chain] height: %d diff: %d id: %s time: %d\n",
		ph.BlockHeight, ph.TotalDifficulty, ph.Header.Sum(), ph.Header.Timestamp)

	return nil
}

func (bc *blockchain) forkMainChain(ph *processedHeader, b coin.Block,
	batch *db.Batch) error {

	// Find most recent fork with main chain, starting from ph.  Memoize
	// intermediate headers
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

		sideheaders = append([]processedHeader{sideph}, sideheaders...)
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

		mainheaders = append([]processedHeader{*mainph}, mainheaders...)
	}

	// Revert main chain
	for _, mph := range mainheaders {
		mph.IsMainChain = false

		headerBytes, err := json.Marshal(mph)
		if err != nil {
			return err
		}

		id := bucket(HeaderBucket, mph.Header.Sum())
		batch.Put(id, headerBytes)
	}

	// Apply side chain
	for _, sph := range sideheaders {
		sph.IsMainChain = true

		headerBytes, err := json.Marshal(sph)
		if err != nil {
			return err
		}

		hid := bucket(HeaderBucket, sph.Header.Sum())
		batch.Put(hid, headerBytes)
	}

	fmt.Printf("[Fork] main: %d, side %d\n", len(mainheaders), len(sideheaders))
	ph.IsMainChain = true

	return nil
}

/*
 * Difficulty Retargeting
 */

func (s *blockchain) currDifficultyTarget() (uint64, error) {
	headHeight := s.head.BlockHeight
	if headHeight < difficultyRetargetWindow-1 {
		return s.head.Header.Difficulty, nil
	}

	retargetOffset := headHeight % difficultyRetargetWindow
	pastHeaderHeight := headHeight - retargetOffset

	fmt.Printf("Retrieving header at height %d for head height %d\n", pastHeaderHeight, s.head.BlockHeight)

	pastHeaderID, ok := s.heightToHash[pastHeaderHeight]
	if !ok {
		return 0, fmt.Errorf("unknown retargeting ID")
	}

	pastHeader, err := s.getHeader(pastHeaderID)
	if err != nil {
		return 0, fmt.Errorf("unknown retargeting header")
	}

	if s.head.BlockHeight%difficultyRetargetWindow != difficultyRetargetWindow-1 {
		return pastHeader.Header.Difficulty, nil
	}

	head := s.head.Header
	//	Convert to seconds
	windowTime := head.Timestamp - pastHeader.Header.Timestamp
	fmt.Printf("Window Time: %d\n", windowTime)
	windowDifficulty := s.head.Header.Difficulty
	fmt.Printf("Window Difficulty: %d\n", windowDifficulty)
	fmt.Printf("Target Interval: %d\n", targetBlockInterval)

	newDiffMin := uint64(targetBlockInterval) * windowDifficulty / uint64(windowTime)
	fmt.Printf("NewDiffMin %d\n", newDiffMin)

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
	bigOne := new(big.Int).SetUint64(1)
	n := new(big.Int).SetUint64(nmin)
	for {
		if n.ProbablyPrime(4) {
			return n.Uint64(), nil
		}
		n.Add(n, bigOne)
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
