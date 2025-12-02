package executor

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/openshift-hyperfleet/hyperfleet-adapter/internal/config_loader"
)

// ParamExtractor extracts parameters from various sources
type ParamExtractor struct {
	config   *config_loader.AdapterConfig
	eventCtx *ExecutionContext
}

// NewParamExtractor creates a new parameter extractor
func NewParamExtractor(config *config_loader.AdapterConfig, eventCtx *ExecutionContext) *ParamExtractor {
	return &ParamExtractor{
		config:   config,
		eventCtx: eventCtx,
	}
}

// ExtractAll extracts all parameters from the configuration
// Returns a map of parameter name to value
func (pe *ParamExtractor) ExtractAll(params []config_loader.Parameter) (map[string]interface{}, error) {
	result := make(map[string]interface{})

	// Parse event data once
	eventData, err := pe.parseEventData()
	if err != nil {
		return nil, NewExecutorError(PhaseParamExtraction, "parse_event", "failed to parse event data", err)
	}

	for _, param := range params {
		value, err := pe.extractParam(param, eventData)
		if err != nil {
			if param.Required {
				return nil, NewExecutorError(PhaseParamExtraction, param.Name,
					fmt.Sprintf("failed to extract required parameter: %s", param.Source), err)
			}
			// Use default for non-required params
			if param.Default != nil {
				result[param.Name] = param.Default
			}
			continue
		}

		// Apply default if value is nil
		if value == nil && param.Default != nil {
			value = param.Default
		}

		result[param.Name] = value
	}

	return result, nil
}

// parseEventData parses the CloudEvent data into a map
func (pe *ParamExtractor) parseEventData() (map[string]interface{}, error) {
	if pe.eventCtx.Event == nil {
		return make(map[string]interface{}), nil
	}

	data := pe.eventCtx.Event.Data()
	if len(data) == 0 {
		return make(map[string]interface{}), nil
	}

	var eventData map[string]interface{}
	if err := json.Unmarshal(data, &eventData); err != nil {
		return nil, fmt.Errorf("failed to parse event data as JSON: %w", err)
	}

	return eventData, nil
}

// extractParam extracts a single parameter based on its source
func (pe *ParamExtractor) extractParam(param config_loader.Parameter, eventData map[string]interface{}) (interface{}, error) {
	source := param.Source

	// Handle different source types
	switch {
	case strings.HasPrefix(source, "env."):
		return pe.extractFromEnv(source[4:])
	case strings.HasPrefix(source, "event."):
		return pe.extractFromEvent(source[6:], eventData)
	case strings.HasPrefix(source, "secret."):
		return pe.extractFromSecret(source[7:])
	case strings.HasPrefix(source, "configmap."):
		return pe.extractFromConfigMap(source[10:])
	case source == "":
		// No source specified, return default or nil
		return param.Default, nil
	default:
		// Try to extract from event data directly
		return pe.extractFromEvent(source, eventData)
	}
}

// extractFromEnv extracts a value from environment variables
func (pe *ParamExtractor) extractFromEnv(envVar string) (interface{}, error) {
	value := os.Getenv(envVar)
	if value == "" {
		return nil, fmt.Errorf("environment variable %s not set", envVar)
	}
	return value, nil
}

// extractFromEvent extracts a value from the CloudEvent data using dot notation
func (pe *ParamExtractor) extractFromEvent(path string, eventData map[string]interface{}) (interface{}, error) {
	parts := strings.Split(path, ".")
	var current interface{} = eventData

	for i, part := range parts {
		switch v := current.(type) {
		case map[string]interface{}:
			val, ok := v[part]
			if !ok {
				return nil, fmt.Errorf("field '%s' not found at path '%s'", part, strings.Join(parts[:i+1], "."))
			}
			current = val
		case map[interface{}]interface{}:
			val, ok := v[part]
			if !ok {
				return nil, fmt.Errorf("field '%s' not found at path '%s'", part, strings.Join(parts[:i+1], "."))
			}
			current = val
		default:
			return nil, fmt.Errorf("cannot access field '%s': parent is not a map (got %T)", part, current)
		}
	}

	return current, nil
}

// extractFromSecret extracts a value from a Kubernetes Secret
// Format: secret.<secret-name>.<key>
func (pe *ParamExtractor) extractFromSecret(path string) (interface{}, error) {
	// TODO: Implement secret extraction using k8s_client
	// For now, return an error indicating this is not yet implemented
	return nil, fmt.Errorf("secret extraction not yet implemented: %s", path)
}

// extractFromConfigMap extracts a value from a Kubernetes ConfigMap
// Format: configmap.<configmap-name>.<key>
func (pe *ParamExtractor) extractFromConfigMap(path string) (interface{}, error) {
	// TODO: Implement configmap extraction using k8s_client
	// For now, return an error indicating this is not yet implemented
	return nil, fmt.Errorf("configmap extraction not yet implemented: %s", path)
}

// AddMetadataParams adds adapter metadata to the params
func (pe *ParamExtractor) AddMetadataParams(params map[string]interface{}) {
	// Add metadata from adapter config
	params["metadata"] = map[string]interface{}{
		"name":      pe.config.Metadata.Name,
		"namespace": pe.config.Metadata.Namespace,
		"labels":    pe.config.Metadata.Labels,
	}

	// Add event metadata if available
	if pe.eventCtx.Event != nil {
		params["eventMetadata"] = map[string]interface{}{
			"id":     pe.eventCtx.Event.ID(),
			"type":   pe.eventCtx.Event.Type(),
			"source": pe.eventCtx.Event.Source(),
			"time":   pe.eventCtx.Event.Time().String(),
		}
	}
}

