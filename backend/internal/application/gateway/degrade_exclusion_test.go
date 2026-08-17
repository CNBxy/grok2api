package gateway

import (
	"context"
	"testing"

	"github.com/chenyme/grok2api/backend/internal/domain/account"
	"github.com/chenyme/grok2api/backend/internal/repository"
)

func TestShouldSkipDegradedCandidateFiltersBuildAccountsAndDisabledNodes(t *testing.T) {
	selector := NewSelector(nil, nil, nil, nil, 0, 0, 0)
	selector.UpdateExcludeRecentDegradeAccounts(true)
	selector.degradeExclusion.Store(&degradeExclusionSnapshot{
		accounts:      map[uint64]struct{}{11: {}},
		disabledNodes: map[uint64]struct{}{7: {}},
	})

	if !selector.shouldSkipDegradedCandidate(account.ProviderBuild, 11, 0) {
		t.Fatal("expected degraded build account to be skipped")
	}
	if selector.shouldSkipDegradedCandidate(account.ProviderWeb, 11, 0) {
		t.Fatal("web accounts are not classified as degrade hits")
	}
	if !selector.shouldSkipDegradedCandidate(account.ProviderBuild, 99, 7) {
		t.Fatal("expected account on disabled node to be skipped")
	}
	if selector.shouldSkipDegradedCandidate(account.ProviderBuild, 99, 8) {
		t.Fatal("healthy node account should remain eligible")
	}

	selector.UpdateExcludeRecentDegradeAccounts(false)
	if selector.shouldSkipDegradedCandidate(account.ProviderBuild, 11, 7) {
		t.Fatal("disabled filter must fail open")
	}
}

func TestRefreshDegradeExclusionReplacesSnapshot(t *testing.T) {
	selector := NewSelector(nil, nil, nil, nil, 0, 0, 0)
	selector.UpdateExcludeRecentDegradeAccounts(true)
	selector.SetDegradeExclusionSource(
		func(context.Context, repository.DegradeAccountIDQuery) ([]uint64, error) {
			return []uint64{3, 5}, nil
		},
		func(context.Context) ([]uint64, error) {
			return []uint64{9}, nil
		},
	)
	if err := selector.RefreshDegradeExclusion(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !selector.shouldSkipDegradedCandidate(account.ProviderBuild, 3, 0) {
		t.Fatal("refreshed degrade account missing")
	}
	if !selector.shouldSkipDegradedCandidate(account.ProviderConsole, 1, 9) {
		t.Fatal("refreshed disabled node missing")
	}
}
