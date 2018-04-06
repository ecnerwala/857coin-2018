package server

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math"
	"math/big"
	"sync"
	"time"

	"../coin"
	db "github.com/syndtr/goleveldb/leveldb"
	"github.com/syndtr/goleveldb/leveldb/util"
)

const (
	targetBlockInterval      = 10 * 60 * 1000 * 1000 * 1000      // 10 minutes
	difficultyRetargetLength = 24 * 60 * 60 * 1000 * 1000 * 1000 // 1 day
	difficultyRetargetWindow = uint64(difficultyRetargetLength / targetBlockInterval)
	maxClockDrift            = 2 * 60 * 1000 * 1000 * 1000 // 2 minutes

	BlockchainPath = "blockchain.db"

	HeaderBucket = "HEADER-"
	BlockBucket  = "BLOCK-"

	MinimumDifficulty = uint64(86)
)

var genesisHeader coin.Header

var (
	ErrHeaderExhausted = errors.New("exhausted all possible nonces")
	ErrClockDrift      = errors.New("excessive clock drift")
	ErrSpamHeader      = errors.New("header previously submitted")
	ErrDifficulty      = errors.New("invalid difficulty")
)

type (
	blockchain struct {
		sync.Mutex

		head           processedHeader
		currDifficulty uint64

		scores       map[string]int
		mainscores   map[string]int
		everscores   map[string]int
		heightToHash map[uint64]coin.Hash

		spam map[coin.Hash]struct{}

		db *db.DB
	}

	exploreBlock struct {
		ID              coin.Hash   `json:"id"`
		Header          coin.Header `json:"header"`
		Block           coin.Block  `json:"block"`
		BlockHeight     uint64      `json:"blockheight"`
		IsMainChain     bool        `json:"ismainchain"`
		EverMainChain   bool        `json:"evermainchain"`
		TotalDifficulty uint64      `json:"totaldiff"`
		Timestamp       time.Time   `json:"timestamp"`
	}

	compositeBlock struct {
		Header coin.Header `json:"header"`
		Block  coin.Block  `json:"block"`
	}

	processedHeader struct {
		Header          coin.Header `json:"header"`
		BlockHeight     uint64      `json:"blockheight"`
		IsMainChain     bool        `json:"ismainchain"`
		EverMainChain   bool        `json:"evermainchain"`
		TotalDifficulty uint64      `json:"totaldiff"`
	}
)

func newBlockchain() (*blockchain, error) {
	bc := &blockchain{
		currDifficulty: MinimumDifficulty,
		spam:           make(map[coin.Hash]struct{}),
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
		log.Println("Mining genesis block...")
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
	msg := "Never roll your own crypto"
	b := coin.Block(msg)

	genesisHeader = coin.Header{
		MerkleRoot: sha256.Sum256([]byte(msg)),
		Difficulty: MinimumDifficulty,
		Timestamp:  time.Now().UnixNano(),
	}

	A, B := genesisHeader.ComputeAAndB()
	aesA := make([]*big.Int, 0)
	aesB := make([]*big.Int, 0)
	for i := uint64(0); i >= 0; i++ {
		aesA = append(aesA, coin.ComputeAES(A, i))
		aesB = append(aesB, coin.ComputeAES(B, i))
		for j := uint64(0); j < i; j++ {
			if coin.ComputeHammingCloseness(aesA[i], aesA[j], aesB[i], aesB[j]) >= genesisHeader.Difficulty {
				genesisHeader.Nonces[1] = i
				genesisHeader.Nonces[2] = j
				return bc.AddBlock(genesisHeader, b)
			}
		}
	}
	return nil
}

func (bc *blockchain) loadScores() error {
	bc.scores = make(map[string]int)
	bc.mainscores = make(map[string]int)
	bc.everscores = make(map[string]int)

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

			// Increment total score
			if total, ok := bc.scores[teamname]; ok {
				bc.scores[teamname] = total + 1
			} else {
				bc.scores[teamname] = 1
			}

			// Increment main chain score
			if pheader.IsMainChain {
				// Increment team's score
				if total, ok := bc.mainscores[teamname]; ok {
					bc.mainscores[teamname] = total + 1
				} else {
					bc.mainscores[teamname] = 1
				}
			}

			// Increment ever in main chain score
			if pheader.EverMainChain {
				// Increment team's score
				if total, ok := bc.everscores[teamname]; ok {
					bc.everscores[teamname] = total + 1
				} else {
					bc.everscores[teamname] = 1
				}
			}
		}
	}

	return nil
}

