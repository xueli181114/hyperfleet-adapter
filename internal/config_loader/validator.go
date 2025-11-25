package config_loader

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/golang/glog"
	"github.com/google/cel-go/cel"

	"github.com/openshift-hyperfleet/hyperfleet-adapter/internal/criteria"
)

// -----------------------------------------------------------------------------
// Validation Errors
// -----------------------------------------------------------------------------

// ValidationError represents a validation error with context
type ValidationError struct {
	Path    string
	Message string
}

func (e *ValidationError) Error() string {
	return fmt.Sprintf("%s: %s", e.Path, e.Message)
}

// ValidationErrors holds multiple validation errors
type ValidationErrors struct {
	Errors []ValidationError
}

func (ve *ValidationErrors) Error() string {
	if len(ve.Errors) == 0 {
		return "no validation errors"
	}
	var msgs []string
	for _, e := range ve.Errors {
		msgs = append(msgs, e.Error())
	}
	return fmt.Sprintf("validation failed with %d error(s):\n  - %s", len(ve.Errors), strings.Join(msgs, "\n  - "))
}

func (ve *ValidationErrors) Add(path, message string) {
	ve.Errors = append(ve.Errors, ValidationError{Path: path, Message: message})
}

func (ve *ValidationErrors) HasErrors() bool {
	return len(ve.Errors) > 0
}

// -----------------------------------------------------------------------------
// Validator
// -----------------------------------------------------------------------------

// Validator performs semantic validation on AdapterConfig.
// It validates operators, template variables, CEL expressions, and K8s manifests.
type Validator struct {
	config        *AdapterConfig
	errors        *ValidationErrors
	definedParams map[string]bool
	celEnv        *cel.Env
}

// NewValidator creates a new Validator for the given config
func NewValidator(config *AdapterConfig) *Validator {
	return &Validator{
		config: config,
		errors: &ValidationErrors{},
	}
}

// Validate performs all semantic validations and returns any errors.
// This is the main entry point for validation.
func (v *Validator) Validate() error {
	if v.config == nil {
		return fmt.Errorf("config is nil")
	}

	// Initialize validation context
	v.collectDefinedParameters()
	if err := v.initCELEnv(); err != nil {
		v.errors.Add("cel", fmt.Sprintf("failed to create CEL environment: %v", err))
	}

	// Run all validators
	v.validateConditionOperators()
	v.validateTemplateVariables()
	v.validateCELExpressions()
	v.validateK8sManifests()

	if v.errors.HasErrors() {
		return v.errors
	}
	return nil
}

// -----------------------------------------------------------------------------
// Parameter Collection
// -----------------------------------------------------------------------------

// collectDefinedParameters collects all defined parameter names for template validation
func (v *Validator) collectDefinedParameters() {
	v.definedParams = v.config.GetDefinedVariables()
}

// -----------------------------------------------------------------------------
// Operator Validation
// -----------------------------------------------------------------------------

// validateConditionOperators validates all condition operators in the config
func (v *Validator) validateConditionOperators() {
	// Validate precondition conditions
	for i, precond := range v.config.Spec.Preconditions {
		for j, cond := range precond.Conditions {
			path := fmt.Sprintf("%s.%s[%d].%s[%d]", FieldSpec, FieldPreconditions, i, FieldConditions, j)
			v.validateOperator(cond.Operator, path)
		}
	}

	// Validate post action when conditions
	if v.config.Spec.Post != nil {
		for i, action := range v.config.Spec.Post.PostActions {
			if action.When != nil {
				for j, cond := range action.When.Conditions {
					path := fmt.Sprintf("%s.%s.%s[%d].%s.%s[%d]", FieldSpec, FieldPost, FieldPostActions, i, FieldWhen, FieldConditions, j)
					v.validateOperator(cond.Operator, path)
				}
			}
		}
	}
}

// validateOperator checks if an operator is valid
func (v *Validator) validateOperator(operator string, path string) {
	if operator == "" {
		v.errors.Add(path, "operator is required")
		return
	}
	if !criteria.IsValidOperator(operator) {
		v.errors.Add(path, fmt.Sprintf("invalid operator %q, must be one of: %s",
			operator, strings.Join(criteria.OperatorStrings(), ", ")))
	}
}

// -----------------------------------------------------------------------------
// Template Variable Validation
// -----------------------------------------------------------------------------

