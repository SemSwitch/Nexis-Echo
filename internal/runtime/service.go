package runtime

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/SemSwitch/Nexis-Echo/internal/model"
)

// Service is the lifecycle contract implemented by every runtime component.
type Service interface {
	Name() string
	Start(ctx context.Context) error
	Stop(ctx context.Context) error
	Health() model.ServiceHealth
}

// StartAll starts each service in order. If any service fails to start,
// previously started services are stopped in reverse order.
func StartAll(ctx context.Context, logger *slog.Logger, services []Service) error {
	for i, svc := range services {
		logger.Info("starting service", "name", svc.Name())
		if err := svc.Start(ctx); err != nil {
			// Roll back already-started services in reverse.
			for j := i - 1; j >= 0; j-- {
				logger.Info("rolling back service", "name", services[j].Name())
				_ = services[j].Stop(ctx)
			}
			return fmt.Errorf("start %s: %w", svc.Name(), err)
		}
	}
	return nil
}

// StopAll stops each service in reverse order. It logs but does not
// short-circuit on individual stop errors.
func StopAll(ctx context.Context, logger *slog.Logger, services []Service) error {
	var firstErr error
	for i := len(services) - 1; i >= 0; i-- {
		svc := services[i]
		logger.Info("stopping service", "name", svc.Name())
		if err := svc.Stop(ctx); err != nil {
			logger.Error("stop failed", "name", svc.Name(), "error", err)
			if firstErr == nil {
				firstErr = fmt.Errorf("stop %s: %w", svc.Name(), err)
			}
		}
	}
	return firstErr
}
