package services

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ny4rl4th0t3p/seedward-chaincoord/internal/domain"
)

// A bare event (no WithTime) is written synchronously the moment it happens, so the funnel must
// stamp it with the write time — the forensic log must never record the zero timestamp.
func TestRecordAudit_StampsOccurredAtWhenZero(t *testing.T) {
	audit := &fakeAuditLogWriter{}
	recordAudit(context.Background(), audit, zerolog.Nop(), "launch-1", domain.WindowOpened{LaunchID: uuid.New()})

	require.Len(t, audit.events, 1)
	assert.False(t, audit.events[0].OccurredAt.IsZero(), "a bare event must be stamped with the write time")
}

// An event carrying an authoritative domain time (e.g. a proposal's ExecutedAt, set via WithTime)
// keeps it — the funnel must not overwrite a non-zero timestamp with the write time.
func TestRecordAudit_PreservesExplicitOccurredAt(t *testing.T) {
	audit := &fakeAuditLogWriter{}
	ts := time.Date(2020, 1, 2, 3, 4, 5, 0, time.UTC)
	recordAudit(context.Background(), audit, zerolog.Nop(), "launch-1",
		domain.ValidatorApproved{LaunchID: uuid.New()}.WithTime(ts))

	require.Len(t, audit.events, 1)
	assert.Equal(t, ts, audit.events[0].OccurredAt, "an event carrying an authoritative time keeps it")
}
