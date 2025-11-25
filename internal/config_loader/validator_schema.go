package config_loader

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// -----------------------------------------------------------------------------
// SchemaValidator
// -----------------------------------------------------------------------------

// SchemaValidator performs schema validation on AdapterConfig.
// It validates required fields, file references, and loads external files.
type SchemaValidator struct {
	config  *AdapterConfig
	baseDir string // Base directory for resolving relative paths
}

// NewSchemaValidator creates a new SchemaValidator for the given config
func NewSchemaValidator(config *AdapterConfig, baseDir string) *SchemaValidator {
	return &SchemaValidator{
		config:  config,
		baseDir: baseDir,
	}
}

// ValidateStructure performs all structural validations.
// Returns error on first validation failure (fail-fast).
func (v *SchemaValidator) ValidateStructure() error {
	validators := []func() error{
		v.validateAPIVersionAndKind,
		v.validateMetadata,
		v.validateAdapterSpec,
		v.validateParams,
		v.validatePreconditions,
		v.validateResources,
		v.validatePostActions,
	}

	for _, validate := range validators {
		if err := validate(); err != nil {
			return err
		}
	}

	return nil
}

// ValidateFileReferences validates that all file references exist.
// Only runs if baseDir is set.
func (v *SchemaValidator) ValidateFileReferences() error {
	if v.baseDir == "" {
		return nil
	}
	return v.validateFileReferences()
}

// LoadFileReferences loads content from file references into the config.
// Only runs if baseDir is set.
func (v *SchemaValidator) LoadFileReferences() error {
	if v.baseDir == "" {
		return nil
	}
	return v.loadFileReferences()
}

// -----------------------------------------------------------------------------
// Core Structural Validators
// -----------------------------------------------------------------------------

func (v *SchemaValidator) validateAPIVersionAndKind() error {
	if v.config.APIVersion == "" {
		return fmt.Errorf("apiVersion is required")
	}
	if !IsSupportedAPIVersion(v.config.APIVersion) {
		return fmt.Errorf("unsupported apiVersion %q (supported: %s)",
			v.config.APIVersion, strings.Join(SupportedAPIVersions, ", "))
	}
	if v.config.Kind == "" {
		return fmt.Errorf("kind is required")
	}
	if v.config.Kind != ExpectedKind {
		return fmt.Errorf("invalid kind %q (expected: %q)", v.config.Kind, ExpectedKind)
	}
	return nil
}

func (v *SchemaValidator) validateMetadata() error {
	if v.config.Metadata.Name == "" {
		return fmt.Errorf("metadata.name is required")
	}
	return nil
}

func (v *SchemaValidator) validateAdapterSpec() error {
	if v.config.Spec.Adapter.Version == "" {
		return fmt.Errorf("%s.%s.%s is required", FieldSpec, FieldAdapter, FieldVersion)
	}
	return nil
}

func (v *SchemaValidator) validateParams() error {
	for i, param := range v.config.Spec.Params {
		path := fmt.Sprintf("%s.%s[%d]", FieldSpec, FieldParams, i)

		if param.Name == "" {
			return fmt.Errorf("%s.%s is required", path, FieldName)
		}

		if !v.hasParamSource(param) {
			return fmt.Errorf("%s (%s): must specify %s, %s, %s, or fetchExternalResource",
				path, param.Name, FieldSource, FieldBuild, FieldBuildRef)
		}
	}
	return nil
}

func (v *SchemaValidator) validatePreconditions() error {
	for i, precond := range v.config.Spec.Preconditions {
		path := fmt.Sprintf("%s.%s[%d]", FieldSpec, FieldPreconditions, i)

		if precond.Name == "" {
			return fmt.Errorf("%s.%s is required", path, FieldName)
		}

		if !v.hasPreconditionLogic(precond) {
			return fmt.Errorf("%s (%s): must specify %s, %s, or %s",
				path, precond.Name, FieldAPICall, FieldExpression, FieldConditions)
		}

		if precond.APICall != nil {
			if err := v.validateAPICall(precond.APICall, path+"."+FieldAPICall); err != nil {
				return err
			}
		}
	}
	return nil
}

