package account

import (
	"context"
	"time"

	accountdomain "github.com/chenyme/grok2api/backend/internal/domain/account"
	"github.com/chenyme/grok2api/backend/internal/repository"
)

// AutoDisableBuildBotConfig 是自动停用/启用风控 Build 账号的策略。
type AutoDisableBuildBotConfig struct {
	Enabled  bool
	Interval time.Duration
}

const (
	autoDisableBuildBotBatchSize   = 500
	autoDisableBuildBotMaxScans    = 200
	autoDisableBuildBotLockKey     = "account-auto-disable-bot"
	autoDisableBuildBotLockTTL     = 5 * time.Minute
	autoDisableBuildBotRunTimeout  = 4 * time.Minute
)

// UpdateAutoDisableBuildBotConfig 热更新自动停用风控 Build 账号策略。
func (s *Service) UpdateAutoDisableBuildBotConfig(value AutoDisableBuildBotConfig) {
	value = normalizeAutoDisableBuildBotConfig(value)
	s.autoCleanMu.Lock()
	// Reuse autoCleanMu for simplicity; these configs are logically separate.
	prev := s.autoDisableBuildBot
	if prev.Enabled == value.Enabled && prev.Interval == value.Interval {
		s.autoCleanMu.Unlock()
		return
	}
	s.autoDisableBuildBot = value
	s.autoDisableBuildBotRevision++
	s.autoCleanMu.Unlock()
	select {
	case s.autoCleanWake <- struct{}{}:
	default:
	}
}

func normalizeAutoDisableBuildBotConfig(value AutoDisableBuildBotConfig) AutoDisableBuildBotConfig {
	if value.Interval < time.Minute {
		value.Interval = time.Minute
	}
	if value.Interval > time.Hour {
		value.Interval = time.Hour
	}
	return value
}

func (s *Service) autoDisableBuildBotSnapshot() (AutoDisableBuildBotConfig, uint64) {
	s.autoCleanMu.RLock()
	defer s.autoCleanMu.RUnlock()
	return s.autoDisableBuildBot, s.autoDisableBuildBotRevision
}

func (s *Service) autoDisableBuildBotInterval() time.Duration {
	cfg, _ := s.autoDisableBuildBotSnapshot()
	if !cfg.Enabled {
		return time.Hour
	}
	return normalizeAutoDisableBuildBotConfig(cfg).Interval
}

// RunAutoDisableBuildBot 定时检查 Grok Build 账号风控状态：
// - 被风控的已启用账号 → 停用
// - 已解封的已停用账号 → 启用
func (s *Service) RunAutoDisableBuildBot(ctx context.Context) {
	select {
	case <-s.autoCleanWake:
	default:
	}
	cfg, scheduledRevision := s.autoDisableBuildBotSnapshot()
	timer := time.NewTimer(s.autoDisableBuildBotInterval())
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-s.autoCleanWake:
			cfg, scheduledRevision = s.autoDisableBuildBotSnapshot()
			resetCredentialRefreshTimer(timer, s.autoDisableBuildBotInterval())
		case <-timer.C:
			current, revision := s.autoDisableBuildBotSnapshot()
			if revision == scheduledRevision && current.Enabled {
				if err := s.runAutoDisableBuildBot(ctx, current); err != nil && ctx.Err() == nil {
					s.logger.Warn("auto_disable_bot_failed", "error", err)
				}
			}
			cfg, scheduledRevision = s.autoDisableBuildBotSnapshot()
			resetCredentialRefreshTimer(timer, s.autoDisableBuildBotInterval())
		}
	}
}

func (s *Service) runAutoDisableBuildBot(ctx context.Context, cfg AutoDisableBuildBotConfig) error {
	runCtx, cancel := context.WithTimeout(ctx, autoDisableBuildBotRunTimeout)
	defer cancel()
	if s.refreshLock != nil {
		release, acquired, err := s.refreshLock.Acquire(runCtx, autoDisableBuildBotLockKey, autoDisableBuildBotLockTTL)
		if err != nil {
			return err
		}
		if !acquired {
			s.logger.Debug("auto_disable_bot_skipped", "reason", "lock_contended")
			return nil
		}
		if release != nil {
			defer release()
		}
	}

	var afterID uint64
	totalDisabled := 0
	totalEnabled := 0
	totalScanned := 0
	scanBatches := 0
	exhausted := false
	for scanBatches < autoDisableBuildBotMaxScans {
		accounts, _, err := s.accounts.ListProviderAccountBatch(runCtx, accountdomain.ProviderBuild, afterID, autoDisableBuildBotBatchSize)
		if err != nil {
			if totalScanned > 0 || totalDisabled > 0 || totalEnabled > 0 {
				s.logger.Warn("auto_disable_bot_partial", "scanned", totalScanned, "disabled", totalDisabled, "enabled", totalEnabled, "error", err)
			}
			return err
		}
		if len(accounts) == 0 {
			exhausted = true
			break
		}
		scanBatches++
		totalScanned += len(accounts)
		afterID = accounts[len(accounts)-1].ID
		batchComplete := len(accounts) < autoDisableBuildBotBatchSize

		toDisable := make([]uint64, 0)
		toEnable := make([]uint64, 0)
		for _, acct := range accounts {
			metadata := s.credentialMetadata(acct)
			if metadata.BuildBotFlagged && acct.Enabled {
				toDisable = append(toDisable, acct.ID)
			} else if !metadata.BuildBotFlagged && !acct.Enabled {
				toEnable = append(toEnable, acct.ID)
			}
		}
		accounts = nil

		if len(toDisable) > 0 {
			disabled := false
			updated, err := s.accounts.UpdateMany(runCtx, toDisable, repository.AccountUpdates{Enabled: &disabled})
			if err != nil {
				s.logger.Warn("auto_disable_bot_disable_failed", "accounts", toDisable, "error", err)
			} else {
				totalDisabled += int(updated)
				if s.sticky != nil {
					for _, id := range toDisable {
						_ = s.sticky.DeleteByAccount(runCtx, id)
					}
				}
				s.invalidateBuildBotFlagCache()
			}
		}

		if len(toEnable) > 0 {
			enabled := true
			updated, err := s.accounts.UpdateMany(runCtx, toEnable, repository.AccountUpdates{Enabled: &enabled})
			if err != nil {
				s.logger.Warn("auto_disable_bot_enable_failed", "accounts", toEnable, "error", err)
			} else {
				totalEnabled += int(updated)
				s.invalidateBuildBotFlagCache()
			}
		}

		if batchComplete {
			exhausted = true
			break
		}
	}

	limitReached := !exhausted && scanBatches == autoDisableBuildBotMaxScans
	if totalDisabled > 0 || totalEnabled > 0 {
		s.logger.Info("auto_disable_bot", "scanned", totalScanned, "disabled", totalDisabled, "enabled", totalEnabled, "scan_batches", scanBatches, "limit_reached", limitReached)
	}
	return nil
}
