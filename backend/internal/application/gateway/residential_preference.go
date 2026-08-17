package gateway

import (
	"context"

	"github.com/chenyme/grok2api/backend/internal/domain/account"
	"github.com/chenyme/grok2api/backend/internal/domain/egress"
)

type residentialSnapshot struct {
	nodes map[uint64]struct{}
}

type residentialNodeSource struct {
	listIDs func(context.Context) ([]uint64, error)
}

func IsResidentialEgressName(name string) bool {
	return egress.IsResidentialName(name)
}

func (s *Selector) SetResidentialNodeSource(listIDs func(context.Context) ([]uint64, error)) {
	s.residentialSource.Store(&residentialNodeSource{listIDs: listIDs})
}

func (s *Selector) UpdatePreferResidentialEgress(enabled bool) {
	s.configMu.Lock()
	s.preferResidential = enabled
	s.configMu.Unlock()
}

func (s *Selector) preferResidentialEnabled() bool {
	s.configMu.RLock()
	defer s.configMu.RUnlock()
	return s.preferResidential
}

func (s *Selector) residentialNodeSet() map[uint64]struct{} {
	snap := s.residentialNodes.Load()
	if snap == nil {
		return nil
	}
	return snap.nodes
}

func (s *Selector) isResidentialCandidate(value account.Credential) bool {
	if value.EgressNodeID == 0 {
		return false
	}
	nodes := s.residentialNodeSet()
	if nodes == nil {
		return false
	}
	_, ok := nodes[value.EgressNodeID]
	return ok
}

func preferResidentialCandidate(enabled bool, nodes map[uint64]struct{}, nodeID uint64) bool {
	if !enabled || nodeID == 0 || nodes == nil {
		return false
	}
	_, ok := nodes[nodeID]
	return ok
}

func (s *Selector) RefreshResidentialNodes(ctx context.Context) error {
	source, _ := s.residentialSource.Load().(*residentialNodeSource)
	if source == nil || source.listIDs == nil {
		return nil
	}
	ids, err := source.listIDs(ctx)
	if err != nil {
		return err
	}
	nodes := make(map[uint64]struct{}, len(ids))
	for _, id := range ids {
		if id != 0 {
			nodes[id] = struct{}{}
		}
	}
	s.residentialNodes.Store(&residentialSnapshot{nodes: nodes})
	if s.logger != nil {
		s.logger.Info("residential_preference_refreshed", "nodes", len(nodes))
	}
	return nil
}
