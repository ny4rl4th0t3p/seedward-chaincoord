package services

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/ny4rl4th0t3p/seedward-chaincoord/internal/application/ports"
	"github.com/ny4rl4th0t3p/seedward-chaincoord/internal/domain/launch"
)

type fakeRehearsalAttemptRepo struct {
	byID  map[uuid.UUID]*launch.RehearsalAttempt
	byKey map[string]*launch.RehearsalAttempt // launchID|hash
}

func newFakeRehearsalAttemptRepo() *fakeRehearsalAttemptRepo {
	return &fakeRehearsalAttemptRepo{
		byID:  make(map[uuid.UUID]*launch.RehearsalAttempt),
		byKey: make(map[string]*launch.RehearsalAttempt),
	}
}

func (f *fakeRehearsalAttemptRepo) GetOrCreate(
	_ context.Context, launchID uuid.UUID, inputSetHash string, issuedAt time.Time,
) (*launch.RehearsalAttempt, error) {
	key := launchID.String() + "|" + inputSetHash
	if a, ok := f.byKey[key]; ok {
		return a, nil
	}
	a := &launch.RehearsalAttempt{
		ID:           uuid.New(),
		LaunchID:     launchID,
		InputSetHash: inputSetHash,
		IssuedAt:     issuedAt,
		Status:       launch.AttemptOpen,
	}
	f.byKey[key] = a
	f.byID[a.ID] = a
	return a, nil
}

func (f *fakeRehearsalAttemptRepo) FindByID(_ context.Context, id uuid.UUID) (*launch.RehearsalAttempt, error) {
	if a, ok := f.byID[id]; ok {
		return a, nil
	}
	return nil, ports.ErrNotFound
}

type fakeRehearsalResultRepo struct {
	byLaunch map[uuid.UUID][]*launch.RehearsalResult
	sigs     map[string]bool
}

func newFakeRehearsalResultRepo() *fakeRehearsalResultRepo {
	return &fakeRehearsalResultRepo{
		byLaunch: make(map[uuid.UUID][]*launch.RehearsalResult),
		sigs:     make(map[string]bool),
	}
}

func (f *fakeRehearsalResultRepo) Save(_ context.Context, res *launch.RehearsalResult) error {
	if f.sigs[res.Signature] {
		return nil // idempotent on signature
	}
	f.sigs[res.Signature] = true
	f.byLaunch[res.LaunchID] = append(f.byLaunch[res.LaunchID], res)
	return nil
}

func (f *fakeRehearsalResultRepo) FindByLaunch(_ context.Context, launchID uuid.UUID) ([]*launch.RehearsalResult, error) {
	return f.byLaunch[launchID], nil
}
