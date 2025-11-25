package criteria

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewEvaluationContext(t *testing.T) {
	ctx := NewEvaluationContext()
	assert.NotNil(t, ctx)
	assert.NotNil(t, ctx.Data)
}

func TestEvaluationContextSetGet(t *testing.T) {
	ctx := NewEvaluationContext()
	
	ctx.Set("key1", "value1")
	ctx.Set("key2", 42)
	ctx.Set("key3", map[string]string{"nested": "value"})

	val, ok := ctx.Get("key1")
	assert.True(t, ok)
	assert.Equal(t, "value1", val)

	val, ok = ctx.Get("key2")
	assert.True(t, ok)
	assert.Equal(t, 42, val)

	val, ok = ctx.Get("nonexistent")
	assert.False(t, ok)
	assert.Nil(t, val)
}

func TestEvaluationContextGetNestedField(t *testing.T) {
	ctx := NewEvaluationContext()
	ctx.Set("cluster", map[string]interface{}{
		"status": map[string]interface{}{
			"phase": "Ready",
			"conditions": []interface{}{
				map[string]interface{}{"type": "Available", "status": "True"},
			},
		},
		"spec": map[string]interface{}{
			"provider": "aws",
		},
	})

	tests := []struct {
		name      string
		path      string
		want      interface{}
		wantError bool
	}{
		{
			name: "simple nested field",
			path: "cluster.status.phase",
			want: "Ready",
		},
		{
			name: "deeply nested field",
			path: "cluster.spec.provider",
			want: "aws",
		},
		{
			name:      "nonexistent field",
			path:      "cluster.status.nonexistent",
			wantError: true,
		},
		{
			name:      "nonexistent top level",
			path:      "nonexistent.field",
			wantError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			val, err := ctx.GetNestedField(tt.path)
			if tt.wantError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.want, val)
			}
		})
	}
}

func TestEvaluationContextMerge(t *testing.T) {
	ctx1 := NewEvaluationContext()
	ctx1.Set("key1", "value1")
	ctx1.Set("key2", "value2")

	ctx2 := NewEvaluationContext()
	ctx2.Set("key2", "overwritten")
	ctx2.Set("key3", "value3")

	ctx1.Merge(ctx2)

	val, _ := ctx1.Get("key1")
	assert.Equal(t, "value1", val)

	val, _ = ctx1.Get("key2")
	assert.Equal(t, "overwritten", val)

	val, _ = ctx1.Get("key3")
	assert.Equal(t, "value3", val)
}

func TestEvaluateEquals(t *testing.T) {
	tests := []struct {
		name      string
		field     interface{}
		value     interface{}
		want      bool
		wantError bool
	}{
		{
			name:  "equal strings",
			field: "test",
			value: "test",
			want:  true,
		},
		{
			name:  "not equal strings",
			field: "test1",
			value: "test2",
			want:  false,
		},
		{
			name:  "equal numbers",
			field: 42,
			value: 42,
			want:  true,
		},
		{
			name:  "not equal numbers",
			field: 42,
			value: 43,
			want:  false,
		},
		{
			name:  "both nil",
			field: nil,
			value: nil,
			want:  true,
		},
		{
			name:  "field nil",
			field: nil,
			value: "test",
			want:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := evaluateEquals(tt.field, tt.value)
			if tt.wantError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.want, result)
			}
		})
	}
}

func TestEvaluateIn(t *testing.T) {
	tests := []struct {
		name      string
		field     interface{}
		list      interface{}
		want      bool
		wantError bool
	}{
		{
			name:  "value in string list",
			field: "aws",
			list:  []interface{}{"aws", "gcp", "azure"},
			want:  true,
		},
		{
			name:  "value not in string list",
			field: "unknown",
			list:  []interface{}{"aws", "gcp", "azure"},
			want:  false,
		},
		{
			name:  "number in number list",
			field: 2,
			list:  []interface{}{1, 2, 3},
			want:  true,
		},
		{
			name:      "not a list",
			field:     "test",
			list:      "not a list",
			wantError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := evaluateIn(tt.field, tt.list)
			if tt.wantError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.want, result)
			}
		})
	}
}

