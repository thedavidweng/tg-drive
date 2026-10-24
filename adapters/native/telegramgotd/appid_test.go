package telegramgotd

import (
	"math"
	"testing"
)

func TestAppIDRejectsOutOfRange(t *testing.T) {
	cases := map[int64]int{
		0:                 0,
		42:                42,
		math.MaxInt32:     math.MaxInt32,
		math.MaxInt32 + 1: 0,
		-1:                0,
	}
	for in, want := range cases {
		if got := appID(in); got != want {
			t.Errorf("appID(%d) = %d, want %d", in, got, want)
		}
	}
}
