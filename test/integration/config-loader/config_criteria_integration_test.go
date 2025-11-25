//go:build integration

package config_loader_integration

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/openshift-hyperfleet/hyperfleet-adapter/internal/config_loader"
	"github.com/openshift-hyperfleet/hyperfleet-adapter/internal/criteria"
)

// getConfigPath returns the path to the adapter config template.
// It first checks the ADAPTER_CONFIG_PATH environment variable, then falls back
// to resolving the path relative to the project root.
func getConfigPath() string {
	if envPath := os.Getenv("ADAPTER_CONFIG_PATH"); envPath != "" {
		return envPath
	}
	return filepath.Join(getProjectRoot(), "configs/adapter-config-template.yaml")
}

// TestConfigLoadAndCriteriaEvaluation tests loading config and evaluating preconditions
func TestConfigLoadAndCriteriaEvaluation(t *testing.T) {
	// Load actual config template using robust path resolution
	configPath := getConfigPath()
	config, err := config_loader.Load(configPath)
	require.NoError(t, err, "should load config template from %s", configPath)
	require.NotNil(t, config)

	// Create evaluation context with simulated runtime data
	ctx := criteria.NewEvaluationContext()

	// Simulate data extracted from HyperFleet API response
	ctx.Set("clusterPhase", "Ready")
	ctx.Set("cloudProvider", "aws")
	ctx.Set("vpcId", "vpc-12345")
	ctx.Set("region", "us-east-1")
	ctx.Set("clusterName", "test-cluster")
	ctx.Set("nodeCount", 3)

	// Simulate cluster details response
	ctx.Set("clusterDetails", map[string]interface{}{
		"metadata": map[string]interface{}{
			"name": "test-cluster",
		},
		"spec": map[string]interface{}{
			"provider":   "aws",
			"region":     "us-east-1",
			"vpc_id":     "vpc-12345",
			"node_count": 3,
		},
		"status": map[string]interface{}{
			"phase": "Ready",
		},
	})

	evaluator := criteria.NewEvaluator(ctx)

	t.Run("evaluate precondition conditions from config", func(t *testing.T) {
		// Find the clusterStatus precondition
		precond := config.GetPreconditionByName("clusterStatus")
		require.NotNil(t, precond, "clusterStatus precondition should exist")

		// Evaluate each condition from the config
		for i, cond := range precond.Conditions {
			t.Logf("Evaluating condition %d: %s %s %v", i, cond.Field, cond.Operator, cond.Value)

			result, err := evaluator.EvaluateConditionWithResult(
				cond.Field,
				criteria.Operator(cond.Operator),
				cond.Value,
			)
			require.NoError(t, err, "condition %d should evaluate without error", i)
			assert.True(t, result.Matched, "condition %d should match: %s %s %v (got field value: %v)",
				i, cond.Field, cond.Operator, cond.Value, result.FieldValue)
		}
	})

	t.Run("evaluate all preconditions as combined expression", func(t *testing.T) {
		precond := config.GetPreconditionByName("clusterStatus")
		require.NotNil(t, precond)

		// Convert conditions to ConditionDef slice
		conditions := make([]criteria.ConditionDef, len(precond.Conditions))
		for i, cond := range precond.Conditions {
			conditions[i] = criteria.ConditionDef{
				Field:    cond.Field,
				Operator: criteria.Operator(cond.Operator),
				Value:    cond.Value,
			}
		}

		// Evaluate all conditions together
		result, err := evaluator.EvaluateConditionsWithResult(conditions)
		require.NoError(t, err)
		assert.True(t, result.Matched, "all preconditions should pass")
		assert.Equal(t, -1, result.FailedCondition, "no condition should fail")

		// Verify extracted fields
		assert.NotEmpty(t, result.ExtractedFields)
		t.Logf("Extracted fields: %v", result.ExtractedFields)
	})
}

