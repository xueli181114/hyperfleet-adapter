package criteria

import (
	"fmt"
	"reflect"
	"strconv"
	"strings"

	"github.com/golang/glog"
	"github.com/google/cel-go/cel"
	"github.com/google/cel-go/common/types"
	"github.com/google/cel-go/common/types/ref"
)

// CELEvaluator evaluates CEL expressions against a context
type CELEvaluator struct {
	env     *cel.Env
	context *EvaluationContext
}

// CELResult contains the result of evaluating a CEL expression.
// When using EvaluateSafe, errors are captured in Error/ErrorReason instead of being returned,
// allowing the caller to decide how to handle failures (e.g., treat as false, log, etc.).
type CELResult struct {
	// Value is the result of the CEL expression evaluation (nil if error)
	Value interface{}
	// Matched indicates if the result is boolean true (for conditions)
	// Always false when Error is set
	Matched bool
	// Type is the CEL type of the result ("error" when evaluation failed)
	Type string
	// Expression is the original expression that was evaluated
	Expression string
	// Error indicates if evaluation failed (nil if successful)
	Error error
	// ErrorReason provides a human-readable error description
	// Common values: "field not found", "null value access", "type mismatch"
	ErrorReason string
}

// HasError returns true if the evaluation resulted in an error
func (r *CELResult) HasError() bool {
	return r.Error != nil
}

// IsSuccess returns true if the evaluation succeeded without error
func (r *CELResult) IsSuccess() bool {
	return r.Error == nil
}

// NewCELEvaluator creates a new CEL evaluator with the given context
func NewCELEvaluator(ctx *EvaluationContext) (*CELEvaluator, error) {
	if ctx == nil {
		ctx = NewEvaluationContext()
	}

	// Build CEL environment with variables from context
	options := buildCELOptions(ctx)

	env, err := cel.NewEnv(options...)
	if err != nil {
		return nil, fmt.Errorf("failed to create CEL environment: %w", err)
	}

	return &CELEvaluator{
		env:     env,
		context: ctx,
	}, nil
}

// buildCELOptions creates CEL environment options from the context
// Variables are dynamically registered based on what's in ctx.Data
func buildCELOptions(ctx *EvaluationContext) []cel.EnvOption {
	options := make([]cel.EnvOption, 0)

	// Enable optional types for optional chaining syntax (e.g., a.?b.?c)
	options = append(options, cel.OptionalTypes())

	for key, value := range ctx.Data {
		celType := inferCELType(value)
		options = append(options, cel.Variable(key, celType))
	}

	return options
}

// inferCELType infers the CEL type from a Go value
func inferCELType(value interface{}) *cel.Type {
	if value == nil {
		return cel.DynType
	}

	switch value.(type) {
	case string:
		return cel.StringType
	case bool:
		return cel.BoolType
	case int, int8, int16, int32, int64:
		return cel.IntType
	case uint, uint8, uint16, uint32, uint64:
		return cel.UintType
	case float32, float64:
		return cel.DoubleType
	case []interface{}:
		return cel.ListType(cel.DynType)
	case map[string]interface{}:
		return cel.MapType(cel.StringType, cel.DynType)
	default:
		return cel.DynType
	}
}

// Evaluate evaluates a CEL expression and returns the result.
// Returns an error if evaluation fails. Use EvaluateSafe for error-tolerant evaluation.
func (e *CELEvaluator) Evaluate(expression string) (*CELResult, error) {
	result := e.EvaluateSafe(expression)
	if result.Error != nil {
		return nil, result.Error
	}
	return result, nil
}

