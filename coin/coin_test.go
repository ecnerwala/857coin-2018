package coin

import (
	"fmt"
	"testing"
	"time"
)

var (
	ts, _ = time.Parse(time.RFC3339, "2016-02-21T12:08:41+00:00")
)

func TestVerifyPoW(t *testing.T) {
	tests := []struct {
		diff    uint64
		nonces  [NumCollisions]uint32
		verfies bool
	}{
		{
			diff:    7,
			nonces:  [NumCollisions]uint32{0, 0, 0},
			verfies: true,
		},
		{
			diff:    7,
			nonces:  [NumCollisions]uint32{1, 1, 1},
			verfies: false,
		},
		{
			diff:    7,
			nonces:  [NumCollisions]uint32{1, 2, 3},
			verfies: false,
		},
		{
			diff:    7,
			nonces:  [NumCollisions]uint32{4, 4, 4},
			verfies: true,
		},
		{
			diff:    7,
			nonces:  [NumCollisions]uint32{4, 5, 3},
			verfies: false,
		},
		{
			diff:    7,
			nonces:  [NumCollisions]uint32{6, 2, 1},
			verfies: true,
		},
	}

	for i, test := range tests {
		header := Header{
			Difficulty: test.diff,
			Timestamp:  ts,
			Nonces:     test.nonces,
		}

		check := header.verifyPoW()
		if check != test.verfies {
			fmt.Errorf("[TestVerifyPoW] test #%d -- unexpected behavior: "+
				"want %s, got %s", i, test.verfies, check)
		}
	}

	for i := uint32(0); i < 49979687; i++ {
		for j := uint32(0); j < 49979687; j++ {
			for k := uint32(0); k < 49979687; k++ {
				header := Header{
					Difficulty: 49979687,
					Timestamp:  ts,
					Nonces:     [NumCollisions]uint32{i, j, k},
				}
				if header.verifyPoW() {
					fmt.Println("Found collision:", i, j, k)
				}
			}
		}
	}
}
