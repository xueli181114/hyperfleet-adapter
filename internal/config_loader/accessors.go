package config_loader

import (
	"fmt"
)

// -----------------------------------------------------------------------------
// Built-in Variables
// -----------------------------------------------------------------------------

// builtinVariables is the list of built-in variables always available in templates/CEL
var builtinVariables = []string{
	"adapter", "config", "now", "date",
}

// BuiltinVariables returns the list of built-in variables always available in templates/CEL
func BuiltinVariables() []string {
	return builtinVariables
}

// -----------------------------------------------------------------------------
// Config Accessors (Unified Configuration)
// -----------------------------------------------------------------------------

// GetDefinedVariables returns all variables defined in the config that can be used
// in templates and CEL expressions. This includes:
// - Built-in variables (adapter, now, date)
// - Parameters from params
// - Captured variables from preconditions
// - Post payloads
// - Resource aliases (resources.<name>)
func (c *Config) GetDefinedVariables() map[string]bool {
	vars := make(map[string]bool)

	if c == nil {
		return vars
	}

	// Built-in variables
	for _, b := range BuiltinVariables() {
		vars[b] = true
	}

	// Parameters from params
	for _, p := range c.Params {
		if p.Name != "" {
			vars[p.Name] = true
		}
	}

	// Variables from precondition captures
	for _, precond := range c.Preconditions {
		for _, capture := range precond.Capture {
			if capture.Name != "" {
				vars[capture.Name] = true
			}
		}
	}

	// Post payloads
	if c.Post != nil {
		for _, p := range c.Post.Payloads {
			if p.Name != "" {
				vars[p.Name] = true
			}
		}
	}

	// Resource aliases
	for _, r := range c.Resources {
		if r.Name != "" {
			vars[FieldResources+"."+r.Name] = true
		}
	}

	return vars
}

// GetParamByName returns a parameter by name from params, or nil if not found
func (c *Config) GetParamByName(name string) *Parameter {
	if c == nil {
		return nil
	}
	for i := range c.Params {
		if c.Params[i].Name == name {
			return &c.Params[i]
		}
	}
	return nil
}

// GetRequiredParams returns all parameters marked as required from params
func (c *Config) GetRequiredParams() []Parameter {
	if c == nil {
		return nil
	}
	var required []Parameter
	for _, p := range c.Params {
		if p.Required {
			required = append(required, p)
		}
	}
	return required
}

// GetResourceByName returns a resource by name, or nil if not found
func (c *Config) GetResourceByName(name string) *Resource {
	if c == nil {
		return nil
	}
	for i := range c.Resources {
		if c.Resources[i].Name == name {
			return &c.Resources[i]
		}
	}
	return nil
}

// GetPreconditionByName returns a precondition by name, or nil if not found
func (c *Config) GetPreconditionByName(name string) *Precondition {
	if c == nil {
		return nil
	}
	for i := range c.Preconditions {
		if c.Preconditions[i].Name == name {
			return &c.Preconditions[i]
		}
	}
	return nil
}

// GetPostActionByName returns a post action by name, or nil if not found
func (c *Config) GetPostActionByName(name string) *PostAction {
	if c == nil || c.Post == nil {
		return nil
	}
	for i := range c.Post.PostActions {
		if c.Post.PostActions[i].Name == name {
			return &c.Post.PostActions[i]
		}
	}
	return nil
}

// ParamNames returns all parameter names in order
func (c *Config) ParamNames() []string {
	if c == nil {
		return nil
	}
	names := make([]string, len(c.Params))
	for i, p := range c.Params {
		names[i] = p.Name
	}
	return names
}

// ResourceNames returns all resource names in order
func (c *Config) ResourceNames() []string {
	if c == nil {
		return nil
	}
	names := make([]string, len(c.Resources))
	for i, r := range c.Resources {
		names[i] = r.Name
	}
	return names
}

// -----------------------------------------------------------------------------
// Resource Accessors
// -----------------------------------------------------------------------------

// GetTransportClient returns the transport client type for this resource.
// Defaults to "kubernetes" if no transport config is set.
func (r *Resource) GetTransportClient() string {
	if r == nil || r.Transport == nil || r.Transport.Client == "" {
		return TransportClientKubernetes
	}
	return r.Transport.Client
}

// IsMaestroTransport returns true if this resource uses the maestro transport client
func (r *Resource) IsMaestroTransport() bool {
	return r.GetTransportClient() == TransportClientMaestro
}

// HasManifestRef returns true if the manifest uses a ref (single file reference)
func (r *Resource) HasManifestRef() bool {
	if r == nil || r.Manifest == nil {
		return false
	}
	manifest := normalizeToStringKeyMap(r.Manifest)
	if manifest == nil {
		return false
	}
	_, hasRef := manifest["ref"]
	return hasRef
}

// GetManifestRef returns the ref path if set, empty string otherwise
func (r *Resource) GetManifestRef() string {
	if r == nil || r.Manifest == nil {
		return ""
	}
	manifest := normalizeToStringKeyMap(r.Manifest)
	if manifest == nil {
		return ""
	}

	if ref, ok := manifest["ref"].(string); ok {
		return ref
	}

	return ""
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