func (bc *blockchain) loadHeightToHash() error {
	bc.heightToHash = make(map[uint64]coin.Hash)

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
	diff, err := bc.computeDifficulty(bc.head.Header.Sum())
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
	if h.Difficulty < MinimumDifficulty {
		return ErrDifficulty
	}

	// Check that timestamp is within 2 minutes of now
	diff := int64(h.Timestamp) - time.Now().UnixNano()
	if diff > maxClockDrift || diff < -maxClockDrift {
		return ErrClockDrift
	}

	// Only process valid blocks
	if err := h.Valid(b); err != nil {
		return err
	}

	bc.Lock()
	defer bc.Unlock()

	// Check spam filter
	id := h.Sum()
	if _, ok := bc.spam[id]; ok {
		return ErrSpamHeader
	}

	// Check database for existing header
	if _, err := bc.getHeader(id); err == nil {
		bc.spam[id] = struct{}{}
		return ErrSpamHeader
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
		ph.EverMainChain = true

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

	headerTime := time.Unix(0, ph.Header.Timestamp)
	if !ph.IsMainChain {
		log.Printf("[Side Chain] height: %d diff: %d id: %s time: %s\n",
			ph.BlockHeight, ph.TotalDifficulty, ph.Header.Sum(), headerTime)
		return nil
	}

	if err := bc.loadHeightToHash(); err != nil {
		return err
	}

	log.Printf("[Main Chain] height: %d diff: %d id: %s time: %s\n",
		ph.BlockHeight, ph.TotalDifficulty, ph.Header.Sum(), headerTime)

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
			return fmt.Errorf("block at height %d not found in heightToHash map", i)
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
		sph.EverMainChain = true

		headerBytes, err := json.Marshal(sph)
		if err != nil {
			return err
		}

		hid := bucket(HeaderBucket, sph.Header.Sum())
		batch.Put(hid, headerBytes)
	}

	if len(mainheaders) != 0 && len(sideheaders) != 0 {
		log.Printf("[Fork] main: %d, side %d\n", len(mainheaders), len(sideheaders))
	}

	ph.IsMainChain = true
	ph.EverMainChain = true

	return nil
}

/*
 * Difficulty Retargeting
 */

func (bc *blockchain) computeDifficulty(id coin.Hash) (uint64, error) {
	if len(bc.heightToHash) == 0 {
		return MinimumDifficulty, nil
	}

	header, err := bc.getHeader(id)
	if err != nil {
		return 0, err
	}

	h := header.BlockHeight

	retargetOffset := h % difficultyRetargetWindow
	pastHeaderHeight := h - retargetOffset

	pastHeader := header

	for pastHeader.BlockHeight > pastHeaderHeight {
		// Skip along the main chain if possible
		if mainID, ok := bc.heightToHash[pastHeader.BlockHeight]; ok {
			if pastHeader.Header.Sum() == mainID {
				pastHeaderID := bc.heightToHash[pastHeaderHeight]
				pastHeader, err = bc.getHeader(pastHeaderID)
				if err != nil {
					return 0, err
				}
				break
			}
		}

		pastHeader, err = bc.getHeader(pastHeader.Header.ParentID)
		if err != nil {
			return 0, err
		}
	}

	// TODO: this shouldn't be pastHeader's actual difficulty, but the past target difficulty
	// We should either store past target difficulty or enforce difficulty == targetDifficulty
	if h%difficultyRetargetWindow != difficultyRetargetWindow-1 {
		return pastHeader.Header.Difficulty, nil
	}

	windowTime := header.Header.Timestamp - pastHeader.Header.Timestamp

	var ratio float64
	if windowTime <= 0 {
		ratio = math.Inf(+1)
	} else {
		ratio = float64(targetBlockInterval) * float64(difficultyRetargetWindow) / float64(windowTime)
	}
	logRatio := math.Log2(ratio)

	log.Printf("[Difficulty] log2: %f ratio: %f time: %d interval %d\n", logRatio,
		ratio, windowTime, targetBlockInterval)

	// Clamp to maximum of 4x increase/decrease
	// TODO: Ditto on past difficulty vs past target difficulty
	newDifficulty := pastHeader.Header.Difficulty
	if logRatio > 2 {
		newDifficulty += 2
	} else if logRatio < -2 {
		newDifficulty -= 2
	} else if logRatio < 0 {
		newDifficulty -= uint64(-logRatio)
	} else if logRatio > 0 {
		newDifficulty += uint64(logRatio)
	} else {
		// 0 or NaN, just no-op
	}

	// Ensure at minimum
	if newDifficulty < MinimumDifficulty {
		newDifficulty = MinimumDifficulty
	}

	return newDifficulty, nil
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

		targetDiff, err := bc.computeDifficulty(h.ParentID)
		if err != nil {
			return nil, err
		}

		if h.Difficulty < targetDiff {
			return nil, ErrDifficulty
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
