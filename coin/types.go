package coin

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
)

var (
	bigOne = new(big.Int).SetUint64(1)
	bigTwo = new(big.Int).SetUint64(2)
)

var (
	ErrUnkownVersion    = errors.New("unknown version")
	ErrInvalidPoW       = errors.New("invalid PoW")
	ErrInvalidNonceHash = errors.New("Hash(n | data) cannt be 0")
	ErrBlockSize        = errors.New("block is too large")
)

type Hash [sha256.Size]byte

func NewHash(hexstr string) (Hash, error) {
	var h Hash
	b, err := hex.DecodeString(hexstr)
	if err != nil {
		return h, err
	}
	if len(b) != sha256.Size {
		return h, fmt.Errorf("short hash value")
	}
	copy(h[:], b)

	return h, nil
}

func (h Hash) String() string {
	return hex.EncodeToString(h[:])
}

// TODO speed up
func (h Hash) MarshalJSON() ([]byte, error) {
	return []byte("\"" + h.String() + "\""), nil
}

func (h *Hash) UnmarshalJSON(b []byte) (err error) {
	if b[0] != '"' || b[len(b)-1] != '"' {
		return fmt.Errorf("expecting string for hash value")
	}
	*h, err = NewHash(string(b[1 : len(b)-1]))

	return err
}

const (
	NumCollisions = 3
)

type Header struct {
	ParentID   Hash                  `json:"parentid"`
	MerkleRoot Hash                  `json:"root"`
	Difficulty uint64                `json:"difficulty"`
	Timestamp  int64                 `json:"timestamp"`
	Nonces     [NumCollisions]uint64 `json:"nonces"`
	Version    uint8                 `json:"version"`
}

const MAX_BLOCK_SIZE = 1000

type Block string

func (h *Header) Sum() Hash {
	b := make([]byte, 32+32+8+8+8+8+8+1)
	offset := copy(b, h.ParentID[:])
	offset += copy(b[offset:], h.MerkleRoot[:])
	binary.BigEndian.PutUint64(b[offset:], h.Difficulty)
	offset += 8
	binary.BigEndian.PutUint64(b[offset:], uint64(h.Timestamp))
	offset += 8
	for i, n := range h.Nonces {
		binary.BigEndian.PutUint64(b[offset+8*i:], n)
	}
	b[offset+24] = h.Version

	return sha256.Sum256(b)
}

func (h *Header) SumNonce(ni int) Hash {
	b := make([]byte, 32+32+8+8+8+1)
	offset := copy(b, h.ParentID[:])
	offset += copy(b[offset:], h.MerkleRoot[:])
	binary.BigEndian.PutUint64(b[offset:], h.Difficulty)
	offset += 8
	binary.BigEndian.PutUint64(b[offset:], uint64(h.Timestamp))
	offset += 8
	binary.BigEndian.PutUint64(b[offset:], h.Nonces[ni])
	b[offset+8] = h.Version

	return sha256.Sum256(b)
}

func (h *Header) Valid(b Block) error {
	if len(b) > MAX_BLOCK_SIZE {
		return ErrBlockSize
	}
	// Header first validation
	if err := h.validPoW(); err != nil {
		return err
	}

	// Ensure header commits block
	if err := h.validMerkleTree(b); err != nil {
		return err
	}

	return nil
}

func (h *Header) validPoW() error {
	dInt := new(big.Int).SetUint64(h.Difficulty)
	mInt := new(big.Int).SetUint64(2)
	mInt.Exp(mInt, dInt, nil)

	a := h.SumNonce(0)
	b := h.SumNonce(1)
	c := h.SumNonce(2)

	aInt := new(big.Int).SetBytes(a[:])
	bInt := new(big.Int).SetBytes(b[:])
	cInt := new(big.Int).SetBytes(c[:])

	aInt.Mod(aInt, mInt)
	bInt.Mod(bInt, mInt)
	cInt.Mod(cInt, mInt)

	if aInt.Cmp(bInt) != 0 || aInt.Cmp(cInt) != 0 {
		return ErrInvalidPoW
	}

	return nil
}

func (h *Header) validMerkleTree(b Block) error {
	if h.Version == 0 {
		if h.MerkleRoot == computeMerkleTreeV0(b) {
			return nil
		}
	}

	return ErrUnkownVersion
}

func computeMerkleTreeV0(b Block) Hash {
	return sha256.Sum256([]byte(b))
}
