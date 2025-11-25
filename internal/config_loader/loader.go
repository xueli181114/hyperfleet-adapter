package config_loader

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// -----------------------------------------------------------------------------
// Constants
// -----------------------------------------------------------------------------

// API version constants
const (
	APIVersionV1Alpha1 = "hyperfleet.redhat.com/v1alpha1"
	ExpectedKind       = "AdapterConfig"
)

// Environment variable for config file path
const EnvConfigPath = "ADAPTER_CONFIG_PATH"

// SupportedAPIVersions contains all supported apiVersion values
var SupportedAPIVersions = []string{
	APIVersionV1Alpha1,
}

// ValidHTTPMethods defines allowed HTTP methods for API calls
var ValidHTTPMethods = map[string]struct{}{
	"GET":    {},
	"POST":   {},
	"PUT":    {},
	"PATCH":  {},
	"DELETE": {},
}

// ValidHTTPMethodsList is a pre-built list of valid HTTP methods for error messages
var ValidHTTPMethodsList = []string{"GET", "POST", "PUT", "PATCH", "DELETE"}

// -----------------------------------------------------------------------------
// Loader Options (Functional Options Pattern)
// -----------------------------------------------------------------------------

// LoaderOption configures the loader behavior
type LoaderOption func(*loaderConfig)

type loaderConfig struct {
	adapterVersion         string
	skipSemanticValidation bool
	baseDir                string // Base directory for resolving relative paths (buildRef, manifest.ref)
}

// WithAdapterVersion validates config against expected adapter version
func WithAdapterVersion(version string) LoaderOption {
	return func(c *loaderConfig) {
		c.adapterVersion = version
	}
}

// WithSkipSemanticValidation skips CEL, template, and K8s manifest validation
func WithSkipSemanticValidation() LoaderOption {
	return func(c *loaderConfig) {
		c.skipSemanticValidation = true
	}
}

// WithBaseDir sets the base directory for resolving relative paths (buildRef, manifest.ref)
func WithBaseDir(dir string) LoaderOption {
	return func(c *loaderConfig) {
		c.baseDir = dir
	}
}

// -----------------------------------------------------------------------------
// Public API
// -----------------------------------------------------------------------------

// ConfigPathFromEnv returns the config file path from the ADAPTER_CONFIG_PATH environment variable
func ConfigPathFromEnv() string {
	return os.Getenv(EnvConfigPath)
}

// Load loads an adapter configuration from a YAML file.
// If filePath is empty, it will read from ADAPTER_CONFIG_PATH environment variable.
// The base directory for relative paths (buildRef, manifest.ref) is automatically
// set to the config file's directory.
func Load(filePath string, opts ...LoaderOption) (*AdapterConfig, error) {
	if filePath == "" {
		filePath = ConfigPathFromEnv()
	}
	if filePath == "" {
		return nil, fmt.Errorf("config file path is required (pass as parameter or set %s environment variable)", EnvConfigPath)
	}

	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file %q: %w", filePath, err)
	}

	// Automatically set base directory from config file path
	absPath, err := filepath.Abs(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to get absolute path for %q: %w", filePath, err)
	}
	baseDir := filepath.Dir(absPath)

	// Prepend WithBaseDir option so it can be overridden by user opts
	allOpts := append([]LoaderOption{WithBaseDir(baseDir)}, opts...)
	return Parse(data, allOpts...)
}

// Parse parses adapter configuration from YAML bytes
func Parse(data []byte, opts ...LoaderOption) (*AdapterConfig, error) {
	cfg := &loaderConfig{}
	for _, opt := range opts {
		opt(cfg)
	}

	var config AdapterConfig
	if err := yaml.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("YAML parse error: %w", err)
	}

	if err := runValidationPipeline(&config, cfg); err != nil {
		return nil, err
	}

	return &config, nil
}

// LoadWithVersion is a convenience wrapper for Load with version validation
// Deprecated: Use Load(path, WithAdapterVersion(version)) instead
func LoadWithVersion(filePath string, adapterVersion string) (*AdapterConfig, error) {
	return Load(filePath, WithAdapterVersion(adapterVersion))
}

// ParseWithVersion is a convenience wrapper for Parse with version validation
// Deprecated: Use Parse(data, WithAdapterVersion(version)) instead
func ParseWithVersion(data []byte, adapterVersion string) (*AdapterConfig, error) {
	return Parse(data, WithAdapterVersion(adapterVersion))
}

// -----------------------------------------------------------------------------
// Validation Pipeline
// -----------------------------------------------------------------------------

// validatorFunc is a function that validates a config and returns an error
type validatorFunc func(*AdapterConfig) error

// runValidationPipeline executes all validators in sequence
func runValidationPipeline(config *AdapterConfig, cfg *loaderConfig) error {
	// Core structural validators (always run)
	coreValidators := []validatorFunc{
		validateAPIVersionAndKind,
		validateMetadata,
		validateAdapterSpec,
		validateParams,
		validatePreconditions,
		validateResources,
		validatePostActions,
	}

	for _, v := range coreValidators {
		if err := v(config); err != nil {
			return fmt.Errorf("validation failed: %w", err)
		}
	}

	// Adapter version validation (optional)
	if cfg.adapterVersion != "" {
		if err := ValidateAdapterVersion(config, cfg.adapterVersion); err != nil {
			return fmt.Errorf("adapter version validation failed: %w", err)
		}
	}

	// File reference validation (buildRef, manifest.ref)
	// Only run if baseDir is set (when loaded from file)
	if cfg.baseDir != "" {
		if err := validateFileReferences(config, cfg.baseDir); err != nil {
			return fmt.Errorf("file reference validation failed: %w", err)
		}

		// Load file references (manifest.ref, buildRef) after validation passes
		if err := loadFileReferences(config, cfg.baseDir); err != nil {
			return fmt.Errorf("failed to load file references: %w", err)
		}
	}

	// Semantic validation (optional, can be skipped for performance)
	if !cfg.skipSemanticValidation {
		if err := Validate(config); err != nil {
			return fmt.Errorf("semantic validation failed: %w", err)
		}
	}

	return nil
}
