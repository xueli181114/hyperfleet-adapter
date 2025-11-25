package config_loader

// AdapterConfig represents the complete adapter configuration structure
type AdapterConfig struct {
	APIVersion string            `yaml:"apiVersion"`
	Kind       string            `yaml:"kind"`
	Metadata   Metadata          `yaml:"metadata"`
	Spec       AdapterConfigSpec `yaml:"spec"`
}

// Metadata contains the adapter metadata
type Metadata struct {
	Name      string            `yaml:"name"`
	Namespace string            `yaml:"namespace"`
	Labels    map[string]string `yaml:"labels,omitempty"`
}

// AdapterConfigSpec contains the adapter specification
type AdapterConfigSpec struct {
	Adapter       AdapterInfo       `yaml:"adapter"`
	HyperfleetAPI HyperfleetAPIConfig `yaml:"hyperfleetApi"`
	Kubernetes    KubernetesConfig  `yaml:"kubernetes"`
	Params        []Parameter       `yaml:"params,omitempty"`
	Preconditions []Precondition    `yaml:"preconditions,omitempty"`
	Resources     []Resource        `yaml:"resources,omitempty"`
	Post          *PostConfig       `yaml:"post,omitempty"`
}

// AdapterInfo contains basic adapter information
type AdapterInfo struct {
	Version string `yaml:"version"`
}

// HyperfleetAPIConfig contains HyperFleet API configuration
type HyperfleetAPIConfig struct {
	Timeout        string `yaml:"timeout"`
	RetryAttempts  int    `yaml:"retryAttempts"`
	RetryBackoff   string `yaml:"retryBackoff"`
}

// KubernetesConfig contains Kubernetes configuration
type KubernetesConfig struct {
	APIVersion string `yaml:"apiVersion"`
}

// Parameter represents a parameter extraction configuration
type Parameter struct {
	Name        string      `yaml:"name"`
	Source      string      `yaml:"source,omitempty"`
	Type        string      `yaml:"type,omitempty"`
	Description string      `yaml:"description,omitempty"`
	Required    bool        `yaml:"required,omitempty"`
	Default     interface{} `yaml:"default,omitempty"`
	// Build contains a structure that will be evaluated and converted to JSON at runtime.
	// The structure is kept as raw interface{} to allow flexible schema definitions.
	Build interface{} `yaml:"build,omitempty"`
	// BuildRef references an external YAML file containing the build definition
	BuildRef string `yaml:"buildRef,omitempty"`
	// BuildRefContent holds the loaded content from BuildRef file (populated by loader)
	BuildRefContent map[string]interface{} `yaml:"-"`
	// For fetching external resources
	FetchExternalResource *FetchExternalResource `yaml:"fetchExternalResource,omitempty"`
}

// FetchExternalResource represents external resource fetching configuration
type FetchExternalResource struct {
	Kind       string              `yaml:"kind"`
	APIVersion string              `yaml:"apiVersion,omitempty"`
	Discovery  DiscoveryConfig     `yaml:"discovery"`
	Required   bool                `yaml:"required,omitempty"`
}

// Precondition represents a precondition check
type Precondition struct {
	Name              string          `yaml:"name"`
	APICall           *APICall        `yaml:"apiCall,omitempty"`
	StoreResponseAs   string          `yaml:"storeResponseAs,omitempty"`
	Extract           []ExtractField  `yaml:"extract,omitempty"`
	Conditions        []Condition     `yaml:"conditions,omitempty"`
	Expression        string          `yaml:"expression,omitempty"`
}

// APICall represents an API call configuration
type APICall struct {
	Method        string   `yaml:"method"`
	URL           string   `yaml:"url"`
	Timeout       string   `yaml:"timeout,omitempty"`
	RetryAttempts int      `yaml:"retryAttempts,omitempty"`
	RetryBackoff  string   `yaml:"retryBackoff,omitempty"`
	Headers       []Header `yaml:"headers,omitempty"`
	Body          string   `yaml:"body,omitempty"`
}

// Header represents an HTTP header
type Header struct {
	Name  string `yaml:"name"`
	Value string `yaml:"value"`
}

// ExtractField represents a field extraction configuration
type ExtractField struct {
	As    string `yaml:"as"`
	Field string `yaml:"field"`
}

// Condition represents a structured condition
type Condition struct {
	Field    string      `yaml:"field"`
	Operator string      `yaml:"operator"`
	Value    interface{} `yaml:"-"` // Populated by UnmarshalYAML from "value" or "values"
}

// conditionRaw is used for custom unmarshaling to support both "value" and "values" keys
type conditionRaw struct {
	Field    string      `yaml:"field"`
	Operator string      `yaml:"operator"`
	Value    interface{} `yaml:"value"`
	Values   interface{} `yaml:"values"` // Alias for Value
}

// UnmarshalYAML implements custom unmarshaling to support both "value" and "values" keys
func (c *Condition) UnmarshalYAML(unmarshal func(interface{}) error) error {
	var raw conditionRaw
	if err := unmarshal(&raw); err != nil {
		return err
	}

	c.Field = raw.Field
	c.Operator = raw.Operator

	// "values" takes precedence if both are specified, otherwise use "value"
	if raw.Values != nil {
		c.Value = raw.Values
	} else {
		c.Value = raw.Value
	}

	return nil
}

// Resource represents a Kubernetes resource configuration
type Resource struct {
	Name             string               `yaml:"name"`
	Manifest         interface{}          `yaml:"manifest,omitempty"`
	RecreateOnChange bool                 `yaml:"recreateOnChange,omitempty"`
	Discovery        *DiscoveryConfig     `yaml:"discovery,omitempty"`
	// ManifestItems holds loaded content when manifest.ref is an array (populated by loader)
	ManifestItems []map[string]interface{} `yaml:"-"`
}

// DiscoveryConfig represents resource discovery configuration
type DiscoveryConfig struct {
	Namespace   string              `yaml:"namespace,omitempty"`
	ByName      string              `yaml:"byName,omitempty"`
	BySelectors *SelectorConfig     `yaml:"bySelectors,omitempty"`
}

// SelectorConfig represents label selector configuration
type SelectorConfig struct {
	LabelSelector map[string]string `yaml:"labelSelector,omitempty"`
}

// PostConfig represents post-processing configuration
type PostConfig struct {
	Params      []Parameter  `yaml:"params,omitempty"`
	PostActions []PostAction `yaml:"postActions,omitempty"`
}

// PostAction represents a post-processing action
type PostAction struct {
	Name    string    `yaml:"name"`
	APICall *APICall  `yaml:"apiCall,omitempty"`
	When    *WhenExpr `yaml:"when,omitempty"`
}

// WhenExpr represents a conditional expression
type WhenExpr struct {
	Expression  string      `yaml:"expression,omitempty"`
	Conditions  []Condition `yaml:"conditions,omitempty"`
	Description string      `yaml:"description,omitempty"`
}

// ManifestRef represents a manifest reference
type ManifestRef struct {
	Ref string `yaml:"ref"`
}