func TestEvaluateContains(t *testing.T) {
	tests := []struct {
		name      string
		field     interface{}
		needle    interface{}
		want      bool
		wantError bool
	}{
		{
			name:   "string contains substring",
			field:  "hello world",
			needle: "world",
			want:   true,
		},
		{
			name:   "string does not contain substring",
			field:  "hello world",
			needle: "xyz",
			want:   false,
		},
		{
			name:   "array contains value",
			field:  []interface{}{"a", "b", "c"},
			needle: "b",
			want:   true,
		},
		{
			name:   "array does not contain value",
			field:  []interface{}{"a", "b", "c"},
			needle: "d",
			want:   false,
		},
		{
			name:      "invalid field type",
			field:     42,
			needle:    "test",
			wantError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := evaluateContains(tt.field, tt.needle)
			if tt.wantError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.want, result)
			}
		})
	}
}

func TestEvaluateGreaterThan(t *testing.T) {
	tests := []struct {
		name      string
		field     interface{}
		threshold interface{}
		want      bool
		wantError bool
	}{
		{
			name:      "int greater than",
			field:     10,
			threshold: 5,
			want:      true,
		},
		{
			name:      "int not greater than",
			field:     5,
			threshold: 10,
			want:      false,
		},
		{
			name:      "float greater than",
			field:     10.5,
			threshold: 10.0,
			want:      true,
		},
		{
			name:      "equal values",
			field:     10,
			threshold: 10,
			want:      false,
		},
		{
			name:      "non-numeric field",
			field:     "test",
			threshold: 10,
			wantError: true,
		},
		{
			name:      "zero field greater than negative",
			field:     0,
			threshold: -1,
			want:      true,
		},
		{
			name:      "zero field not greater than zero",
			field:     0,
			threshold: 0,
			want:      false,
		},
		{
			name:      "zero field not greater than positive",
			field:     0,
			threshold: 1,
			want:      false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := evaluateGreaterThan(tt.field, tt.threshold)
			if tt.wantError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.want, result)
			}
		})
	}
}

func TestEvaluateLessThan(t *testing.T) {
	tests := []struct {
		name      string
		field     interface{}
		threshold interface{}
		want      bool
		wantError bool
	}{
		{
			name:      "int less than",
			field:     5,
			threshold: 10,
			want:      true,
		},
		{
			name:      "int not less than",
			field:     10,
			threshold: 5,
			want:      false,
		},
		{
			name:      "float less than",
			field:     9.5,
			threshold: 10.0,
			want:      true,
		},
		{
			name:      "equal values",
			field:     10,
			threshold: 10,
			want:      false,
		},
		{
			name:      "non-numeric field",
			field:     "test",
			threshold: 10,
			wantError: true,
		},
		{
			name:      "zero field less than positive",
			field:     0,
			threshold: 1,
			want:      true,
		},
		{
			name:      "zero field not less than zero",
			field:     0,
			threshold: 0,
			want:      false,
		},
		{
			name:      "zero field not less than negative",
			field:     0,
			threshold: -1,
			want:      false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := evaluateLessThan(tt.field, tt.threshold)
			if tt.wantError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.want, result)
			}
		})
	}
}

func TestEvaluateExists(t *testing.T) {
	tests := []struct {
		name  string
		field interface{}
		want  bool
	}{
		{
			name:  "non-nil string",
			field: "test",
			want:  true,
		},
		{
			name:  "empty string",
			field: "",
			want:  false,
		},
		{
			name:  "nil value",
			field: nil,
			want:  false,
		},
		{
			name:  "non-empty slice",
			field: []string{"a", "b"},
			want:  true,
		},
		{
			name:  "empty slice",
			field: []string{},
			want:  false,
		},
		{
			name:  "non-empty map",
			field: map[string]string{"key": "value"},
			want:  true,
		},
		{
			name:  "empty map",
			field: map[string]string{},
			want:  false,
		},
		{
			name:  "number",
			field: 42,
			want:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := evaluateExists(tt.field)
			assert.Equal(t, tt.want, result)
		})
	}
}

