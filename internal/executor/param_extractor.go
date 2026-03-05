package executor

import (
	"fmt"
	"os"
	"strings"

	"github.com/go-viper/mapstructure/v2"
	"github.com/openshift-hyperfleet/hyperfleet-adapter/internal/config_loader"
	"github.com/openshift-hyperfleet/hyperfleet-adapter/pkg/utils"
)

// extractConfigParams extracts all configured parameters and populates execCtx.Params
// This is a pure function that directly modifies execCtx for simplicity
func extractConfigParams(config *config_loader.Config, execCtx *ExecutionContext, configMap map[string]interface{}) error {
	for _, param := range config.Params {
		value, err := extractParam(param, execCtx.EventData, configMap)
		if err != nil {
			if param.Required {
				return NewExecutorError(PhaseParamExtraction, param.Name,
					fmt.Sprintf("failed to extract required parameter '%s' from source '%s'", param.Name, param.Source), err)
			}
			// Use default for non-required params if extraction fails
			if param.Default != nil {
				execCtx.Params[param.Name] = param.Default
			}
			continue
		}

		// Apply default if value is nil or (for strings) empty
		isEmpty := value == nil
		if s, ok := value.(string); ok && s == "" {
			isEmpty = true
		}
		if isEmpty && param.Default != nil {
			value = param.Default
		}

		// Apply type conversion if specified
		if value != nil && param.Type != "" {
			converted, convErr := convertParamType(value, param.Type)
			if convErr != nil {
				if param.Required {
					return NewExecutorError(PhaseParamExtraction, param.Name,
						fmt.Sprintf("failed to convert parameter '%s' to type '%s'", param.Name, param.Type), convErr)
				}
				// Use default for non-required params if conversion fails
				if param.Default != nil {
					execCtx.Params[param.Name] = param.Default
				}
				continue
			}
			value = converted
		}

		if value != nil {
			execCtx.Params[param.Name] = value
		}
	}

	return nil
}

// extractParam extracts a single parameter based on its source
func extractParam(param config_loader.Parameter, eventData map[string]interface{}, configMap map[string]interface{}) (interface{}, error) {
	source := param.Source

	// Handle different source types
	switch {
	case strings.HasPrefix(source, "env."):
		return extractFromEnv(source[4:])
	case strings.HasPrefix(source, "event."):
		return utils.GetNestedValue(eventData, source[6:])
	case strings.HasPrefix(source, "config."):
		return utils.GetNestedValue(configMap, source[7:])
	case source == "":
		// No source specified, return default or nil
		return param.Default, nil
	default:
		// Try to extract from event data directly
		return utils.GetNestedValue(eventData, source)
	}
}

// configToMap converts a Config to map[string]interface{} using the yaml struct tags for key names.
// mapstructure reads the "yaml" tag for key names but ignores the omitempty option, so zero-valued
// fields like debug_config=false are preserved in the resulting map.
func configToMap(cfg *config_loader.Config) (map[string]interface{}, error) {
	var m map[string]interface{}
	decoder, err := mapstructure.NewDecoder(&mapstructure.DecoderConfig{
		TagName: "yaml",
		Result:  &m,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create config decoder: %w", err)
	}
	if err := decoder.Decode(cfg); err != nil {
		return nil, fmt.Errorf("failed to convert config to map: %w", err)
	}
	return m, nil
}

// extractFromEnv extracts a value from environment variables
func extractFromEnv(envVar string) (interface{}, error) {
	value, exists := os.LookupEnv(envVar)
	if !exists {
		return nil, fmt.Errorf("environment variable %s not set", envVar)
	}
	return value, nil
}

// addAdapterParams adds adapter info and the full config map to execCtx.Params
func addAdapterParams(config *config_loader.Config, execCtx *ExecutionContext, configMap map[string]interface{}) {
	execCtx.Params["adapter"] = map[string]interface{}{
		"name":    config.Adapter.Name,
		"version": config.Adapter.Version,
	}
	execCtx.Params["config"] = configMap
}

// convertParamType converts a value to the specified type.
// Supported types: string, int, int64, float, float64, bool
func convertParamType(value interface{}, targetType string) (interface{}, error) {
	return utils.ConvertToType(value, targetType)
}

//nolint:unparam // error kept for API consistency with convertToInt64
func convertToString(value interface{}) (string, error) {
	return utils.ConvertToString(value)
}

func convertToInt64(value interface{}) (int64, error) {
	return utils.ConvertToInt64(value)
}

func convertToFloat64(value interface{}) (float64, error) {
	return utils.ConvertToFloat64(value)
}

func convertToBool(value interface{}) (bool, error) {
	return utils.ConvertToBool(value)
}
