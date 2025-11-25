package criteria

import (
	"fmt"
)

// Operator represents a comparison operator
type Operator string

const (
	// OperatorEquals checks if field equals value
	OperatorEquals Operator = "equals"
	// OperatorNotEquals checks if field does not equal value
	OperatorNotEquals Operator = "notEquals"
	// OperatorIn checks if field is in a list of values
	OperatorIn Operator = "in"
	// OperatorNotIn checks if field is not in a list of values
	OperatorNotIn Operator = "notIn"
	// OperatorContains checks if field contains value (for strings and arrays)
	OperatorContains Operator = "contains"
	// OperatorGreaterThan checks if field is greater than value
	OperatorGreaterThan Operator = "greaterThan"
	// OperatorLessThan checks if field is less than value
	OperatorLessThan Operator = "lessThan"
	// OperatorExists checks if field exists (is not nil/empty)
	OperatorExists Operator = "exists"
)

// SupportedOperators lists all supported operators.
var SupportedOperators = []Operator{
	OperatorEquals,
	OperatorNotEquals,
	OperatorIn,
	OperatorNotIn,
	OperatorContains,
	OperatorGreaterThan,
	OperatorLessThan,
	OperatorExists,
}

// IsValidOperator checks if the given operator string is valid
func IsValidOperator(op string) bool {
	for _, supported := range SupportedOperators {
		if string(supported) == op {
			return true
		}
	}
	return false
}

// OperatorStrings returns all operators as strings
func OperatorStrings() []string {
	result := make([]string, len(SupportedOperators))
	for i, op := range SupportedOperators {
		result[i] = string(op)
	}
	return result
}

// EvaluationContext holds the data available for criteria evaluation
type EvaluationContext struct {
	// Data contains all variables available for evaluation
	Data map[string]interface{}
}

// NewEvaluationContext creates a new evaluation context
func NewEvaluationContext() *EvaluationContext {
	return &EvaluationContext{
		Data: make(map[string]interface{}),
	}
}

// Set sets a variable in the context
func (c *EvaluationContext) Set(key string, value interface{}) {
	c.Data[key] = value
}

// Get retrieves a variable from the context
func (c *EvaluationContext) Get(key string) (interface{}, bool) {
	val, ok := c.Data[key]
	return val, ok
}

// GetNestedField retrieves a nested field using dot notation (e.g., "status.phase")
func (c *EvaluationContext) GetNestedField(path string) (interface{}, error) {
	return getNestedField(c.Data, path)
}

// Merge merges another context into this one
func (c *EvaluationContext) Merge(other *EvaluationContext) {
	if other == nil {
		return
	}
	for k, v := range other.Data {
		c.Data[k] = v
	}
}

// EvaluationError represents an error during criteria evaluation
type EvaluationError struct {
	Field   string
	Message string
	Err     error
}

func (e *EvaluationError) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("evaluation error for field '%s': %s: %v", e.Field, e.Message, e.Err)
	}
	return fmt.Sprintf("evaluation error for field '%s': %s", e.Field, e.Message)
}

func (e *EvaluationError) Unwrap() error {
	return e.Err
}

