package gateway

import (
	"context"
	"testing"

	"github.com/chenyme/grok2api/backend/internal/domain/account"
)

func TestIsResidentialEgressName(t *testing.T) {
	cases := map[string]bool{
		"住宅Build":                   true,
		"住宅Web":                     true,
		"mihomo-c:48010:住宅 Console": true,
		"Residential US":            true,
		"us-residential-01":         true,
		"mihomo:48010:香港 aws":       false,
		"":                          false,
		"resi-reset":                false,
	}
	for name, want := range cases {
		if got := IsResidentialEgressName(name); got != want {
			t.Fatalf("IsResidentialEgressName(%q) = %t, want %t", name, got, want)
		}
	}
}

func TestPreferResidentialCandidateRanksResidentialFirst(t *testing.T) {
	values := []account.RoutingCandidate{
		{Credential: account.Credential{ID: 1, Priority: 100, EgressNodeID: 28}},
		{Credential: account.Credential{ID: 2, Priority: 1, EgressNodeID: 146}},
	}
	scores := []candidateScore{
		{index: 0, preferResidential: false, preferFreeBuild: false},
		{index: 1, preferResidential: true, preferFreeBuild: false},
	}
	if !candidateScoreBetter(values, scores[1], scores[0]) {
		t.Fatal("residential account should rank above datacenter account")
	}
	if candidateScoreBetter(values, scores[0], scores[1]) {
		t.Fatal("datacenter account should not rank above residential account")
	}
}

func TestRefreshResidentialNodesReplacesSnapshot(t *testing.T) {
	selector := NewSelector(nil, nil, nil, nil, 0, 0, 0)
	selector.UpdatePreferResidentialEgress(true)
	selector.SetResidentialNodeSource(func(context.Context) ([]uint64, error) {
		return []uint64{146, 147}, nil
	})
	if err := selector.RefreshResidentialNodes(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !selector.isResidentialCandidate(account.Credential{ID: 1, EgressNodeID: 146}) {
		t.Fatal("expected residential node 146")
	}
	if selector.isResidentialCandidate(account.Credential{ID: 2, EgressNodeID: 28}) {
		t.Fatal("datacenter node should not be marked residential")
	}
}