func (v *SchemaValidator) validateResources() error {
	for i, resource := range v.config.Spec.Resources {
		path := fmt.Sprintf("%s.%s[%d]", FieldSpec, FieldResources, i)

		if resource.Name == "" {
			return fmt.Errorf("%s.%s is required", path, FieldName)
		}

		if resource.Manifest == nil {
			return fmt.Errorf("%s (%s): %s is required", path, resource.Name, FieldManifest)
		}

		// Discovery is required for ALL resources to find them on subsequent messages
		if err := v.validateResourceDiscovery(&resource, path); err != nil {
			return err
		}
	}
	return nil
}

func (v *SchemaValidator) validateResourceDiscovery(resource *Resource, path string) error {
	if resource.Discovery == nil {
		return fmt.Errorf("%s (%s): %s is required to find the resource on subsequent messages", path, resource.Name, FieldDiscovery)
	}

	// Namespace must be set ("*" means all namespaces)
	if resource.Discovery.Namespace == "" {
		return fmt.Errorf("%s (%s): %s.%s is required (use \"*\" for all namespaces)", path, resource.Name, FieldDiscovery, FieldNamespace)
	}

	// Either byName or bySelectors must be configured
	hasByName := resource.Discovery.ByName != ""
	hasBySelectors := resource.Discovery.BySelectors != nil

	if !hasByName && !hasBySelectors {
		return fmt.Errorf("%s (%s): %s must have either %s or %s configured", path, resource.Name, FieldDiscovery, FieldByName, FieldBySelectors)
	}

	// If bySelectors is used, at least one selector must be defined
	if hasBySelectors {
		if err := v.validateSelectors(resource.Discovery.BySelectors, path, resource.Name); err != nil {
			return err
		}
	}

	return nil
}

func (v *SchemaValidator) validateSelectors(selectors *SelectorConfig, path, resourceName string) error {
	if selectors == nil {
		return fmt.Errorf("%s (%s): %s is nil", path, resourceName, FieldBySelectors)
	}

	if len(selectors.LabelSelector) == 0 {
		return fmt.Errorf("%s (%s): %s must have %s defined", path, resourceName, FieldBySelectors, FieldLabelSelector)
	}

	return nil
}

func (v *SchemaValidator) validatePostActions() error {
	if v.config.Spec.Post == nil {
		return nil
	}

	for i, action := range v.config.Spec.Post.PostActions {
		path := fmt.Sprintf("%s.%s.%s[%d]", FieldSpec, FieldPost, FieldPostActions, i)

		if action.Name == "" {
			return fmt.Errorf("%s.%s is required", path, FieldName)
		}

		if action.APICall != nil {
			if err := v.validateAPICall(action.APICall, path+"."+FieldAPICall); err != nil {
				return err
			}
		}
	}
	return nil
}

func (v *SchemaValidator) validateAPICall(apiCall *APICall, path string) error {
	if apiCall == nil {
		return fmt.Errorf("%s: %s is nil", path, FieldAPICall)
	}

	if apiCall.Method == "" {
		return fmt.Errorf("%s.%s is required", path, FieldMethod)
	}

	if _, valid := ValidHTTPMethods[apiCall.Method]; !valid {
		return fmt.Errorf("%s.%s %q is invalid (allowed: %s)", path, FieldMethod, apiCall.Method, strings.Join(ValidHTTPMethodsList, ", "))
	}

	if apiCall.URL == "" {
		return fmt.Errorf("%s.%s is required", path, FieldURL)
	}

	return nil
}

// -----------------------------------------------------------------------------
// File Reference Validation
// -----------------------------------------------------------------------------

