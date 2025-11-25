package config_loader

import (
	"fmt"
	"time"
)

// -----------------------------------------------------------------------------
// Built-in Variables
// -----------------------------------------------------------------------------

// builtinVariables is the list of built-in variables always available in templates/CEL
var builtinVariables = []string{
	"metadata", "metadata.name", "metadata.namespace", "metadata.labels",
	"now", "date",
}

// BuiltinVariables returns the list of built-in variables always available in templates/CEL
func BuiltinVariables() []string {
	return builtinVariables
}

// -----------------------------------------------------------------------------
// AdapterConfig Accessors
// -----------------------------------------------------------------------------

// GetDefinedVariables returns all variables defined in the config that can be used
// in templates and CEL expressions. This includes:
// - Built-in variables (metadata, now, date)
// - Parameters from spec.params
// - Extracted variables from preconditions
// - Post params
// - Resource aliases (resources.<name>)
func (c *AdapterConfig) GetDefinedVariables() map[string]bool {
	vars := make(map[string]bool)

	if c == nil {
		return vars
	}

	// Built-in variables
	for _, b := range BuiltinVariables() {
		vars[b] = true
	}

	// Parameters from spec.params
	for _, p := range c.Spec.Params {
		if p.Name != "" {
			vars[p.Name] = true
		}
	}

	// Variables from precondition extracts
	for _, precond := range c.Spec.Preconditions {
		if precond.StoreResponseAs != "" {
			vars[precond.StoreResponseAs] = true
		}
		for _, extract := range precond.Extract {
			if extract.As != "" {
				vars[extract.As] = true
			}
		}
	}

	// Post params
	if c.Spec.Post != nil {
		for _, p := range c.Spec.Post.Params {
			if p.Name != "" {
				vars[p.Name] = true
			}
		}
	}

	// Resource aliases
	for _, r := range c.Spec.Resources {
		if r.Name != "" {
			vars[FieldResources+"."+r.Name] = true
		}
	}

	return vars
}

// GetParamByName returns a parameter by name from spec.params, or nil if not found
func (c *AdapterConfig) GetParamByName(name string) *Parameter {
	if c == nil {
		return nil
	}
	for i := range c.Spec.Params {
		if c.Spec.Params[i].Name == name {
			return &c.Spec.Params[i]
		}
	}
	return nil
}

// GetRequiredParams returns all parameters marked as required from spec.params
func (c *AdapterConfig) GetRequiredParams() []Parameter {
	if c == nil {
		return nil
	}
	var required []Parameter
	for _, p := range c.Spec.Params {
		if p.Required {
			required = append(required, p)
		}
	}
	return required
}

// GetResourceByName returns a resource by name, or nil if not found
func (c *AdapterConfig) GetResourceByName(name string) *Resource {
	if c == nil {
		return nil
	}
	for i := range c.Spec.Resources {
		if c.Spec.Resources[i].Name == name {
			return &c.Spec.Resources[i]
		}
	}
	return nil
}

// GetPreconditionByName returns a precondition by name, or nil if not found
func (c *AdapterConfig) GetPreconditionByName(name string) *Precondition {
	if c == nil {
		return nil
	}
	for i := range c.Spec.Preconditions {
		if c.Spec.Preconditions[i].Name == name {
			return &c.Spec.Preconditions[i]
		}
	}
	return nil
}

// GetPostActionByName returns a post action by name, or nil if not found
func (c *AdapterConfig) GetPostActionByName(name string) *PostAction {
	if c == nil || c.Spec.Post == nil {
		return nil
	}
	for i := range c.Spec.Post.PostActions {
		if c.Spec.Post.PostActions[i].Name == name {
			return &c.Spec.Post.PostActions[i]
		}
	}
	return nil
}

// ParamNames returns all parameter names in order
func (c *AdapterConfig) ParamNames() []string {
	if c == nil {
		return nil
	}
	names := make([]string, len(c.Spec.Params))
	for i, p := range c.Spec.Params {
		names[i] = p.Name
	}
	return names
}

// ResourceNames returns all resource names in order
func (c *AdapterConfig) ResourceNames() []string {
	if c == nil {
		return nil
	}
	names := make([]string, len(c.Spec.Resources))
	for i, r := range c.Spec.Resources {
		names[i] = r.Name
	}
	return names
}

// -----------------------------------------------------------------------------
// HyperfleetAPIConfig Accessors
// -----------------------------------------------------------------------------

// ParseTimeout parses the timeout string to time.Duration
// Returns 0 and nil if timeout is empty (caller should use default)
func (c *HyperfleetAPIConfig) ParseTimeout() (time.Duration, error) {
	if c == nil || c.Timeout == "" {
		return 0, nil
	}
	return time.ParseDuration(c.Timeout)
}

// -----------------------------------------------------------------------------
// Resource Accessors
// -----------------------------------------------------------------------------

// HasManifestRef returns true if the manifest uses a ref (single or array)
func (r *Resource) HasManifestRef() bool {
	if r == nil || r.Manifest == nil {
		return false
	}
	manifest := normalizeToStringKeyMap(r.Manifest)
	if manifest == nil {
		return false
	}
	_, hasRef := manifest["ref"]
	_, hasRefs := manifest["refs"]
	return hasRef || hasRefs
}

// GetManifestRefs returns the ref paths (handles both single ref and refs array)
func (r *Resource) GetManifestRefs() []string {
	if r == nil || r.Manifest == nil {
		return nil
	}
	manifest := normalizeToStringKeyMap(r.Manifest)
	if manifest == nil {
		return nil
	}

	// Single ref
	if ref, ok := manifest["ref"].(string); ok && ref != "" {
		return []string{ref}
	}

	// Array of refs
	if refs, ok := manifest["refs"].([]interface{}); ok {
		result := make([]string, 0, len(refs))
		for _, r := range refs {
			if s, ok := r.(string); ok && s != "" {
				result = append(result, s)
			}
		}
		return result
	}

	return nil
}

// UnmarshalManifest attempts to unmarshal the manifest as a map
// Returns nil, nil if resource is nil or manifest is nil
// Returns error if manifest cannot be converted to map
func (r *Resource) UnmarshalManifest() (map[string]interface{}, error) {
	if r == nil || r.Manifest == nil {
		return nil, nil
	}

	// Try to normalize the manifest to map[string]interface{}
	if m := normalizeToStringKeyMap(r.Manifest); m != nil {
		return m, nil
	}

	// If manifest cannot be normalized, return an error with type info
	return nil, fmt.Errorf("manifest is not a map, got %T", r.Manifest)
}

// -----------------------------------------------------------------------------
// Helper Functions
// -----------------------------------------------------------------------------

// normalizeToStringKeyMap converts various map types to map[string]interface{}.
// This handles both map[string]interface{} (from yaml.v3) and map[interface{}]interface{}
// (from yaml.v2 or other sources) for robustness.
// Returns nil if the input is not a map type.
func normalizeToStringKeyMap(v interface{}) map[string]interface{} {
	switch m := v.(type) {
	case map[string]interface{}:
		return m
	case map[interface{}]interface{}:
		result := make(map[string]interface{}, len(m))
		for k, val := range m {
			if keyStr, ok := k.(string); ok {
				result[keyStr] = val
			} else {
				// Convert non-string keys to string representation
				result[fmt.Sprintf("%v", k)] = val
			}
		}
		return result
	default:
		return nil
	}
}