// EvaluateSafe evaluates a CEL expression and captures any errors in the result.
// This never returns an error - all errors are captured in CELResult.Error and CELResult.ErrorReason.
// Use this when you want to handle evaluation failures gracefully at a higher level.
//
// Common error reasons include:
//   - "field not found": when accessing a key that doesn't exist (e.g., data.missing.field)
//   - "null value access": when accessing a field on a null value
//   - "type mismatch": when operations are applied to incompatible types
func (e *CELEvaluator) EvaluateSafe(expression string) *CELResult {
	expression = strings.TrimSpace(expression)
	if expression == "" {
		return &CELResult{
			Value:      true,
			Matched:    true,
			Type:       "bool",
			Expression: expression,
		}
	}

	// Parse the expression
	ast, issues := e.env.Parse(expression)
	if issues != nil && issues.Err() != nil {
		return &CELResult{
			Value:       nil,
			Matched:     false,
			Type:        "error",
			Expression:  expression,
			Error:       fmt.Errorf("CEL parse error: %w", issues.Err()),
			ErrorReason: fmt.Sprintf("parse error: %s", issues.Err()),
		}
	}

	// Type-check the expression (optional, may fail for dynamic types)
	checked, issues := e.env.Check(ast)
	if issues != nil && issues.Err() != nil {
		glog.V(2).Infof("CEL type check failed for expression %q (using parsed AST): %v", expression, issues.Err())
		// Use parsed AST if type checking fails (common with dynamic types)
		checked = ast
	}

	// Create the program
	prg, err := e.env.Program(checked)
	if err != nil {
		return &CELResult{
			Value:       nil,
			Matched:     false,
			Type:        "error",
			Expression:  expression,
			Error:       fmt.Errorf("CEL program creation error: %w", err),
			ErrorReason: fmt.Sprintf("program error: %s", err),
		}
	}

	// Evaluate the expression
	out, _, err := prg.Eval(e.context.Data)
	if err != nil {
		// Capture evaluation error - this includes "no such key" errors
		// Error is logged at debug level and captured in result for executor to handle
		errorReason := categorizeEvalError(err)
		glog.V(2).Infof("CEL evaluation failed for %q: %s (%v)", expression, errorReason, err)
		return &CELResult{
			Value:       nil,
			Matched:     false,
			Type:        "error",
			Expression:  expression,
			Error:       fmt.Errorf("CEL evaluation error: %w", err),
			ErrorReason: errorReason,
		}
	}

	// Convert result
	result := &CELResult{
		Value:      out.Value(),
		Type:       out.Type().TypeName(),
		Expression: expression,
	}

	// Check if result is boolean true
	if boolVal, ok := out.Value().(bool); ok {
		result.Matched = boolVal
	} else {
		// Non-boolean results are considered "matched" if not nil/empty
		result.Matched = !isEmptyValue(out)
	}

	return result
}

// categorizeEvalError provides a human-readable error reason for common CEL evaluation errors
func categorizeEvalError(err error) string {
	errStr := err.Error()
	if strings.Contains(errStr, "no such key") {
		return "field not found"
	}
	if strings.Contains(errStr, "no such attribute") {
		return "attribute not found"
	}
	if strings.Contains(errStr, "null") || strings.Contains(errStr, "nil") {
		return "null value access"
	}
	if strings.Contains(errStr, "type") {
		return "type mismatch"
	}
	return fmt.Sprintf("evaluation failed: %s", errStr)
}

// EvaluateBool evaluates a CEL expression that should return a boolean
func (e *CELEvaluator) EvaluateBool(expression string) (bool, error) {
	result, err := e.Evaluate(expression)
	if err != nil {
		return false, err
	}

	if boolVal, ok := result.Value.(bool); ok {
		return boolVal, nil
	}

	return result.Matched, nil
}

// EvaluateString evaluates a CEL expression that should return a string
func (e *CELEvaluator) EvaluateString(expression string) (string, error) {
	result, err := e.Evaluate(expression)
	if err != nil {
		return "", err
	}

	if strVal, ok := result.Value.(string); ok {
		return strVal, nil
	}

	return fmt.Sprintf("%v", result.Value), nil
}

// isEmptyValue checks if a CEL value is empty/nil
func isEmptyValue(val ref.Val) bool {
	if val == nil {
		return true
	}

	switch v := val.(type) {
	case types.Null:
		return true
	case types.String:
		return string(v) == ""
	case types.Bool:
		return !bool(v)
	default:
		// Check if it's a list or map
		if lister, ok := val.(interface{ Size() ref.Val }); ok {
			size := lister.Size()
			if intSize, ok := size.(types.Int); ok {
				return int64(intSize) == 0
			}
		}
		return false
	}
}

