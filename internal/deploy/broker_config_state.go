package deploy

import (
	"context"
	"path/filepath"
	"time"

	"github.com/fgjcarlos/mcm/internal/mosquitto"
	"github.com/fgjcarlos/mcm/internal/mosquitto/conf"
)

type BrokerConfigActivation struct {
	DeploymentID int64     `json:"deployment_id"`
	RevisionID   string    `json:"revision_id"`
	Hash         string    `json:"hash"`
	Body         string    `json:"body"`
	VerifiedAt   time.Time `json:"verified_at"`
}

func (s *Service) brokerConfigState(ctx context.Context, result *GetBrokerConfigResult) error {
	_, restart := s.applier.(mosquitto.BrokerRestarter)
	result.BrokerVersion = "unknown" // The catalog must never masquerade as detection.
	result.CatalogVersion = s.confCatalog.VersionName()
	result.Capabilities = map[string]bool{"read": true, "preview": true, "reload": s.applier != nil, "restart": restart, "verify_mqtt": s.verifier != nil, "write": s.applier != nil}
	_, snapshot, err := conf.NewIncludeResolver(filepath.Dir(s.confPath)).Resolve(result.Parsed)
	if err != nil {
		return err
	}
	result.IncludeSnapshot = snapshot
	deployments, err := s.deployStore.ListDeployments(ctx, 100, 0)
	if err != nil {
		return err
	}
	for _, d := range deployments {
		if d.Kind != "broker_config" || d.Status != "active_verified" {
			continue
		}
		result.LastVerified = &BrokerConfigActivation{DeploymentID: d.ID, RevisionID: d.RevisionID, Hash: contentHash(d.ConfRendered), Body: d.ConfRendered, VerifiedAt: d.UpdatedAt}
		archived, err := conf.ParseString(d.ConfRendered, s.confPath)
		if err != nil || hasInlineCredentials(archived) {
			result.LastVerified.Body = "[Archived body withheld: inline credentials or unsupported syntax]"
		}
		result.ExternalDrift = result.LastVerified.Hash != result.Hash
		if !result.ExternalDrift && d.RevisionID != "" {
			rev, err := s.brokerCfgStore.GetBrokerConfigRevision(ctx, d.RevisionID)
			if err != nil {
				return err
			}
			result.ExternalDrift = s.checkBrokerConfigIncludes(result.Parsed, rev.IncludeSnapshot) != nil
		}
		break
	}
	return nil
}