// TestConfigConditionsToCEL tests converting config conditions to CEL expressions
func TestConfigConditionsToCEL(t *testing.T) {
	configPath := getConfigPath()
	config, err := config_loader.Load(configPath)
	require.NoError(t, err)

	precond := config.GetPreconditionByName("clusterStatus")
	require.NotNil(t, precond)

	t.Run("convert conditions to CEL", func(t *testing.T) {
		conditions := make([]criteria.ConditionDef, len(precond.Conditions))
		for i, cond := range precond.Conditions {
			conditions[i] = criteria.ConditionDef{
				Field:    cond.Field,
				Operator: criteria.Operator(cond.Operator),
				Value:    cond.Value,
			}
		}

		// Convert to CEL
		celExpr, err := criteria.ConditionsToCEL(conditions)
		require.NoError(t, err)
		assert.NotEmpty(t, celExpr)
		t.Logf("Generated CEL expression: %s", celExpr)

		// Verify each individual condition converts to CEL
		for i, cond := range precond.Conditions {
			expr, err := criteria.ConditionToCEL(cond.Field, cond.Operator, cond.Value)
			require.NoError(t, err, "condition %d should convert to CEL", i)
			t.Logf("Condition %d CEL: %s", i, expr)
		}
	})

	t.Run("evaluate converted CEL expression", func(t *testing.T) {
		ctx := criteria.NewEvaluationContext()
		ctx.Set("clusterPhase", "Ready")
		ctx.Set("cloudProvider", "aws")
		ctx.Set("vpcId", "vpc-12345")

		evaluator := criteria.NewEvaluator(ctx)

		conditions := make([]criteria.ConditionDef, len(precond.Conditions))
		for i, cond := range precond.Conditions {
			conditions[i] = criteria.ConditionDef{
				Field:    cond.Field,
				Operator: criteria.Operator(cond.Operator),
				Value:    cond.Value,
			}
		}

		// Evaluate as CEL
		result, err := evaluator.EvaluateConditionsAsCEL(conditions)
		require.NoError(t, err)
		assert.True(t, result.Matched)
	})
}

// TestConfigWithFailingPreconditions tests behavior when preconditions fail
func TestConfigWithFailingPreconditions(t *testing.T) {
	configPath := getConfigPath()
	config, err := config_loader.Load(configPath)
	require.NoError(t, err)

	precond := config.GetPreconditionByName("clusterStatus")
	require.NotNil(t, precond)

	t.Run("preconditions fail with wrong phase", func(t *testing.T) {
		ctx := criteria.NewEvaluationContext()
		ctx.Set("clusterPhase", "Terminating") // Not in allowed list
		ctx.Set("cloudProvider", "aws")
		ctx.Set("vpcId", "vpc-12345")

		evaluator := criteria.NewEvaluator(ctx)

		conditions := make([]criteria.ConditionDef, len(precond.Conditions))
		for i, cond := range precond.Conditions {
			conditions[i] = criteria.ConditionDef{
				Field:    cond.Field,
				Operator: criteria.Operator(cond.Operator),
				Value:    cond.Value,
			}
		}

		result, err := evaluator.EvaluateConditionsWithResult(conditions)
		require.NoError(t, err)
		assert.False(t, result.Matched, "preconditions should fail with wrong phase")
		assert.Equal(t, 0, result.FailedCondition, "first condition (clusterPhase) should fail")
	})

	t.Run("preconditions fail with wrong provider", func(t *testing.T) {
		ctx := criteria.NewEvaluationContext()
		ctx.Set("clusterPhase", "Ready")
		ctx.Set("cloudProvider", "onprem") // Not in allowed list
		ctx.Set("vpcId", "vpc-12345")

		evaluator := criteria.NewEvaluator(ctx)

		conditions := make([]criteria.ConditionDef, len(precond.Conditions))
		for i, cond := range precond.Conditions {
			conditions[i] = criteria.ConditionDef{
				Field:    cond.Field,
				Operator: criteria.Operator(cond.Operator),
				Value:    cond.Value,
			}
		}

		result, err := evaluator.EvaluateConditionsWithResult(conditions)
		require.NoError(t, err)
		assert.False(t, result.Matched, "preconditions should fail with wrong provider")
	})

	t.Run("preconditions fail with missing vpcId", func(t *testing.T) {
		ctx := criteria.NewEvaluationContext()
		ctx.Set("clusterPhase", "Ready")
		ctx.Set("cloudProvider", "aws")
		// vpcId not set - should fail exists check

		evaluator := criteria.NewEvaluator(ctx)

		// Just check the vpcId exists condition
		result := evaluator.EvaluateConditionSafe("vpcId", criteria.OperatorExists, true)
		assert.False(t, result, "exists check should fail when field is missing")
	})
}