// templateVarRegex matches Go template variables like {{ .varName }} or {{ .nested.var }}
var templateVarRegex = regexp.MustCompile(`\{\{\s*\.([a-zA-Z_][a-zA-Z0-9_\.\-]*)\s*(?:\|[^}]*)?\}\}`)

// validateTemplateVariables validates that template variables are defined
func (v *Validator) validateTemplateVariables() {
	// Validate precondition API call URLs and bodies
	for i, precond := range v.config.Spec.Preconditions {
		if precond.APICall != nil {
			basePath := fmt.Sprintf("%s.%s[%d].%s", FieldSpec, FieldPreconditions, i, FieldAPICall)
			v.validateTemplateString(precond.APICall.URL, basePath+"."+FieldURL)
			v.validateTemplateString(precond.APICall.Body, basePath+"."+FieldBody)
			for j, header := range precond.APICall.Headers {
				v.validateTemplateString(header.Value,
					fmt.Sprintf("%s.%s[%d].%s", basePath, FieldHeaders, j, FieldHeaderValue))
			}
		}
	}

	// Validate resource manifests
	for i, resource := range v.config.Spec.Resources {
		resourcePath := fmt.Sprintf("%s.%s[%d]", FieldSpec, FieldResources, i)
		if manifest, ok := resource.Manifest.(map[string]interface{}); ok {
			v.validateTemplateMap(manifest, resourcePath+"."+FieldManifest)
		}
		if resource.Discovery != nil {
			discoveryPath := resourcePath + "." + FieldDiscovery
			v.validateTemplateString(resource.Discovery.Namespace, discoveryPath+"."+FieldNamespace)
			v.validateTemplateString(resource.Discovery.ByName, discoveryPath+"."+FieldByName)
			if resource.Discovery.BySelectors != nil {
				for k, val := range resource.Discovery.BySelectors.LabelSelector {
					v.validateTemplateString(val,
						fmt.Sprintf("%s.%s.%s[%s]", discoveryPath, FieldBySelectors, FieldLabelSelector, k))
				}
			}
		}
	}

	// Validate post action API calls
	if v.config.Spec.Post != nil {
		for i, action := range v.config.Spec.Post.PostActions {
			if action.APICall != nil {
				basePath := fmt.Sprintf("%s.%s.%s[%d].%s", FieldSpec, FieldPost, FieldPostActions, i, FieldAPICall)
				v.validateTemplateString(action.APICall.URL, basePath+"."+FieldURL)
				v.validateTemplateString(action.APICall.Body, basePath+"."+FieldBody)
				for j, header := range action.APICall.Headers {
					v.validateTemplateString(header.Value,
						fmt.Sprintf("%s.%s[%d].%s", basePath, FieldHeaders, j, FieldHeaderValue))
				}
			}
		}

		// Validate post params build value templates (build is now interface{})
		for i, param := range v.config.Spec.Post.Params {
			if param.Build != nil {
				if buildMap, ok := param.Build.(map[string]interface{}); ok {
					v.validateTemplateMap(buildMap, fmt.Sprintf("%s.%s.%s[%d].%s", FieldSpec, FieldPost, FieldParams, i, FieldBuild))
				}
			}
		}
	}

	// Validate top-level params build value templates
	for i, param := range v.config.Spec.Params {
		if param.Build != nil {
			if buildMap, ok := param.Build.(map[string]interface{}); ok {
				v.validateTemplateMap(buildMap, fmt.Sprintf("%s.%s[%d].%s", FieldSpec, FieldParams, i, FieldBuild))
			}
		}
	}
}

// validateTemplateString checks template variables in a string
func (v *Validator) validateTemplateString(s string, path string) {
	if s == "" {
		return
	}

	matches := templateVarRegex.FindAllStringSubmatch(s, -1)
	for _, match := range matches {
		if len(match) > 1 {
			varName := match[1]
			if !v.isVariableDefined(varName) {
				v.errors.Add(path, fmt.Sprintf("undefined template variable %q", varName))
			}
		}
	}
}

