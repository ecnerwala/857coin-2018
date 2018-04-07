package coin

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"strings"
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

type Header struct {
	ParentID   Hash                  `json:"parentid"`
	MerkleRoot Hash                  `json:"root"`
	Difficulty uint64                `json:"difficulty"`
	Timestamp  int64                 `json:"timestamp"`
	Nonces     [3]uint64             `json:"nonces"`
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

func (h *Header) computeAAndB() (cipher.Block, cipher.Block) {
	b := make([]byte, 32+32+8+8+8+1)
	copy(b, h.ParentID[:])
	copy(b[32:], h.MerkleRoot[:])
	binary.BigEndian.PutUint64(b[32+32:], h.Difficulty)
	binary.BigEndian.PutUint64(b[32+32+8:], uint64(h.Timestamp))
	binary.BigEndian.PutUint64(b[32+32+8+8:], h.Nonces[0])
	b[32+32+8+8+8] = h.Version
	seed := sha256.Sum256(b)
	seed2 := sha256.Sum256(seed[:])
	A, _ := aes.NewCipher(seed[:])
	B, _ := aes.NewCipher(seed2[:])
	return A, B
}

func computeAES(block cipher.Block, m uint64) *big.Int {
	blockM := make([]byte, 16)
	binary.BigEndian.PutUint64(blockM, 0)
	binary.BigEndian.PutUint64(blockM[8:], m)
	blockC := make([]byte, 16)
	block.Encrypt(blockC, blockM)
	c := new(big.Int).SetBytes(blockC[:])
	return c
}

func computeHammingCloseness(Ai, Aj, Bi, Bj *big.Int) uint64 {
	int128 := new(big.Int).SetUint64(128)
	mod := new(big.Int).SetUint64(2)
	mod.Exp(mod, int128, nil)

	AiBj := new(big.Int).SetUint64(0)
	AiBj.Add(Ai, Bj)
	AiBj.Mod(AiBj, mod)
	AjBi := new(big.Int).SetUint64(0)
	AjBi.Add(Aj, Bi)
	AjBi.Mod(AjBi, mod)

	xor := new(big.Int).SetUint64(0)
	xor.Xor(AiBj, AjBi)
	s := fmt.Sprintf("%0128b", xor)
	return uint64(strings.Count(s, "0"))
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
	if h.Nonces[1] == h.Nonces[2] {
		return ErrInvalidPoW
	}

	A, B := h.computeAAndB()
	Ai := computeAES(A, h.Nonces[1])
	Aj := computeAES(A, h.Nonces[2])
	Bi := computeAES(B, h.Nonces[1])
	Bj := computeAES(B, h.Nonces[2])
	d := computeHammingCloseness(Ai, Aj, Bi, Bj)

	if d < h.Difficulty {
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

func (h *Header) MineBlock(b Block) {
	h.MerkleRoot = computeMerkleTreeV0(b)

	A, B := h.computeAAndB()
	aesA := make([]*big.Int, 0)
	aesB := make([]*big.Int, 0)
	for i := uint64(0); ; i++ {
		aesA = append(aesA, computeAES(A, i))
		aesB = append(aesB, computeAES(B, i))
		for j := uint64(0); j < i; j++ {
			if computeHammingCloseness(aesA[i], aesA[j], aesB[i], aesB[j]) >= h.Difficulty {
				h.Nonces[1] = i
				h.Nonces[2] = j
				return
			}
		}
	}
}