func (v *SchemaValidator) validateFileReferences() error {
	var errors []string

	// Validate buildRef in spec.params
	for i, param := range v.config.Spec.Params {
		if param.BuildRef != "" {
			path := fmt.Sprintf("%s.%s[%d].%s", FieldSpec, FieldParams, i, FieldBuildRef)
			if err := v.validateFileExists(param.BuildRef, path); err != nil {
				errors = append(errors, err.Error())
			}
		}
	}

	// Validate buildRef in spec.post.params
	if v.config.Spec.Post != nil {
		for i, param := range v.config.Spec.Post.Params {
			if param.BuildRef != "" {
				path := fmt.Sprintf("%s.%s.%s[%d].%s", FieldSpec, FieldPost, FieldParams, i, FieldBuildRef)
				if err := v.validateFileExists(param.BuildRef, path); err != nil {
					errors = append(errors, err.Error())
				}
			}
		}
	}

	// Validate manifest.ref and manifest.refs in spec.resources
	for i, resource := range v.config.Spec.Resources {
		refs := resource.GetManifestRefs()
		for j, ref := range refs {
			var path string
			if len(refs) == 1 {
				path = fmt.Sprintf("%s.%s[%d].%s.%s", FieldSpec, FieldResources, i, FieldManifest, FieldRef)
			} else {
				path = fmt.Sprintf("%s.%s[%d].%s.%s[%d]", FieldSpec, FieldResources, i, FieldManifest, FieldRefs, j)
			}
			if err := v.validateFileExists(ref, path); err != nil {
				errors = append(errors, err.Error())
			}
		}
	}

	if len(errors) > 0 {
		return fmt.Errorf("file reference errors:\n  - %s", strings.Join(errors, "\n  - "))
	}
	return nil
}

func (v *SchemaValidator) validateFileExists(refPath, configPath string) error {
	if refPath == "" {
		return fmt.Errorf("%s: file reference is empty", configPath)
	}

	fullPath, err := v.resolvePath(refPath)
	if err != nil {
		return fmt.Errorf("%s: %w", configPath, err)
	}

	// Check if file exists
	info, err := os.Stat(fullPath)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("%s: referenced file %q does not exist (resolved to %q)", configPath, refPath, fullPath)
		}
		return fmt.Errorf("%s: error checking file %q: %w", configPath, refPath, err)
	}

	// Ensure it's a file, not a directory
	if info.IsDir() {
		return fmt.Errorf("%s: referenced path %q is a directory, not a file", configPath, refPath)
	}

	return nil
}

// -----------------------------------------------------------------------------
// File Reference Loading
// -----------------------------------------------------------------------------

func (v *SchemaValidator) loadFileReferences() error {
	// Load manifest.ref or manifest.refs in spec.resources
	for i := range v.config.Spec.Resources {
		resource := &v.config.Spec.Resources[i]
		refs := resource.GetManifestRefs()
		if len(refs) == 0 {
			continue
		}

		// Load all referenced files
		items := make([]map[string]interface{}, 0, len(refs))
		for j, ref := range refs {
			content, err := v.loadYAMLFile(ref)
			if err != nil {
				if len(refs) == 1 {
					return fmt.Errorf("%s.%s[%d].%s.%s: %w", FieldSpec, FieldResources, i, FieldManifest, FieldRef, err)
				}
				return fmt.Errorf("%s.%s[%d].%s.%s[%d]: %w", FieldSpec, FieldResources, i, FieldManifest, FieldRefs, j, err)
			}
			items = append(items, content)
		}

		// Store loaded items
		if len(items) == 1 {
			// Single ref: replace manifest with content (backward compatible)
			resource.Manifest = items[0]
		} else {
			// Multiple refs: store in ManifestItems array
			resource.ManifestItems = items
		}
	}

	// Load buildRef in spec.params
	for i := range v.config.Spec.Params {
		param := &v.config.Spec.Params[i]
		if param.BuildRef != "" {
			content, err := v.loadYAMLFile(param.BuildRef)
			if err != nil {
				return fmt.Errorf("%s.%s[%d].%s: %w", FieldSpec, FieldParams, i, FieldBuildRef, err)
			}
			param.BuildRefContent = content
		}
	}

	// Load buildRef in spec.post.params
	if v.config.Spec.Post != nil {
		for i := range v.config.Spec.Post.Params {
			param := &v.config.Spec.Post.Params[i]
			if param.BuildRef != "" {
				content, err := v.loadYAMLFile(param.BuildRef)
				if err != nil {
					return fmt.Errorf("%s.%s.%s[%d].%s: %w", FieldSpec, FieldPost, FieldParams, i, FieldBuildRef, err)
				}
				param.BuildRefContent = content
			}
		}
	}

	return nil
}