// TestConfigResourceDiscoveryFields tests extracting discovery fields from config
func TestConfigResourceDiscoveryFields(t *testing.T) {
	configPath := getConfigPath()
	config, err := config_loader.Load(configPath)
	require.NoError(t, err)

	t.Run("verify resource discovery configs", func(t *testing.T) {
		for _, resource := range config.Spec.Resources {
			t.Logf("Resource: %s", resource.Name)

			if resource.Discovery != nil {
				t.Logf("  Discovery namespace: %s", resource.Discovery.Namespace)
				t.Logf("  Discovery byName: %s", resource.Discovery.ByName)
				if resource.Discovery.BySelectors != nil {
					t.Logf("  Discovery selectors: %v", resource.Discovery.BySelectors.LabelSelector)
				}

				// Verify discovery config has at least one method
				hasDiscoveryMethod := resource.Discovery.ByName != "" ||
					(resource.Discovery.BySelectors != nil && len(resource.Discovery.BySelectors.LabelSelector) > 0)
				assert.True(t, hasDiscoveryMethod, "resource %s should have discovery method", resource.Name)
			}
		}
	})
}

// TestConfigPostProcessingEvaluation tests evaluating post-processing conditions
func TestConfigPostProcessingEvaluation(t *testing.T) {
	configPath := getConfigPath()
	config, err := config_loader.Load(configPath)
	require.NoError(t, err)

	require.NotNil(t, config.Spec.Post, "config should have post processing")

	t.Run("simulate post-processing with k8s resource data", func(t *testing.T) {
		ctx := criteria.NewEvaluationContext()

		// Simulate K8s resources returned from discovery
		ctx.Set("resources", map[string]interface{}{
			"clusterNamespace": map[string]interface{}{
				"apiVersion": "v1",
				"kind":       "Namespace",
				"metadata": map[string]interface{}{
					"name": "cluster-abc123",
				},
				"status": map[string]interface{}{
					"phase": "Active",
				},
			},
			"clusterController": map[string]interface{}{
				"apiVersion": "apps/v1",
				"kind":       "Deployment",
				"metadata": map[string]interface{}{
					"name":      "cluster-controller",
					"namespace": "cluster-abc123",
				},
				"status": map[string]interface{}{
					"replicas":          3,
					"readyReplicas":     3,
					"availableReplicas": 3,
					"conditions": []interface{}{
						map[string]interface{}{
							"type":   "Available",
							"status": "True",
							"reason": "MinimumReplicasAvailable",
						},
					},
				},
			},
			"clusterConfigMap": map[string]interface{}{
				"apiVersion": "v1",
				"kind":       "ConfigMap",
				"metadata": map[string]interface{}{
					"name":      "cluster-config",
					"namespace": "cluster-abc123",
				},
			},
		})

		evaluator := criteria.NewEvaluator(ctx)

		// Test accessing nested K8s resource data
		t.Run("access namespace status", func(t *testing.T) {
			value, err := evaluator.GetField("resources.clusterNamespace.status.phase")
			require.NoError(t, err)
			assert.Equal(t, "Active", value)
		})

		t.Run("access deployment replicas", func(t *testing.T) {
			value, err := evaluator.GetField("resources.clusterController.status.readyReplicas")
			require.NoError(t, err)
			assert.Equal(t, 3, value)
		})

		t.Run("evaluate deployment ready condition", func(t *testing.T) {
			// Check if ready replicas > 0
			result, err := evaluator.EvaluateConditionWithResult(
				"resources.clusterController.status.readyReplicas",
				criteria.OperatorGreaterThan,
				0,
			)
			require.NoError(t, err)
			assert.True(t, result.Matched)
			assert.Equal(t, 3, result.FieldValue)
		})

		t.Run("evaluate replicas match", func(t *testing.T) {
			// Check replicas == readyReplicas
			replicas, _ := evaluator.GetField("resources.clusterController.status.replicas")
			readyReplicas, _ := evaluator.GetField("resources.clusterController.status.readyReplicas")
			assert.Equal(t, replicas, readyReplicas)
		})

		t.Run("evaluate with CEL expression", func(t *testing.T) {
			result, err := evaluator.EvaluateCEL(`resources.clusterController.status.readyReplicas > 0`)
			require.NoError(t, err)
			assert.True(t, result.Matched)
		})
	})
}