// isVariableDefined checks if a variable is defined (including nested paths)
func (v *Validator) isVariableDefined(varName string) bool {
	// Check exact match
	if v.definedParams[varName] {
		return true
	}

	// Check if the root variable is defined (for nested paths like clusterDetails.status.phase)
	parts := strings.Split(varName, ".")
	if len(parts) > 0 {
		root := parts[0]

		// Handle simple root variables (e.g. "metadata", "clusterId")
		if v.definedParams[root] {
			return true
		}

		// Special handling for resource aliases: treat "resources.<name>" as a root.
		// Resource aliases are registered as "resources.clusterNamespace" etc.,
		// so we need to check if "resources.<alias>" is defined for paths like
		// "resources.clusterNamespace.metadata.namespace"
		if root == FieldResources && len(parts) > 1 {
			alias := root + "." + parts[1]
			if v.definedParams[alias] {
				return true
			}
		}
	}

	return false
}

// validateTemplateMap recursively validates template variables in a map
func (v *Validator) validateTemplateMap(m map[string]interface{}, path string) {
	for key, value := range m {
		currentPath := fmt.Sprintf("%s.%s", path, key)
		switch val := value.(type) {
		case string:
			v.validateTemplateString(val, currentPath)
		case map[string]interface{}:
			v.validateTemplateMap(val, currentPath)
		case []interface{}:
			for i, item := range val {
				itemPath := fmt.Sprintf("%s[%d]", currentPath, i)
				if str, ok := item.(string); ok {
					v.validateTemplateString(str, itemPath)
				} else if m, ok := item.(map[string]interface{}); ok {
					v.validateTemplateMap(m, itemPath)
				}
			}
		}
	}
}

// -----------------------------------------------------------------------------
// CEL Expression Validation
// -----------------------------------------------------------------------------

// initCELEnv initializes the CEL environment dynamically from config-defined variables.
// This uses v.definedParams which must be populated by collectDefinedParameters() first.
func (v *Validator) initCELEnv() error {
	// Pre-allocate capacity: +2 for cel.OptionalTypes() and potential "resources" variable
	options := make([]cel.EnvOption, 0, len(v.definedParams)+2)

	// Enable optional types for optional chaining syntax (e.g., a.?b.?c)
	options = append(options, cel.OptionalTypes())

	// Track root variables we've already added (to avoid duplicates for nested paths)
	addedRoots := make(map[string]bool)

	for varName := range v.definedParams {
		// Extract root variable name (e.g., "clusterDetails" from "clusterDetails.status.phase")
		root := varName
		if idx := strings.Index(varName, "."); idx > 0 {
			root = varName[:idx]
		}

		// Skip if we've already added this root variable
		if addedRoots[root] {
			continue
		}
		addedRoots[root] = true

		// Use DynType since we don't know the actual type at validation time
		options = append(options, cel.Variable(root, cel.DynType))
	}

	// Always add "resources" as a map for resource lookups like resources.clusterNamespace
	if !addedRoots[FieldResources] {
		options = append(options, cel.Variable(FieldResources, cel.MapType(cel.StringType, cel.DynType)))
	}

	env, err := cel.NewEnv(options...)
	if err != nil {
		return err
	}
	v.celEnv = env
	return nil
}

// validateCELExpressions validates all CEL expressions in the config
func (v *Validator) validateCELExpressions() {
	if v.celEnv == nil {
		return // CEL env initialization failed, already reported
	}

	// Validate precondition expressions
	for i, precond := range v.config.Spec.Preconditions {
		if precond.Expression != "" {
			path := fmt.Sprintf("%s.%s[%d].%s", FieldSpec, FieldPreconditions, i, FieldExpression)
			v.validateCELExpression(precond.Expression, path)
		}
	}

	// Validate post action when expressions
	if v.config.Spec.Post != nil {
		for i, action := range v.config.Spec.Post.PostActions {
			if action.When != nil && action.When.Expression != "" {
				path := fmt.Sprintf("%s.%s.%s[%d].%s.%s", FieldSpec, FieldPost, FieldPostActions, i, FieldWhen, FieldExpression)
				v.validateCELExpression(action.When.Expression, path)
			}
		}

		// Validate post params build expressions (build is now interface{})
		// We recursively find and validate any "expression" fields in the build structure
		for i, param := range v.config.Spec.Post.Params {
			if param.Build != nil {
				if buildMap, ok := param.Build.(map[string]interface{}); ok {
					v.validateBuildExpressions(buildMap, fmt.Sprintf("%s.%s.%s[%d].%s", FieldSpec, FieldPost, FieldParams, i, FieldBuild))
				}
			}
		}
	}
}