func TestEvaluatorEvaluateCondition(t *testing.T) {
	ctx := NewEvaluationContext()
	ctx.Set("clusterPhase", "Ready")
	ctx.Set("nodeCount", 5)
	ctx.Set("provider", "aws")
	ctx.Set("status", map[string]interface{}{
		"phase": "Active",
	})

	evaluator := NewEvaluator(ctx)

	tests := []struct {
		name      string
		field     string
		operator  Operator
		value     interface{}
		want      bool
		wantError bool
	}{
		{
			name:     "equals operator",
			field:    "clusterPhase",
			operator: OperatorEquals,
			value:    "Ready",
			want:     true,
		},
		{
			name:     "not equals operator",
			field:    "clusterPhase",
			operator: OperatorNotEquals,
			value:    "Terminating",
			want:     true,
		},
		{
			name:     "in operator",
			field:    "provider",
			operator: OperatorIn,
			value:    []interface{}{"aws", "gcp", "azure"},
			want:     true,
		},
		{
			name:     "greater than operator",
			field:    "nodeCount",
			operator: OperatorGreaterThan,
			value:    3,
			want:     true,
		},
		{
			name:     "less than operator",
			field:    "nodeCount",
			operator: OperatorLessThan,
			value:    10,
			want:     true,
		},
		{
			name:     "exists operator",
			field:    "provider",
			operator: OperatorExists,
			value:    nil,
			want:     true,
		},
		{
			name:     "nested field",
			field:    "status.phase",
			operator: OperatorEquals,
			value:    "Active",
			want:     true,
		},
		{
			name:      "nonexistent field",
			field:     "nonexistent",
			operator:  OperatorEquals,
			value:     "test",
			wantError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := evaluator.EvaluateCondition(tt.field, tt.operator, tt.value)
			if tt.wantError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.want, result)
			}
		})
	}
}

func TestEvaluatorEvaluateConditions(t *testing.T) {
	ctx := NewEvaluationContext()
	ctx.Set("clusterPhase", "Ready")
	ctx.Set("cloudProvider", "aws")
	ctx.Set("vpcId", "vpc-12345")

	evaluator := NewEvaluator(ctx)

	tests := []struct {
		name       string
		conditions []ConditionDef
		want       bool
		wantError  bool
	}{
		{
			name: "all conditions pass",
			conditions: []ConditionDef{
				{Field: "clusterPhase", Operator: "in", Value: []interface{}{"Provisioning", "Ready"}},
				{Field: "cloudProvider", Operator: "in", Value: []interface{}{"aws", "gcp", "azure"}},
				{Field: "vpcId", Operator: "exists", Value: nil},
			},
			want: true,
		},
		{
			name: "one condition fails",
			conditions: []ConditionDef{
				{Field: "clusterPhase", Operator: "equals", Value: "Ready"},
				{Field: "cloudProvider", Operator: "equals", Value: "gcp"}, // This fails
			},
			want: false,
		},
		{
			name:       "empty conditions",
			conditions: []ConditionDef{},
			want:       true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := evaluator.EvaluateConditions(tt.conditions)
			if tt.wantError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.want, result)
			}
		})
	}
}

func TestGetNestedField(t *testing.T) {
	data := map[string]interface{}{
		"level1": map[string]interface{}{
			"level2": map[string]interface{}{
				"level3": "value",
			},
			"array": []interface{}{1, 2, 3},
		},
		"simple": "test",
	}

	tests := []struct {
		name      string
		path      string
		want      interface{}
		wantError bool
	}{
		{
			name: "simple field",
			path: "simple",
			want: "test",
		},
		{
			name: "nested field level 2",
			path: "level1.level2",
			want: map[string]interface{}{"level3": "value"},
		},
		{
			name: "nested field level 3",
			path: "level1.level2.level3",
			want: "value",
		},
		{
			name: "array field",
			path: "level1.array",
			want: []interface{}{1, 2, 3},
		},
		{
			name:      "nonexistent field",
			path:      "level1.nonexistent",
			wantError: true,
		},
		{
			name:      "nonexistent top level",
			path:      "nonexistent",
			wantError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := getNestedField(data, tt.path)
			if tt.wantError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.want, result)
			}
		})
	}
}

func TestToFloat64(t *testing.T) {
	tests := []struct {
		name      string
		value     interface{}
		want      float64
		wantError bool
	}{
		{name: "float64", value: float64(3.14), want: 3.14},
		{name: "float32", value: float32(3.14), want: float64(float32(3.14))},
		{name: "int", value: int(42), want: 42.0},
		{name: "int8", value: int8(42), want: 42.0},
		{name: "int16", value: int16(42), want: 42.0},
		{name: "int32", value: int32(42), want: 42.0},
		{name: "int64", value: int64(42), want: 42.0},
		{name: "uint", value: uint(42), want: 42.0},
		{name: "uint8", value: uint8(42), want: 42.0},
		{name: "uint16", value: uint16(42), want: 42.0},
		{name: "uint32", value: uint32(42), want: 42.0},
		{name: "uint64", value: uint64(42), want: 42.0},
		{name: "string", value: "test", wantError: true},
		{name: "bool", value: true, wantError: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := toFloat64(tt.value)
			if tt.wantError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.want, result)
			}
		})
	}
}