// TestConfigNullSafetyWithMissingResources tests null safety when resources are missing
func TestConfigNullSafetyWithMissingResources(t *testing.T) {
	configPath := getConfigPath()
	config, err := config_loader.Load(configPath)
	require.NoError(t, err)
	require.NotNil(t, config)

	t.Run("handle missing resource gracefully", func(t *testing.T) {
		ctx := criteria.NewEvaluationContext()

		// Partially populated resources (simulating some not yet created)
		ctx.Set("resources", map[string]interface{}{
			"clusterNamespace": map[string]interface{}{
				"status": map[string]interface{}{
					"phase": "Active",
				},
			},
			"clusterController": nil, // Not created yet
		})

		evaluator := criteria.NewEvaluator(ctx)

		// Safe access to missing resource
		value := evaluator.GetFieldSafe("resources.clusterController.status.readyReplicas")
		assert.Nil(t, value, "should return nil for null resource")

		// Default value for missing resource
		value = evaluator.GetFieldOrDefault("resources.clusterController.status.readyReplicas", 0)
		assert.Equal(t, 0, value, "should return default for null resource")

		// HasField should return false
		assert.False(t, evaluator.HasField("resources.clusterController.status"))

		// Safe evaluation should return false
		result := evaluator.EvaluateConditionSafe(
			"resources.clusterController.status.readyReplicas",
			criteria.OperatorGreaterThan,
			0,
		)
		assert.False(t, result, "should safely return false for null path")
	})

	t.Run("handle deeply nested null", func(t *testing.T) {
		ctx := criteria.NewEvaluationContext()
		ctx.Set("resources", map[string]interface{}{
			"clusterController": map[string]interface{}{
				"status": nil, // Status is null
			},
		})

		evaluator := criteria.NewEvaluator(ctx)

		// Should handle null status gracefully
		value := evaluator.GetFieldOrDefault("resources.clusterController.status.readyReplicas", -1)
		assert.Equal(t, -1, value)
	})
}

// TestConfigParameterExtraction tests parameter definitions from config
func TestConfigParameterExtraction(t *testing.T) {
	configPath := getConfigPath()
	config, err := config_loader.Load(configPath)
	require.NoError(t, err)

	t.Run("verify required parameters", func(t *testing.T) {
		requiredParams := config.GetRequiredParams()
		assert.NotEmpty(t, requiredParams, "should have required parameters")

		// Check expected required params exist
		requiredNames := make(map[string]bool)
		for _, p := range requiredParams {
			requiredNames[p.Name] = true
			t.Logf("Required param: %s (source: %s)", p.Name, p.Source)
		}

		assert.True(t, requiredNames["hyperfleetApiBaseUrl"], "hyperfleetApiBaseUrl should be required")
		assert.True(t, requiredNames["clusterId"], "clusterId should be required")
	})

	t.Run("verify parameter sources", func(t *testing.T) {
		for _, param := range config.Spec.Params {
			if param.Source != "" {
				// Check source format
				assert.True(t,
					strings.HasPrefix(param.Source, "env.") || strings.HasPrefix(param.Source, "event."),
					"param %s source should start with env. or event., got: %s", param.Name, param.Source)
			}
		}
	})
}

