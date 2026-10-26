package license

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestExpiredNilCurrent(t *testing.T) {
	SetCurrent(nil)
	require.False(t, Expired())
}

func TestExpiredPast(t *testing.T) {
	SetCurrent(&Entitlement{ExpiresAt: time.Now().Add(-time.Hour)})
	defer SetCurrent(nil)
	require.True(t, Expired())
}

func TestExpiredFuture(t *testing.T) {
	SetCurrent(&Entitlement{ExpiresAt: time.Now().Add(time.Hour)})
	defer SetCurrent(nil)
	require.False(t, Expired())
}

func TestSeatLimitNil(t *testing.T) {
	SetCurrent(nil)
	require.Equal(t, 0, SeatLimit())
}

func TestSeatLimitValue(t *testing.T) {
	SetCurrent(&Entitlement{Seats: 10})
	defer SetCurrent(nil)
	require.Equal(t, 10, SeatLimit())
}

func TestHasFeatureNil(t *testing.T) {
	SetCurrent(nil)
	require.True(t, HasFeature("anything"))
}

func TestHasFeatureEmpty(t *testing.T) {
	SetCurrent(&Entitlement{})
	defer SetCurrent(nil)
	require.True(t, HasFeature("anything"))
}

func TestHasFeatureMatch(t *testing.T) {
	SetCurrent(&Entitlement{Features: []string{"content_sec", "mp_release"}})
	defer SetCurrent(nil)
	require.True(t, HasFeature("mp_release"))
	require.False(t, HasFeature("unknown"))
}