// validateCELExpression validates a single CEL expression
func (v *Validator) validateCELExpression(expr string, path string) {
	if expr == "" {
		return
	}

	// Clean up the expression (remove leading/trailing whitespace and newlines)
	expr = strings.TrimSpace(expr)

	// Try to parse the expression
	ast, issues := v.celEnv.Parse(expr)
	if issues != nil && issues.Err() != nil {
		v.errors.Add(path, fmt.Sprintf("CEL parse error: %v", issues.Err()))
		return
	}

	// Try to check the expression (type checking)
	// Note: This may fail for dynamic variables, which is acceptable
	_, issues = v.celEnv.Check(ast)
	if issues != nil && issues.Err() != nil {
		glog.V(2).Infof("CEL type check failed for expression %q (validation continues): %v", expr, issues.Err())
	}
}

// validateBuildExpressions recursively validates CEL expressions in a build structure.
// It looks for any field named "expression" and validates it as a CEL expression.
func (v *Validator) validateBuildExpressions(m map[string]interface{}, path string) {
	for key, value := range m {
		currentPath := fmt.Sprintf("%s.%s", path, key)
		switch val := value.(type) {
		case string:
			// If the key is "expression", validate it as CEL
			if key == FieldExpression {
				v.validateCELExpression(val, currentPath)
			}
		case map[string]interface{}:
			v.validateBuildExpressions(val, currentPath)
		case []interface{}:
			for i, item := range val {
				itemPath := fmt.Sprintf("%s[%d]", currentPath, i)
				if m, ok := item.(map[string]interface{}); ok {
					v.validateBuildExpressions(m, itemPath)
				}
			}
		}
	}
}

// -----------------------------------------------------------------------------
// Kubernetes Manifest Validation
// -----------------------------------------------------------------------------

// validateK8sManifests validates Kubernetes resource manifests
func (v *Validator) validateK8sManifests() {
	for i, resource := range v.config.Spec.Resources {
		path := fmt.Sprintf("%s.%s[%d].%s", FieldSpec, FieldResources, i, FieldManifest)

		// Validate inline or single-ref manifest
		if manifest, ok := resource.Manifest.(map[string]interface{}); ok {
			// Check for ref (external template reference)
			if ref, hasRef := manifest[FieldRef].(string); hasRef {
				if ref == "" {
					v.errors.Add(path+"."+FieldRef, "manifest ref cannot be empty")
				}
				// Single ref: content will have been loaded into Manifest by loadFileReferences
				// and will be validated below if it's a valid manifest map
			} else if _, hasRefs := manifest[FieldRefs]; hasRefs {
				// Multiple refs: content loaded into ManifestItems, validated below
			} else {
				// Inline manifest - validate it
				v.validateK8sManifest(manifest, path)
			}
		}

		// Validate any loaded manifest items from manifest.refs (multiple refs)
		for j, item := range resource.ManifestItems {
			v.validateK8sManifest(item, fmt.Sprintf("%s.%s[%d].%s[%d]", FieldSpec, FieldResources, i, FieldManifestItems, j))
		}
	}
}

// validateK8sManifest validates a single Kubernetes manifest
func (v *Validator) validateK8sManifest(manifest map[string]interface{}, path string) {
	// Required fields for K8s resources
	requiredFields := []string{FieldAPIVersion, FieldKind, FieldMetadata}

	for _, field := range requiredFields {
		if _, ok := manifest[field]; !ok {
			v.errors.Add(path, fmt.Sprintf("missing required Kubernetes field %q", field))
		}
	}

	// Validate metadata has name
	if metadata, ok := manifest[FieldMetadata].(map[string]interface{}); ok {
		if _, hasName := metadata[FieldName]; !hasName {
			v.errors.Add(path+"."+FieldMetadata, fmt.Sprintf("missing required field %q", FieldName))
		}
	}

	// Validate apiVersion format
	if apiVersion, ok := manifest[FieldAPIVersion].(string); ok {
		if apiVersion == "" {
			v.errors.Add(path+"."+FieldAPIVersion, "apiVersion cannot be empty")
		}
	}

	// Validate kind
	if kind, ok := manifest[FieldKind].(string); ok {
		if kind == "" {
			v.errors.Add(path+"."+FieldKind, "kind cannot be empty")
		}
	}
}

// -----------------------------------------------------------------------------
// Public API (backward compatible)
// -----------------------------------------------------------------------------

// Validate performs semantic validation on the config including
// operators, template variables, CEL expressions, and K8s manifests.
// This is called automatically by Parse() after structural validation.
func Validate(config *AdapterConfig) error {
	return NewValidator(config).Validate()
}
