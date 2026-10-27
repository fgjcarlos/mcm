package server

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/fgjcarlos/mcm/internal/config"
	"github.com/fgjcarlos/mcm/internal/dynsec"
	"github.com/fgjcarlos/mcm/internal/tlsutil"
)

// buildDynSecTransportFactory opens the control channel of the dynamic
// security plugin.
//
// The channel is the only way to reach the security database of the
// plugin. MCM connects with the broker account it already uses, which the
// adoption turns into the management account of the plugin.
func buildDynSecTransportFactory(cfg config.MosquittoConfig) func(context.Context) (dynsec.Transport, func(), error) {
	return func(ctx context.Context) (dynsec.Transport, func(), error) {
		address := fmt.Sprintf("%s:%d", cfg.Host, cfg.Port)
		opts := dynsec.Options{
			Address:  address,
			Username: strings.TrimSpace(cfg.Username),
			Password: cfg.Password,
			ClientID: fmt.Sprintf("mcm-dynsec-%d", time.Now().UnixNano()),
			Timeout:  10 * time.Second,
		}
		if cfg.TLS.Enabled {
			tlsConfig, err := tlsutil.BuildMosquittoTLSConfig(cfg)
			if err != nil {
				return nil, nil, fmt.Errorf("build broker TLS config: %w", err)
			}
			opts.TLS = tlsConfig
		}
		adapter := dynsec.NewAdapter(opts)
		if err := adapter.Connect(ctx); err != nil {
			return nil, nil, err
		}
		return adapter, adapter.Close, nil
	}
}