// ConditionToCEL converts a structured condition to a CEL expression.
// The generated expression does NOT include null-safety guards - if the field
// doesn't exist, CEL will return an error which is captured by EvaluateSafe().
// This allows the caller to decide how to handle missing fields at a higher level.
func ConditionToCEL(field, operator string, value interface{}) (string, error) {
	celValue, err := formatCELValue(value)
	if err != nil {
		return "", err
	}

	switch operator {
	case "equals":
		return fmt.Sprintf("%s == %s", field, celValue), nil
	case "notEquals":
		return fmt.Sprintf("%s != %s", field, celValue), nil
	case "in":
		return fmt.Sprintf("%s in %s", field, celValue), nil
	case "notIn":
		return fmt.Sprintf("!(%s in %s)", field, celValue), nil
	case "contains":
		return fmt.Sprintf("%s.contains(%s)", field, celValue), nil
	case "greaterThan":
		return fmt.Sprintf("%s > %s", field, celValue), nil
	case "lessThan":
		return fmt.Sprintf("%s < %s", field, celValue), nil
	case "exists":
		// For nested paths, use has() which checks if the field exists
		// Note: has(a.b.c) will error if a.b doesn't exist - caller handles via EvaluateSafe
		if strings.Contains(field, ".") {
			return fmt.Sprintf("has(%s)", field), nil
		}
		// For top-level variables, check not null and not empty string
		return fmt.Sprintf("(%s != null && %s != \"\")", field, field), nil
	default:
		return "", fmt.Errorf("unsupported operator for CEL conversion: %s", operator)
	}
}

// formatCELValue formats a Go value as a CEL literal
func formatCELValue(value interface{}) (string, error) {
	if value == nil {
		return "null", nil
	}

	switch v := value.(type) {
	case string:
		return strconv.Quote(v), nil
	case bool:
		return fmt.Sprintf("%t", v), nil
	case int, int8, int16, int32, int64:
		return fmt.Sprintf("%d", v), nil
	case uint, uint8, uint16, uint32, uint64:
		return fmt.Sprintf("%du", v), nil
	case float32, float64:
		return fmt.Sprintf("%v", v), nil
	case []interface{}:
		items := make([]string, len(v))
		for i, item := range v {
			formatted, err := formatCELValue(item)
			if err != nil {
				return "", err
			}
			items[i] = formatted
		}
		return fmt.Sprintf("[%s]", strings.Join(items, ", ")), nil
	case []string:
		items := make([]string, len(v))
		for i, item := range v {
			formatted, err := formatCELValue(item)
			if err != nil {
				return "", err
			}
			items[i] = formatted
		}
		return fmt.Sprintf("[%s]", strings.Join(items, ", ")), nil
	default:
		// Handle other types via reflection
		rv := reflect.ValueOf(value)
		switch rv.Kind() {
		case reflect.Slice, reflect.Array:
			items := make([]string, rv.Len())
			for i := 0; i < rv.Len(); i++ {
				formatted, err := formatCELValue(rv.Index(i).Interface())
				if err != nil {
					return "", err
				}
				items[i] = formatted
			}
			return fmt.Sprintf("[%s]", strings.Join(items, ", ")), nil
		case reflect.String:
			return strconv.Quote(rv.String()), nil
		case reflect.Bool:
			return fmt.Sprintf("%t", rv.Bool()), nil
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
			return fmt.Sprintf("%d", rv.Int()), nil
		case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
			return fmt.Sprintf("%du", rv.Uint()), nil
		case reflect.Float32, reflect.Float64:
			return fmt.Sprintf("%v", rv.Float()), nil
		default:
			return "", fmt.Errorf("unsupported type for CEL formatting: %T", value)
		}
	}
}

// ConditionsToCEL converts multiple conditions to a single CEL expression (AND logic)
func ConditionsToCEL(conditions []ConditionDef) (string, error) {
	if len(conditions) == 0 {
		return "true", nil
	}

	expressions := make([]string, len(conditions))
	for i, cond := range conditions {
		expr, err := ConditionToCEL(cond.Field, string(cond.Operator), cond.Value)
		if err != nil {
			return "", fmt.Errorf("failed to convert condition %d: %w", i, err)
		}
		expressions[i] = "(" + expr + ")"
	}

	return strings.Join(expressions, " && "), nil
}