func (v *SchemaValidator) loadYAMLFile(refPath string) (map[string]interface{}, error) {
	fullPath, err := v.resolvePath(refPath)
	if err != nil {
		return nil, err
	}

	data, err := os.ReadFile(fullPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read file %q: %w", fullPath, err)
	}

	var content map[string]interface{}
	if err := yaml.Unmarshal(data, &content); err != nil {
		return nil, fmt.Errorf("failed to parse YAML file %q: %w", fullPath, err)
	}

	return content, nil
}

// resolvePath resolves a relative path against the base directory and validates
// that the resolved path does not escape the base directory.
// Returns the resolved path and an error if path traversal is detected.
func (v *SchemaValidator) resolvePath(refPath string) (string, error) {
	// Get absolute, clean path for base directory
	baseAbs, err := filepath.Abs(v.baseDir)
	if err != nil {
		return "", fmt.Errorf("failed to resolve base directory: %w", err)
	}
	baseClean := filepath.Clean(baseAbs)

	var targetPath string
	if filepath.IsAbs(refPath) {
		targetPath = filepath.Clean(refPath)
	} else {
		targetPath = filepath.Clean(filepath.Join(baseClean, refPath))
	}

	// Check if target path is within base directory using filepath.Rel
	rel, err := filepath.Rel(baseClean, targetPath)
	if err != nil {
		return "", fmt.Errorf("path %q escapes base directory", refPath)
	}

	// If the relative path starts with "..", it escapes the base directory
	if strings.HasPrefix(rel, "..") {
		return "", fmt.Errorf("path %q escapes base directory", refPath)
	}

	return targetPath, nil
}

// -----------------------------------------------------------------------------
// Validation Helpers
// -----------------------------------------------------------------------------

func (v *SchemaValidator) hasParamSource(param Parameter) bool {
	return param.Source != "" ||
		param.Build != nil ||
		param.BuildRef != "" ||
		param.FetchExternalResource != nil
}

func (v *SchemaValidator) hasPreconditionLogic(precond Precondition) bool {
	return precond.APICall != nil ||
		precond.Expression != "" ||
		len(precond.Conditions) > 0
}

// -----------------------------------------------------------------------------
// Package-level Helper Functions (for backward compatibility)
// -----------------------------------------------------------------------------

// IsSupportedAPIVersion checks if the given apiVersion is supported
func IsSupportedAPIVersion(apiVersion string) bool {
	for _, v := range SupportedAPIVersions {
		if v == apiVersion {
			return true
		}
	}
	return false
}

// ValidateAdapterVersion validates the config's adapter version matches the expected version
func ValidateAdapterVersion(config *AdapterConfig, expectedVersion string) error {
	if expectedVersion == "" {
		return nil
	}

	configVersion := config.Spec.Adapter.Version
	if configVersion != expectedVersion {
		return fmt.Errorf("adapter version mismatch: config %q != adapter %q",
			configVersion, expectedVersion)
	}

	return nil
}

// -----------------------------------------------------------------------------
// Legacy Functions (for backward compatibility with loader.go)
// -----------------------------------------------------------------------------

func validateAPIVersionAndKind(config *AdapterConfig) error {
	return NewSchemaValidator(config, "").validateAPIVersionAndKind()
}

func validateMetadata(config *AdapterConfig) error {
	return NewSchemaValidator(config, "").validateMetadata()
}

func validateAdapterSpec(config *AdapterConfig) error {
	return NewSchemaValidator(config, "").validateAdapterSpec()
}

func validateParams(config *AdapterConfig) error {
	return NewSchemaValidator(config, "").validateParams()
}

func validatePreconditions(config *AdapterConfig) error {
	return NewSchemaValidator(config, "").validatePreconditions()
}

func validateResources(config *AdapterConfig) error {
	return NewSchemaValidator(config, "").validateResources()
}

func validatePostActions(config *AdapterConfig) error {
	return NewSchemaValidator(config, "").validatePostActions()
}

func validateFileReferences(config *AdapterConfig, baseDir string) error {
	return NewSchemaValidator(config, baseDir).ValidateFileReferences()
}

func loadFileReferences(config *AdapterConfig, baseDir string) error {
	return NewSchemaValidator(config, baseDir).LoadFileReferences()
}
