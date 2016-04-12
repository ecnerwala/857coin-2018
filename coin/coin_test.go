package coin

import (
	"fmt"
	"testing"
)

var (
	ts = int64(1460392042611995593)
)

func TestValidPoW(t *testing.T) {
	tests := []struct {
		diff   uint64
		nonces [NumCollisions]uint64
		err    error
	}{
		{
			diff:   5,
			nonces: [NumCollisions]uint64{13, 17, 25},
		},
		{
			diff:   5,
			nonces: [NumCollisions]uint64{22, 31, 15},
		},
		{
			diff:   5,
			nonces: [NumCollisions]uint64{17, 29, 25},
		},
		{
			diff:   5,
			nonces: [NumCollisions]uint64{0, 0, 0},
			err:    ErrInvalidPoW,
		},
		{
			diff:   5,
			nonces: [NumCollisions]uint64{4, 4, 4},
			err:    ErrInvalidPoW,
		},
		{
			diff:   5,
			nonces: [NumCollisions]uint64{6, 2, 1},
			err:    ErrInvalidPoW,
		},
	}

	for i, test := range tests {
		header := Header{
			Difficulty: test.diff,
			Timestamp:  ts,
			Nonces:     test.nonces,
		}

		check := header.validPoW()
		if check != test.err {
			t.Error(fmt.Errorf("[TestValidPoW] test #%d -- unexpected behavior: "+
				"want %s, got %s", i, test.err, check))
		}
	}
}
