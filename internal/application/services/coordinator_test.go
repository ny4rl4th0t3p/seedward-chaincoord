package services

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ny4rl4th0t3p/seedward-chaincoord/internal/application/ports"
)

type fakeCoordinatorRepo struct {
	addErr    error
	removeErr error
}

func (f *fakeCoordinatorRepo) Add(context.Context, string, string) error { return f.addErr }
func (f *fakeCoordinatorRepo) Remove(context.Context, string) error      { return f.removeErr }
func (*fakeCoordinatorRepo) Contains(context.Context, string) (bool, error) {
	return false, nil
}

func (*fakeCoordinatorRepo) List(context.Context, int, int) ([]*ports.CoordinatorAllowlistEntry, int, error) {
	return nil, 0, nil
}

func TestCoordinatorService_Add_Audited(t *testing.T) {
	audit := &fakeAuditLogWriter{}
	svc := NewCoordinatorService(&fakeCoordinatorRepo{}, audit)

	require.NoError(t, svc.Add(context.Background(), testAddr1, testAddr2))

	require.Len(t, audit.events, 1, "adding a coordinator must be audited")
	ev := audit.events[0]
	assert.Equal(t, "CoordinatorAdded", ev.EventName)
	assert.Equal(t, ports.GlobalAuditScope, ev.LaunchID, "global (non-launch) scope")
	assert.Contains(t, string(ev.Payload), mustAddr(testAddr1).Hex(), "payload records the canonical account hex")
}

func TestCoordinatorService_Remove_Audited(t *testing.T) {
	audit := &fakeAuditLogWriter{}
	svc := NewCoordinatorService(&fakeCoordinatorRepo{}, audit)

	require.NoError(t, svc.Remove(context.Background(), testAddr1, testAddr2))

	require.Len(t, audit.events, 1, "removing a coordinator must be audited")
	assert.Equal(t, "CoordinatorRemoved", audit.events[0].EventName)
}

func TestCoordinatorService_Add_RepoErrorNotAudited(t *testing.T) {
	audit := &fakeAuditLogWriter{}
	svc := NewCoordinatorService(&fakeCoordinatorRepo{addErr: assert.AnError}, audit)

	require.Error(t, svc.Add(context.Background(), testAddr1, testAddr2))
	assert.Empty(t, audit.events, "a failed add must not be audited")
}