func TestEvaluationError(t *testing.T) {
	err := &EvaluationError{
		Field:   "testField",
		Message: "test error",
	}
	assert.Contains(t, err.Error(), "testField")
	assert.Contains(t, err.Error(), "test error")

	err2 := &EvaluationError{
		Field:   "testField",
		Message: "test error",
		Err:     assert.AnError,
	}
	assert.Contains(t, err2.Error(), "testField")
	assert.Contains(t, err2.Error(), "test error")
	assert.Equal(t, assert.AnError, err2.Unwrap())
}

func TestNewEvaluatorWithNilContext(t *testing.T) {
	evaluator := NewEvaluator(nil)
	require.NotNil(t, evaluator)
	require.NotNil(t, evaluator.context)
}

func TestGetField(t *testing.T) {
	ctx := NewEvaluationContext()
	ctx.Set("cluster", map[string]interface{}{
		"metadata": map[string]interface{}{
			"name": "test-cluster",
		},
		"status": map[string]interface{}{
			"phase": "Ready",
		},
	})

	evaluator := NewEvaluator(ctx)

	// Get existing field
	value, err := evaluator.GetField("cluster.metadata.name")
	require.NoError(t, err)
	assert.Equal(t, "test-cluster", value)

	// Get nested field
	value, err = evaluator.GetField("cluster.status.phase")
	require.NoError(t, err)
	assert.Equal(t, "Ready", value)

	// Get non-existent field
	_, err = evaluator.GetField("cluster.nonexistent")
	assert.Error(t, err)
}

func TestGetFieldOrDefault(t *testing.T) {
	ctx := NewEvaluationContext()
	ctx.Set("cluster", map[string]interface{}{
		"metadata": map[string]interface{}{
			"name": "test-cluster",
		},
	})

	evaluator := NewEvaluator(ctx)

	// Get existing field
	value := evaluator.GetFieldOrDefault("cluster.metadata.name", "default")
	assert.Equal(t, "test-cluster", value)

	// Get non-existent field returns default
	value = evaluator.GetFieldOrDefault("cluster.nonexistent", "default-value")
	assert.Equal(t, "default-value", value)

	// Get deeply nested non-existent field returns default
	value = evaluator.GetFieldOrDefault("cluster.a.b.c.d", 42)
	assert.Equal(t, 42, value)
}

func TestEvaluateConditionWithResult(t *testing.T) {
	ctx := NewEvaluationContext()
	ctx.Set("status", "Ready")
	ctx.Set("replicas", 3)
	ctx.Set("provider", "aws")

	evaluator := NewEvaluator(ctx)

	// Test equals - matched
	result, err := evaluator.EvaluateConditionWithResult("status", OperatorEquals, "Ready")
	require.NoError(t, err)
	assert.True(t, result.Matched)
	assert.Equal(t, "Ready", result.FieldValue)
	assert.Equal(t, "status", result.Field)
	assert.Equal(t, OperatorEquals, result.Operator)
	assert.Equal(t, "Ready", result.ExpectedValue)

	// Test equals - not matched
	result, err = evaluator.EvaluateConditionWithResult("status", OperatorEquals, "Failed")
	require.NoError(t, err)
	assert.False(t, result.Matched)
	assert.Equal(t, "Ready", result.FieldValue) // Still returns the actual value

	// Test greaterThan
	result, err = evaluator.EvaluateConditionWithResult("replicas", OperatorGreaterThan, 2)
	require.NoError(t, err)
	assert.True(t, result.Matched)
	assert.Equal(t, 3, result.FieldValue)

	// Test in operator
	result, err = evaluator.EvaluateConditionWithResult("provider", OperatorIn, []string{"aws", "gcp", "azure"})
	require.NoError(t, err)
	assert.True(t, result.Matched)
	assert.Equal(t, "aws", result.FieldValue)
}

