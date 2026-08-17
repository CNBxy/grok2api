package gateway

import (
	"context"
	"time"

	"github.com/chenyme/grok2api/backend/internal/domain/account"
	auditdomain "github.com/chenyme/grok2api/backend/internal/domain/audit"
	"github.com/chenyme/grok2api/backend/internal/repository"
)

const defaultDegradeExclusionWindow = 7 * 24 * time.Hour

type degradeExclusionSnapshot struct {
	accounts      map[uint64]struct{}
	disabledNodes map[uint64]struct{}
}

type degradeExclusionThresholds struct {
	SoftTPS         float64
	HardTPS         float64
	MinGenerationMS int64
	MinOutputTokens int64
	FailClosed      bool
	Window          time.Duration
}

type degradeExclusionSource struct {
	listAccountIDs    func(context.Context, repository.DegradeAccountIDQuery) ([]uint64, error)
	listDisabledNodes func(context.Context) ([]uint64, error)
}

func (s *Selector) SetDegradeExclusionSource(
	listAccountIDs func(context.Context, repository.DegradeAccountIDQuery) ([]uint64, error),
	listDisabledNodes func(context.Context) ([]uint64, error),
) {
	s.degradeSource.Store(&degradeExclusionSource{listAccountIDs: listAccountIDs, listDisabledNodes: listDisabledNodes})
}

func (s *Selector) UpdateExcludeRecentDegradeAccounts(enabled bool) {
	s.configMu.Lock()
	s.excludeRecentDegrade = enabled
	s.configMu.Unlock()
}

func (s *Selector) UpdateDegradeExclusionThresholds(value degradeExclusionThresholds) {
	s.configMu.Lock()
	s.degradeThresholds = normalizeDegradeExclusionThresholds(value)
	s.configMu.Unlock()
}

func (s *Selector) excludeRecentDegradeEnabled() bool {
	s.configMu.RLock()
	defer s.configMu.RUnlock()
	return s.excludeRecentDegrade
}

func (s *Selector) degradeExclusionConfig() degradeExclusionThresholds {
	s.configMu.RLock()
	defer s.configMu.RUnlock()
	return s.degradeThresholds
}

func (s *Selector) shouldSkipDegradedCandidate(provider account.Provider, accountID, nodeID uint64) bool {
	if !s.excludeRecentDegradeEnabled() {
		return false
	}
	snap := s.degradeExclusion.Load()
	if snap == nil {
		return false
	}
	if provider == account.ProviderBuild {
		if _, hit := snap.accounts[accountID]; hit {
			return true
		}
	}
	if nodeID != 0 {
		if _, hit := snap.disabledNodes[nodeID]; hit {
			return true
		}
	}
	return false
}

func (s *Selector) RefreshDegradeExclusion(ctx context.Context) error {
	if !s.excludeRecentDegradeEnabled() {
		s.degradeExclusion.Store(&degradeExclusionSnapshot{})
		return nil
	}
	source, _ := s.degradeSource.Load().(*degradeExclusionSource)
	if source == nil || source.listAccountIDs == nil || source.listDisabledNodes == nil {
		return nil
	}
	thresholds := s.degradeExclusionConfig()
	now := time.Now().UTC()
	accountIDs, err := source.listAccountIDs(ctx, repository.DegradeAccountIDQuery{
		Start: now.Add(-thresholds.Window), End: now,
		SoftTPS: thresholds.SoftTPS, HardTPS: thresholds.HardTPS,
		MinGenerationMS: thresholds.MinGenerationMS, MinOutputTokens: thresholds.MinOutputTokens,
		FailClosed: thresholds.FailClosed,
	})
	if err != nil {
		return err
	}
	nodeIDs, err := source.listDisabledNodes(ctx)
	if err != nil {
		return err
	}
	accounts := make(map[uint64]struct{}, len(accountIDs))
	for _, id := range accountIDs {
		if id != 0 {
			accounts[id] = struct{}{}
		}
	}
	disabledNodes := make(map[uint64]struct{}, len(nodeIDs))
	for _, id := range nodeIDs {
		if id != 0 {
			disabledNodes[id] = struct{}{}
		}
	}
	s.degradeExclusion.Store(&degradeExclusionSnapshot{accounts: accounts, disabledNodes: disabledNodes})
	if s.logger != nil {
		s.logger.Info("degrade_exclusion_refreshed", "accounts", len(accounts), "disabled_nodes", len(disabledNodes), "window", thresholds.Window.String())
	}
	return nil
}

func normalizeDegradeExclusionThresholds(value degradeExclusionThresholds) degradeExclusionThresholds {
	if value.SoftTPS <= 0 {
		value.SoftTPS = auditdomain.DefaultDegradeSoftTPS
	}
	if value.HardTPS <= 0 {
		value.HardTPS = auditdomain.DefaultDegradeHardTPS
	}
	if value.SoftTPS >= value.HardTPS {
		value.SoftTPS = auditdomain.DefaultDegradeSoftTPS
		value.HardTPS = auditdomain.DefaultDegradeHardTPS
	}
	if value.MinGenerationMS <= 0 {
		value.MinGenerationMS = auditdomain.DefaultDegradeMinGenMS
	}
	if value.MinOutputTokens <= 0 {
		value.MinOutputTokens = auditdomain.DefaultDegradeMinOutput
	}
	if value.Window <= 0 {
		value.Window = defaultDegradeExclusionWindow
	}
	return value
}
