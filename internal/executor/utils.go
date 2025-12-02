package executor

import (
	"fmt"
	"strings"

	"github.com/openshift-hyperfleet/hyperfleet-adapter/internal/config_loader"
	"github.com/openshift-hyperfleet/hyperfleet-adapter/internal/criteria"
	"github.com/openshift-hyperfleet/hyperfleet-adapter/pkg/logger"
)

// ToConditionDefs converts config_loader.Condition slice to criteria.ConditionDef slice.
// This centralizes the conversion logic that was previously repeated in multiple places.
func ToConditionDefs(conditions []config_loader.Condition) []criteria.ConditionDef {
	defs := make([]criteria.ConditionDef, len(conditions))
	for i, cond := range conditions {
		defs[i] = criteria.ConditionDef{
			Field:    cond.Field,
			Operator: criteria.Operator(cond.Operator),
			Value:    cond.Value,
		}
	}
	return defs
}

// ExecuteLogAction executes a log action with the given context
// The message is rendered as a Go template with access to all params
// This is a shared utility function used by both PreconditionExecutor and PostActionExecutor
func ExecuteLogAction(logAction *config_loader.LogAction, execCtx *ExecutionContext, log logger.Logger) error {
	if logAction == nil || logAction.Message == "" {
		return nil
	}

	// Render the message template
	message, err := renderTemplate(logAction.Message, execCtx.Params)
	if err != nil {
		return fmt.Errorf("failed to render log message: %w", err)
	}

	// Log at the specified level (default: info)
	level := strings.ToLower(logAction.Level)
	if level == "" {
		level = "info"
	}

	switch level {
	case "debug":
		log.V(2).Infof("[config] %s", message)
	case "info":
		log.Infof("[config] %s", message)
	case "warning", "warn":
		log.Warningf("[config] %s", message)
	case "error":
		log.Errorf("[config] %s", message)
	default:
		log.Infof("[config] %s", message)
	}

	return nil
}