func TestEvaluateConditionsWithResult(t *testing.T) {
	ctx := NewEvaluationContext()
	ctx.Set("status", "Ready")
	ctx.Set("replicas", 3)
	ctx.Set("provider", "aws")

	evaluator := NewEvaluator(ctx)

	// All conditions pass
	conditions := []ConditionDef{
		{Field: "status", Operator: "equals", Value: "Ready"},
		{Field: "replicas", Operator: "greaterThan", Value: 1},
		{Field: "provider", Operator: "in", Value: []string{"aws", "gcp"}},
	}

	result, err := evaluator.EvaluateConditionsWithResult(conditions)
	require.NoError(t, err)
	assert.True(t, result.Matched)
	assert.Equal(t, -1, result.FailedCondition)
	assert.Len(t, result.Results, 3)
	assert.Len(t, result.ExtractedFields, 3)

	// Verify extracted fields
	assert.Equal(t, "Ready", result.ExtractedFields["status"])
	assert.Equal(t, 3, result.ExtractedFields["replicas"])
	assert.Equal(t, "aws", result.ExtractedFields["provider"])

	// One condition fails
	conditions = []ConditionDef{
		{Field: "status", Operator: "equals", Value: "Ready"},
		{Field: "replicas", Operator: "greaterThan", Value: 10}, // This will fail
		{Field: "provider", Operator: "equals", Value: "aws"},
	}

	result, err = evaluator.EvaluateConditionsWithResult(conditions)
	require.NoError(t, err)
	assert.False(t, result.Matched)
	assert.Equal(t, 1, result.FailedCondition) // Second condition (index 1) failed
	assert.Len(t, result.Results, 3)

	// Extracted fields are still populated even when conditions fail
	assert.Equal(t, "Ready", result.ExtractedFields["status"])
	assert.Equal(t, 3, result.ExtractedFields["replicas"])
}

func TestExtractFields(t *testing.T) {
	ctx := NewEvaluationContext()
	ctx.Set("cluster", map[string]interface{}{
		"metadata": map[string]interface{}{
			"name":      "test-cluster",
			"namespace": "default",
		},
		"status": map[string]interface{}{
			"phase": "Ready",
		},
	})

	evaluator := NewEvaluator(ctx)

	// Extract multiple fields
	fields := []string{"cluster.metadata.name", "cluster.metadata.namespace", "cluster.status.phase"}
	extracted, err := evaluator.ExtractFields(fields)
	require.NoError(t, err)
	assert.Len(t, extracted, 3)
	assert.Equal(t, "test-cluster", extracted["cluster.metadata.name"])
	assert.Equal(t, "default", extracted["cluster.metadata.namespace"])
	assert.Equal(t, "Ready", extracted["cluster.status.phase"])

	// Error on non-existent field
	fields = []string{"cluster.metadata.name", "cluster.nonexistent"}
	_, err = evaluator.ExtractFields(fields)
	assert.Error(t, err)
}

func TestExtractFieldsSafe(t *testing.T) {
	ctx := NewEvaluationContext()
	ctx.Set("cluster", map[string]interface{}{
		"metadata": map[string]interface{}{
			"name": "test-cluster",
		},
		"status": nil, // null value
	})

	evaluator := NewEvaluator(ctx)

	fields := []string{
		"cluster.metadata.name",  // exists
		"cluster.nonexistent",    // missing key
		"cluster.status.phase",   // null parent
		"missing.field",          // missing root
	}

	extracted := evaluator.ExtractFieldsSafe(fields)
	assert.Len(t, extracted, 4)
	assert.Equal(t, "test-cluster", extracted["cluster.metadata.name"]) // Actual value
	assert.Nil(t, extracted["cluster.nonexistent"])                     // Missing key -> nil
	assert.Nil(t, extracted["cluster.status.phase"])                    // Null parent -> nil
	assert.Nil(t, extracted["missing.field"])                           // Missing root -> nil
}

func TestExtractFieldsOrDefault(t *testing.T) {
	ctx := NewEvaluationContext()
	ctx.Set("cluster", map[string]interface{}{
		"metadata": map[string]interface{}{
			"name": "test-cluster",
		},
	})

	evaluator := NewEvaluator(ctx)

	fields := map[string]interface{}{
		"cluster.metadata.name": "default-name",
		"cluster.nonexistent":   "fallback-value",
		"missing.field":         42,
	}

	extracted := evaluator.ExtractFieldsOrDefault(fields)
	assert.Len(t, extracted, 3)
	assert.Equal(t, "test-cluster", extracted["cluster.metadata.name"]) // Actual value
	assert.Equal(t, "fallback-value", extracted["cluster.nonexistent"]) // Default
	assert.Equal(t, 42, extracted["missing.field"])                     // Default
}

func TestEvaluationResultStruct(t *testing.T) {
	result := EvaluationResult{
		Matched:       true,
		FieldValue:    "Ready",
		Field:         "status.phase",
		Operator:      OperatorEquals,
		ExpectedValue: "Ready",
	}

	assert.True(t, result.Matched)
	assert.Equal(t, "Ready", result.FieldValue)
	assert.Equal(t, "status.phase", result.Field)
	assert.Equal(t, OperatorEquals, result.Operator)
	assert.Equal(t, "Ready", result.ExpectedValue)
}

func TestNullHandling(t *testing.T) {
	ctx := NewEvaluationContext()
	ctx.Set("cluster", map[string]interface{}{
		"metadata": map[string]interface{}{
			"name": "test-cluster",
		},
		"status": nil, // null value
		"spec": map[string]interface{}{
			"provider": nil, // null nested value
			"nested": map[string]interface{}{
				"value": "exists",
			},
		},
	})

	evaluator := NewEvaluator(ctx)

	t.Run("access field on null parent", func(t *testing.T) {
		// Accessing cluster.status.phase when status is null
		_, err := evaluator.GetField("cluster.status.phase")
		assert.Error(t, err)
		assert.True(t, IsFieldNotFound(err))
	})

	t.Run("safe get on null path returns nil", func(t *testing.T) {
		value := evaluator.GetFieldSafe("cluster.status.phase")
		assert.Nil(t, value)
	})

	t.Run("get with default on null path", func(t *testing.T) {
		value := evaluator.GetFieldOrDefault("cluster.status.phase", "Unknown")
		assert.Equal(t, "Unknown", value)
	})

	t.Run("has field returns false for null", func(t *testing.T) {
		assert.False(t, evaluator.HasField("cluster.status"))
		assert.False(t, evaluator.HasField("cluster.status.phase"))
		assert.False(t, evaluator.HasField("cluster.spec.provider"))
	})

	t.Run("has field returns true for existing", func(t *testing.T) {
		assert.True(t, evaluator.HasField("cluster.metadata.name"))
		assert.True(t, evaluator.HasField("cluster.spec.nested.value"))
	})

	t.Run("safe evaluate returns false for null path", func(t *testing.T) {
		result := evaluator.EvaluateConditionSafe("cluster.status.phase", OperatorEquals, "Ready")
		assert.False(t, result)
	})

	t.Run("exists operator on null returns false", func(t *testing.T) {
		result := evaluator.EvaluateConditionSafe("cluster.status", OperatorExists, true)
		assert.False(t, result)
	})

	t.Run("existing field still works", func(t *testing.T) {
		result := evaluator.EvaluateConditionSafe("cluster.metadata.name", OperatorEquals, "test-cluster")
		assert.True(t, result)
	})
}

func TestDeepNullPath(t *testing.T) {
	ctx := NewEvaluationContext()
	ctx.Set("a", map[string]interface{}{
		"b": map[string]interface{}{
			"c": nil, // null at c
		},
	})

	evaluator := NewEvaluator(ctx)

	// a.b.c is null, so a.b.c.d.e.f should fail gracefully
	_, err := evaluator.GetField("a.b.c.d.e.f")
	assert.Error(t, err)
	assert.True(t, IsFieldNotFound(err))

	// Safe version returns nil
	value := evaluator.GetFieldSafe("a.b.c.d.e.f")
	assert.Nil(t, value)

	// Default version returns default
	value = evaluator.GetFieldOrDefault("a.b.c.d.e.f", "fallback")
	assert.Equal(t, "fallback", value)

	// HasField returns false
	assert.False(t, evaluator.HasField("a.b.c.d.e.f"))
	assert.False(t, evaluator.HasField("a.b.c"))
	assert.True(t, evaluator.HasField("a.b"))
}

func TestFieldNotFoundError(t *testing.T) {
	err := &FieldNotFoundError{
		Path:    "cluster.status.phase",
		Field:   "phase",
		Message: "field 'phase' not found at path 'cluster.status.phase'",
	}

	assert.Equal(t, "field 'phase' not found at path 'cluster.status.phase'", err.Error())
	assert.True(t, IsFieldNotFound(err))
	assert.False(t, IsFieldNotFound(fmt.Errorf("some other error")))
}

