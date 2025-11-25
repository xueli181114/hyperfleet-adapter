package config_loader

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func TestLoad(t *testing.T) {
	// Create a temporary config file
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "adapter-config.yaml")

	configYAML := `
apiVersion: hyperfleet.redhat.com/v1alpha1
kind: AdapterConfig
metadata:
  name: test-adapter
  namespace: hyperfleet-system
  labels:
    hyperfleet.io/adapter-type: test
spec:
  adapter:
    version: "0.0.1"
  hyperfleetApi:
    timeout: 2s
    retryAttempts: 3
    retryBackoff: exponential
  kubernetes:
    apiVersion: "v1"
  params:
    - name: "clusterId"
      source: "event.cluster_id"
      type: "string"
      required: true
  preconditions:
    - name: "clusterStatus"
      apiCall:
        method: "GET"
        url: "https://api.example.com/clusters/{{ .clusterId }}"
      storeResponseAs: "clusterDetails"
  resources:
    - name: "testNamespace"
      manifest:
        apiVersion: v1
        kind: Namespace
        metadata:
          name: "test-ns"
      discovery:
        namespace: "*"
        byName: "test-ns"
`

	err := os.WriteFile(configPath, []byte(configYAML), 0644)
	require.NoError(t, err)

	// Test loading
	config, err := Load(configPath)
	require.NoError(t, err)
	require.NotNil(t, config)

	// Verify basic fields
	assert.Equal(t, "hyperfleet.redhat.com/v1alpha1", config.APIVersion)
	assert.Equal(t, "AdapterConfig", config.Kind)
	assert.Equal(t, "test-adapter", config.Metadata.Name)
	assert.Equal(t, "hyperfleet-system", config.Metadata.Namespace)
	assert.Equal(t, "0.0.1", config.Spec.Adapter.Version)
}

func TestLoadInvalidPath(t *testing.T) {
	config, err := Load("/nonexistent/path/to/config.yaml")
	assert.Error(t, err)
	assert.Nil(t, config)
	assert.Contains(t, err.Error(), "failed to read config file")
}

func TestParse(t *testing.T) {
	tests := []struct {
		name      string
		yaml      string
		wantError bool
		errorMsg  string
	}{
		{
			name: "valid minimal config",
			yaml: `
apiVersion: hyperfleet.redhat.com/v1alpha1
kind: AdapterConfig
metadata:
  name: test-adapter
spec:
  adapter:
    version: "1.0.0"
  hyperfleetApi:
    timeout: 5s
  kubernetes:
    apiVersion: "v1"
`,
			wantError: false,
		},
		{
			name: "missing apiVersion",
			yaml: `
kind: AdapterConfig
metadata:
  name: test-adapter
spec:
  adapter:
    version: "1.0.0"
`,
			wantError: true,
			errorMsg:  "apiVersion is required",
		},
		{
			name: "missing kind",
			yaml: `
apiVersion: hyperfleet.redhat.com/v1alpha1
metadata:
  name: test-adapter
spec:
  adapter:
    version: "1.0.0"
`,
			wantError: true,
			errorMsg:  "kind is required",
		},
		{
			name: "missing metadata.name",
			yaml: `
apiVersion: hyperfleet.redhat.com/v1alpha1
kind: AdapterConfig
metadata:
  namespace: test
spec:
  adapter:
    version: "1.0.0"
`,
			wantError: true,
			errorMsg:  "metadata.name is required",
		},
		{
			name: "missing adapter.version",
			yaml: `
apiVersion: hyperfleet.redhat.com/v1alpha1
kind: AdapterConfig
metadata:
  name: test-adapter
spec:
  adapter: {}
`,
			wantError: true,
			errorMsg:  "spec.adapter.version is required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config, err := Parse([]byte(tt.yaml))
			if tt.wantError {
				assert.Error(t, err)
				if tt.errorMsg != "" {
					assert.Contains(t, err.Error(), tt.errorMsg)
				}
				assert.Nil(t, config)
			} else {
				assert.NoError(t, err)
				assert.NotNil(t, config)
			}
		})
	}
}

func TestValidateParameters(t *testing.T) {
	tests := []struct {
		name      string
		yaml      string
		wantError bool
		errorMsg  string
	}{
		{
			name: "valid parameter with source",
			yaml: `
apiVersion: hyperfleet.redhat.com/v1alpha1
kind: AdapterConfig
metadata:
  name: test-adapter
spec:
  adapter:
    version: "1.0.0"
  hyperfleetApi:
    timeout: 5s
  kubernetes:
    apiVersion: "v1"
  params:
    - name: "clusterId"
      source: "event.cluster_id"
      required: true
`,
			wantError: false,
		},
		{
			name: "parameter without name",
			yaml: `
apiVersion: hyperfleet.redhat.com/v1alpha1
kind: AdapterConfig
metadata:
  name: test-adapter
spec:
  adapter:
    version: "1.0.0"
  hyperfleetApi:
    timeout: 5s
  kubernetes:
    apiVersion: "v1"
  params:
    - source: "event.cluster_id"
`,
			wantError: true,
			errorMsg:  "spec.params[0].name is required",
		},
		{
			name: "parameter without source or build",
			yaml: `
apiVersion: hyperfleet.redhat.com/v1alpha1
kind: AdapterConfig
metadata:
  name: test-adapter
spec:
  adapter:
    version: "1.0.0"
  hyperfleetApi:
    timeout: 5s
  kubernetes:
    apiVersion: "v1"
  params:
    - name: "clusterId"
      required: true
`,
			wantError: true,
			errorMsg:  "must specify source, build, buildRef, or fetchExternalResource",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config, err := Parse([]byte(tt.yaml))
			if tt.wantError {
				assert.Error(t, err)
				if tt.errorMsg != "" {
					assert.Contains(t, err.Error(), tt.errorMsg)
				}
			} else {
				assert.NoError(t, err)
				assert.NotNil(t, config)
			}
		})
	}
}

func TestValidatePreconditions(t *testing.T) {
	tests := []struct {
		name      string
		yaml      string
		wantError bool
		errorMsg  string
	}{
		{
			name: "valid precondition with API call",
			yaml: `
apiVersion: hyperfleet.redhat.com/v1alpha1
kind: AdapterConfig
metadata:
  name: test-adapter
spec:
  adapter:
    version: "1.0.0"
  hyperfleetApi:
    timeout: 5s
  kubernetes:
    apiVersion: "v1"
  preconditions:
    - name: "checkCluster"
      apiCall:
        method: "GET"
        url: "https://api.example.com/clusters"
`,
			wantError: false,
		},
		{
			name: "precondition without name",
			yaml: `
apiVersion: hyperfleet.redhat.com/v1alpha1
kind: AdapterConfig
metadata:
  name: test-adapter
spec:
  adapter:
    version: "1.0.0"
  hyperfleetApi:
    timeout: 5s
  kubernetes:
    apiVersion: "v1"
  preconditions:
    - apiCall:
        method: "GET"
        url: "https://api.example.com/clusters"
`,
			wantError: true,
			errorMsg:  "spec.preconditions[0].name is required",
		},
		{
			name: "precondition without apiCall or expression",
			yaml: `
apiVersion: hyperfleet.redhat.com/v1alpha1
kind: AdapterConfig
metadata:
  name: test-adapter
spec:
  adapter:
    version: "1.0.0"
  hyperfleetApi:
    timeout: 5s
  kubernetes:
    apiVersion: "v1"
  preconditions:
    - name: "checkCluster"
`,
			wantError: true,
			errorMsg:  "must specify apiCall, expression, or conditions",
		},
		{
			name: "API call without method",
			yaml: `
apiVersion: hyperfleet.redhat.com/v1alpha1
kind: AdapterConfig
metadata:
  name: test-adapter
spec:
  adapter:
    version: "1.0.0"
  hyperfleetApi:
    timeout: 5s
  kubernetes:
    apiVersion: "v1"
  preconditions:
    - name: "checkCluster"
      apiCall:
        url: "https://api.example.com/clusters"
`,
			wantError: true,
			errorMsg:  "method is required",
		},
		{
			name: "API call with invalid method",
			yaml: `
apiVersion: hyperfleet.redhat.com/v1alpha1
kind: AdapterConfig
metadata:
  name: test-adapter
spec:
  adapter:
    version: "1.0.0"
  hyperfleetApi:
    timeout: 5s
  kubernetes:
    apiVersion: "v1"
  preconditions:
    - name: "checkCluster"
      apiCall:
        method: "INVALID"
        url: "https://api.example.com/clusters"
`,
			wantError: true,
			errorMsg:  "is invalid (allowed:",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config, err := Parse([]byte(tt.yaml))
			if tt.wantError {
				assert.Error(t, err)
				if tt.errorMsg != "" {
					assert.Contains(t, err.Error(), tt.errorMsg)
				}
			} else {
				assert.NoError(t, err)
				assert.NotNil(t, config)
			}
		})
	}
}

func TestValidateResources(t *testing.T) {
	tests := []struct {
		name      string
		yaml      string
		wantError bool
		errorMsg  string
	}{
		{
			name: "valid resource with manifest",
			yaml: `
apiVersion: hyperfleet.redhat.com/v1alpha1
kind: AdapterConfig
metadata:
  name: test-adapter
spec:
  adapter:
    version: "1.0.0"
  hyperfleetApi:
    timeout: 5s
  kubernetes:
    apiVersion: "v1"
  resources:
    - name: "testNamespace"
      manifest:
        apiVersion: v1
        kind: Namespace
        metadata:
          name: "test-ns"
      discovery:
        namespace: "*"
        byName: "test-ns"
`,
			wantError: false,
		},
		{
			name: "resource without name",
			yaml: `
apiVersion: hyperfleet.redhat.com/v1alpha1
kind: AdapterConfig
metadata:
  name: test-adapter
spec:
  adapter:
    version: "1.0.0"
  hyperfleetApi:
    timeout: 5s
  kubernetes:
    apiVersion: "v1"
  resources:
    - manifest:
        apiVersion: v1
        kind: Namespace
`,
			wantError: true,
			errorMsg:  "spec.resources[0].name is required",
		},
		{
			name: "resource without manifest",
			yaml: `
apiVersion: hyperfleet.redhat.com/v1alpha1
kind: AdapterConfig
metadata:
  name: test-adapter
spec:
  adapter:
    version: "1.0.0"
  hyperfleetApi:
    timeout: 5s
  kubernetes:
    apiVersion: "v1"
  resources:
    - name: "testNamespace"
`,
			wantError: true,
			errorMsg:  "manifest is required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config, err := Parse([]byte(tt.yaml))
			if tt.wantError {
				assert.Error(t, err)
				if tt.errorMsg != "" {
					assert.Contains(t, err.Error(), tt.errorMsg)
				}
			} else {
				assert.NoError(t, err)
				assert.NotNil(t, config)
			}
		})
	}
}

func TestGetRequiredParams(t *testing.T) {
	yaml := `
apiVersion: hyperfleet.redhat.com/v1alpha1
kind: AdapterConfig
metadata:
  name: test-adapter
spec:
  adapter:
    version: "1.0.0"
  hyperfleetApi:
    timeout: 5s
  kubernetes:
    apiVersion: "v1"
  params:
    - name: "clusterId"
      source: "event.cluster_id"
      required: true
    - name: "optional"
      source: "event.optional"
      required: false
    - name: "resourceId"
      source: "event.resource_id"
      required: true
`

	config, err := Parse([]byte(yaml))
	require.NoError(t, err)

	requiredParams := config.GetRequiredParams()
	assert.Len(t, requiredParams, 2)
	assert.Equal(t, "clusterId", requiredParams[0].Name)
	assert.Equal(t, "resourceId", requiredParams[1].Name)
}

func TestGetResourceByName(t *testing.T) {
	yaml := `
apiVersion: hyperfleet.redhat.com/v1alpha1
kind: AdapterConfig
metadata:
  name: test-adapter
spec:
  adapter:
    version: "1.0.0"
  hyperfleetApi:
    timeout: 5s
  kubernetes:
    apiVersion: "v1"
  resources:
    - name: "namespace1"
      manifest:
        apiVersion: v1
        kind: Namespace
        metadata:
          name: "ns1"
      discovery:
        namespace: "*"
        byName: "ns1"
    - name: "namespace2"
      manifest:
        apiVersion: v1
        kind: Namespace
        metadata:
          name: "ns2"
      discovery:
        namespace: "*"
        byName: "ns2"
`

	config, err := Parse([]byte(yaml))
	require.NoError(t, err)

	resource := config.GetResourceByName("namespace1")
	assert.NotNil(t, resource)
	assert.Equal(t, "namespace1", resource.Name)

	resource = config.GetResourceByName("nonexistent")
	assert.Nil(t, resource)
}

func TestGetPreconditionByName(t *testing.T) {
	yaml := `
apiVersion: hyperfleet.redhat.com/v1alpha1
kind: AdapterConfig
metadata:
  name: test-adapter
spec:
  adapter:
    version: "1.0.0"
  hyperfleetApi:
    timeout: 5s
  kubernetes:
    apiVersion: "v1"
  preconditions:
    - name: "precond1"
      apiCall:
        method: "GET"
        url: "https://api.example.com/check1"
    - name: "precond2"
      apiCall:
        method: "GET"
        url: "https://api.example.com/check2"
`

	config, err := Parse([]byte(yaml))
	require.NoError(t, err)

	precond := config.GetPreconditionByName("precond1")
	assert.NotNil(t, precond)
	assert.Equal(t, "precond1", precond.Name)

	precond = config.GetPreconditionByName("nonexistent")
	assert.Nil(t, precond)
}

func TestParseTimeout(t *testing.T) {
	config := &HyperfleetAPIConfig{
		Timeout: "5s",
	}

	duration, err := config.ParseTimeout()
	require.NoError(t, err)
	assert.Equal(t, "5s", duration.String())

	config.Timeout = "invalid"
	_, err = config.ParseTimeout()
	assert.Error(t, err)
}

func TestUnsupportedAPIVersion(t *testing.T) {
	yaml := `
apiVersion: hyperfleet.redhat.com/v2
kind: AdapterConfig
metadata:
  name: test-adapter
spec:
  adapter:
    version: "1.0.0"
`
	config, err := Parse([]byte(yaml))
	assert.Error(t, err)
	assert.Nil(t, config)
	assert.Contains(t, err.Error(), "unsupported apiVersion")
	assert.Contains(t, err.Error(), "hyperfleet.redhat.com/v2")
}

func TestInvalidKind(t *testing.T) {
	yaml := `
apiVersion: hyperfleet.redhat.com/v1alpha1
kind: WrongKind
metadata:
  name: test-adapter
spec:
  adapter:
    version: "1.0.0"
`
	config, err := Parse([]byte(yaml))
	assert.Error(t, err)
	assert.Nil(t, config)
	assert.Contains(t, err.Error(), "invalid kind")
	assert.Contains(t, err.Error(), "WrongKind")
	assert.Contains(t, err.Error(), "AdapterConfig")
}

func TestLoadWithVersion(t *testing.T) {
	// Create a temporary config file
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "adapter-config.yaml")

	configYAML := `
apiVersion: hyperfleet.redhat.com/v1alpha1
kind: AdapterConfig
metadata:
  name: test-adapter
spec:
  adapter:
    version: "1.2.3"
  hyperfleetApi:
    timeout: 2s
  kubernetes:
    apiVersion: "v1"
`
	err := os.WriteFile(configPath, []byte(configYAML), 0644)
	require.NoError(t, err)

	// Test loading with matching version
	config, err := LoadWithVersion(configPath, "1.2.3")
	require.NoError(t, err)
	require.NotNil(t, config)
	assert.Equal(t, "1.2.3", config.Spec.Adapter.Version)

	// Test loading with mismatched version
	config, err = LoadWithVersion(configPath, "2.0.0")
	assert.Error(t, err)
	assert.Nil(t, config)
	assert.Contains(t, err.Error(), "adapter version mismatch")
	assert.Contains(t, err.Error(), "1.2.3")
	assert.Contains(t, err.Error(), "2.0.0")

	// Test loading with empty expected version (skip validation)
	config, err = LoadWithVersion(configPath, "")
	require.NoError(t, err)
	require.NotNil(t, config)
}

func TestParseWithVersion(t *testing.T) {
	yaml := `
apiVersion: hyperfleet.redhat.com/v1alpha1
kind: AdapterConfig
metadata:
  name: test-adapter
spec:
  adapter:
    version: "0.5.0"
  hyperfleetApi:
    timeout: 2s
  kubernetes:
    apiVersion: "v1"
`
	// Test with matching version
	config, err := ParseWithVersion([]byte(yaml), "0.5.0")
	require.NoError(t, err)
	require.NotNil(t, config)

	// Test with mismatched version
	config, err = ParseWithVersion([]byte(yaml), "1.0.0")
	assert.Error(t, err)
	assert.Nil(t, config)
	assert.Contains(t, err.Error(), "adapter version mismatch")
}

func TestValidateAdapterVersion(t *testing.T) {
	config := &AdapterConfig{
		Spec: AdapterConfigSpec{
			Adapter: AdapterInfo{
				Version: "1.0.0",
			},
		},
	}

	// Matching version
	err := ValidateAdapterVersion(config, "1.0.0")
	assert.NoError(t, err)

	// Mismatched version
	err = ValidateAdapterVersion(config, "2.0.0")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "adapter version mismatch")

	// Empty expected version (skip validation)
	err = ValidateAdapterVersion(config, "")
	assert.NoError(t, err)
}

func TestIsSupportedAPIVersion(t *testing.T) {
	// Supported version
	assert.True(t, IsSupportedAPIVersion("hyperfleet.redhat.com/v1alpha1"))

	// Unsupported versions
	assert.False(t, IsSupportedAPIVersion("hyperfleet.redhat.com/v1"))
	assert.False(t, IsSupportedAPIVersion("hyperfleet.redhat.com/v2"))
	assert.False(t, IsSupportedAPIVersion("other.io/v1alpha1"))
	assert.False(t, IsSupportedAPIVersion(""))
}

func TestSupportedAPIVersions(t *testing.T) {
	// Verify the constant is in the supported list
	assert.Contains(t, SupportedAPIVersions, APIVersionV1Alpha1)
	assert.Equal(t, "hyperfleet.redhat.com/v1alpha1", APIVersionV1Alpha1)
}

func TestValidateFileReferences(t *testing.T) {
	// Create temporary directory with test files
	tmpDir := t.TempDir()

	// Create a test template file
	templatePath := filepath.Join(tmpDir, "templates")
	require.NoError(t, os.MkdirAll(templatePath, 0755))
	templateFile := filepath.Join(templatePath, "test-template.yaml")
	require.NoError(t, os.WriteFile(templateFile, []byte("test: value"), 0644))

	tests := []struct {
		name      string
		config    *AdapterConfig
		baseDir   string
		wantErr   bool
		errMsg    string
	}{
		{
			name: "valid buildRef",
			config: &AdapterConfig{
				Spec: AdapterConfigSpec{
					Params: []Parameter{
						{Name: "test", BuildRef: "templates/test-template.yaml"},
					},
				},
			},
			baseDir: tmpDir,
			wantErr: false,
		},
		{
			name: "invalid buildRef - file not found",
			config: &AdapterConfig{
				Spec: AdapterConfigSpec{
					Params: []Parameter{
						{Name: "test", BuildRef: "templates/nonexistent.yaml"},
					},
				},
			},
			baseDir: tmpDir,
			wantErr: true,
			errMsg:  "does not exist",
		},
		{
			name: "invalid buildRef - is a directory",
			config: &AdapterConfig{
				Spec: AdapterConfigSpec{
					Params: []Parameter{
						{Name: "test", BuildRef: "templates"},
					},
				},
			},
			baseDir: tmpDir,
			wantErr: true,
			errMsg:  "is a directory",
		},
		{
			name: "valid manifest.ref",
			config: &AdapterConfig{
				Spec: AdapterConfigSpec{
					Resources: []Resource{
						{
							Name: "test",
							Manifest: map[string]interface{}{
								"ref": "templates/test-template.yaml",
							},
						},
					},
				},
			},
			baseDir: tmpDir,
			wantErr: false,
		},
		{
			name: "invalid manifest.ref - file not found",
			config: &AdapterConfig{
				Spec: AdapterConfigSpec{
					Resources: []Resource{
						{
							Name: "test",
							Manifest: map[string]interface{}{
								"ref": "templates/nonexistent.yaml",
							},
						},
					},
				},
			},
			baseDir: tmpDir,
			wantErr: true,
			errMsg:  "does not exist",
		},
		{
			name: "valid post.params buildRef",
			config: &AdapterConfig{
				Spec: AdapterConfigSpec{
					Post: &PostConfig{
						Params: []Parameter{
							{Name: "test", BuildRef: "templates/test-template.yaml"},
						},
					},
				},
			},
			baseDir: tmpDir,
			wantErr: false,
		},
		{
			name: "no file references - should pass",
			config: &AdapterConfig{
				Spec: AdapterConfigSpec{
					Params: []Parameter{
						{Name: "test", Source: "event.test"},
					},
				},
			},
			baseDir: tmpDir,
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateFileReferences(tt.config, tt.baseDir)
			if tt.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errMsg)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestLoadWithFileReferences(t *testing.T) {
	// Create temporary directory
	tmpDir := t.TempDir()

	// Create a template file
	templateDir := filepath.Join(tmpDir, "templates")
	require.NoError(t, os.MkdirAll(templateDir, 0755))
	templateFile := filepath.Join(templateDir, "status-payload.yaml")
	require.NoError(t, os.WriteFile(templateFile, []byte(`
status: "{{ .status }}"
`), 0644))

	// Create config file with buildRef
	configYAML := `
apiVersion: hyperfleet.redhat.com/v1alpha1
kind: AdapterConfig
metadata:
  name: test-adapter
spec:
  adapter:
    version: "0.0.1"
  hyperfleetApi:
    timeout: 2s
  kubernetes:
    apiVersion: "v1"
  params:
    - name: "clusterId"
      source: "event.cluster_id"
  resources:
    - name: "testNamespace"
      manifest:
        apiVersion: v1
        kind: Namespace
        metadata:
          name: test
      discovery:
        namespace: "*"
        byName: "test"
  post:
    params:
      - name: "statusPayload"
        buildRef: "templates/status-payload.yaml"
`
	configPath := filepath.Join(tmpDir, "config.yaml")
	require.NoError(t, os.WriteFile(configPath, []byte(configYAML), 0644))

	// Load should succeed because template file exists
	config, err := Load(configPath, WithSkipSemanticValidation())
	require.NoError(t, err)
	require.NotNil(t, config)
	assert.Equal(t, "test-adapter", config.Metadata.Name)

	// Now test with non-existent buildRef
	configYAMLBad := `
apiVersion: hyperfleet.redhat.com/v1alpha1
kind: AdapterConfig
metadata:
  name: test-adapter
spec:
  adapter:
    version: "0.0.1"
  hyperfleetApi:
    timeout: 2s
  kubernetes:
    apiVersion: "v1"
  params:
    - name: "clusterId"
      source: "event.cluster_id"
  resources:
    - name: "testNamespace"
      manifest:
        apiVersion: v1
        kind: Namespace
        metadata:
          name: test
      discovery:
        namespace: "*"
        byName: "test"
  post:
    params:
      - name: "statusPayload"
        buildRef: "templates/nonexistent.yaml"
`
	configPathBad := filepath.Join(tmpDir, "config-bad.yaml")
	require.NoError(t, os.WriteFile(configPathBad, []byte(configYAMLBad), 0644))

	// Load should fail because template file doesn't exist
	config, err = Load(configPathBad, WithSkipSemanticValidation())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "does not exist")
	assert.Nil(t, config)
}

func TestLoadFileReferencesContent(t *testing.T) {
	// Create temporary directory
	tmpDir := t.TempDir()
	templateDir := filepath.Join(tmpDir, "templates")
	require.NoError(t, os.MkdirAll(templateDir, 0755))

	// Create a buildRef template file
	buildRefFile := filepath.Join(templateDir, "status-payload.yaml")
	require.NoError(t, os.WriteFile(buildRefFile, []byte(`
status: "{{ .status }}"
message: "Operation completed"
`), 0644))

	// Create a manifest.ref template file
	manifestRefFile := filepath.Join(templateDir, "deployment.yaml")
	require.NoError(t, os.WriteFile(manifestRefFile, []byte(`
apiVersion: apps/v1
kind: Deployment
metadata:
  name: "{{ .name }}"
  namespace: "{{ .namespace }}"
spec:
  replicas: 1
`), 0644))

	// Create config file with both buildRef and manifest.ref
	configYAML := `
apiVersion: hyperfleet.redhat.com/v1alpha1
kind: AdapterConfig
metadata:
  name: test-adapter
spec:
  adapter:
    version: "0.0.1"
  hyperfleetApi:
    timeout: 2s
  kubernetes:
    apiVersion: "v1"
  params:
    - name: "clusterId"
      source: "event.cluster_id"
  resources:
    - name: "deployment"
      manifest:
        ref: "templates/deployment.yaml"
      discovery:
        namespace: "*"
        bySelectors:
          labelSelector:
            app: "test"
  post:
    params:
      - name: "statusPayload"
        buildRef: "templates/status-payload.yaml"
`
	configPath := filepath.Join(tmpDir, "config.yaml")
	require.NoError(t, os.WriteFile(configPath, []byte(configYAML), 0644))

	// Load config
	config, err := Load(configPath, WithSkipSemanticValidation())
	require.NoError(t, err)
	require.NotNil(t, config)

	// Verify manifest.ref was loaded and replaced
	require.Len(t, config.Spec.Resources, 1)
	manifest, ok := config.Spec.Resources[0].Manifest.(map[string]interface{})
	require.True(t, ok, "Manifest should be a map after loading ref")
	assert.Equal(t, "apps/v1", manifest["apiVersion"])
	assert.Equal(t, "Deployment", manifest["kind"])
	// Verify ref is no longer present (replaced with actual content)
	_, hasRef := manifest["ref"]
	assert.False(t, hasRef, "ref should be replaced with actual content")

	// Verify buildRef content was loaded into BuildRefContent
	require.NotNil(t, config.Spec.Post)
	require.Len(t, config.Spec.Post.Params, 1)
	assert.NotNil(t, config.Spec.Post.Params[0].BuildRefContent)
	assert.Equal(t, "{{ .status }}", config.Spec.Post.Params[0].BuildRefContent["status"])
	assert.Equal(t, "Operation completed", config.Spec.Post.Params[0].BuildRefContent["message"])
	// Original BuildRef path should still be preserved
	assert.Equal(t, "templates/status-payload.yaml", config.Spec.Post.Params[0].BuildRef)
}

func TestValidateResourceDiscovery(t *testing.T) {
	tests := []struct {
		name    string
		config  *AdapterConfig
		wantErr bool
		errMsg  string
	}{
		{
			name: "valid - manifest.ref with discovery bySelectors",
			config: &AdapterConfig{
				Spec: AdapterConfigSpec{
					Resources: []Resource{
						{
							Name:     "test",
							Manifest: map[string]interface{}{"ref": "templates/test.yaml"},
							Discovery: &DiscoveryConfig{
								Namespace: "test-ns",
								BySelectors: &SelectorConfig{
									LabelSelector: map[string]string{"app": "test"},
								},
							},
						},
					},
				},
			},
			wantErr: false,
		},
		{
			name: "valid - manifest.ref with discovery byName",
			config: &AdapterConfig{
				Spec: AdapterConfigSpec{
					Resources: []Resource{
						{
							Name:     "test",
							Manifest: map[string]interface{}{"ref": "templates/test.yaml"},
							Discovery: &DiscoveryConfig{
								Namespace: "*",
								ByName:    "my-resource",
							},
						},
					},
				},
			},
			wantErr: false,
		},
		{
			name: "valid - inline manifest with discovery",
			config: &AdapterConfig{
				Spec: AdapterConfigSpec{
					Resources: []Resource{
						{
							Name: "test",
							Manifest: map[string]interface{}{
								"apiVersion": "v1",
								"kind":       "ConfigMap",
							},
							Discovery: &DiscoveryConfig{
								Namespace: "test-ns",
								ByName:    "my-configmap",
							},
						},
					},
				},
			},
			wantErr: false,
		},
		{
			name: "invalid - inline manifest missing discovery",
			config: &AdapterConfig{
				Spec: AdapterConfigSpec{
					Resources: []Resource{
						{
							Name: "test",
							Manifest: map[string]interface{}{
								"apiVersion": "v1",
								"kind":       "ConfigMap",
							},
							// Missing discovery - now required for all resources
						},
					},
				},
			},
			wantErr: true,
			errMsg:  "discovery is required",
		},
		{
			name: "invalid - manifest.ref missing discovery",
			config: &AdapterConfig{
				Spec: AdapterConfigSpec{
					Resources: []Resource{
						{
							Name:     "test",
							Manifest: map[string]interface{}{"ref": "templates/test.yaml"},
							// Missing discovery
						},
					},
				},
			},
			wantErr: true,
			errMsg:  "discovery is required",
		},
		{
			name: "invalid - manifest.ref missing discovery.namespace",
			config: &AdapterConfig{
				Spec: AdapterConfigSpec{
					Resources: []Resource{
						{
							Name:     "test",
							Manifest: map[string]interface{}{"ref": "templates/test.yaml"},
							Discovery: &DiscoveryConfig{
								// Missing namespace
								ByName: "my-resource",
							},
						},
					},
				},
			},
			wantErr: true,
			errMsg:  "discovery.namespace is required",
		},
		{
			name: "invalid - manifest.ref missing byName or bySelectors",
			config: &AdapterConfig{
				Spec: AdapterConfigSpec{
					Resources: []Resource{
						{
							Name:     "test",
							Manifest: map[string]interface{}{"ref": "templates/test.yaml"},
							Discovery: &DiscoveryConfig{
								Namespace: "test-ns",
								// Missing byName and bySelectors
							},
						},
					},
				},
			},
			wantErr: true,
			errMsg:  "must have either byName or bySelectors",
		},
		{
			name: "invalid - bySelectors without labelSelector defined",
			config: &AdapterConfig{
				Spec: AdapterConfigSpec{
					Resources: []Resource{
						{
							Name:     "test",
							Manifest: map[string]interface{}{"ref": "templates/test.yaml"},
							Discovery: &DiscoveryConfig{
								Namespace:   "test-ns",
								BySelectors: &SelectorConfig{
									// Empty selectors
								},
							},
						},
					},
				},
			},
			wantErr: true,
			errMsg:  "must have labelSelector defined",
		},
		{
			name: "valid - manifest.refs array with discovery",
			config: &AdapterConfig{
				Spec: AdapterConfigSpec{
					Resources: []Resource{
						{
							Name: "test",
							Manifest: map[string]interface{}{
								"refs": []interface{}{"templates/a.yaml", "templates/b.yaml"},
							},
							Discovery: &DiscoveryConfig{
								Namespace: "*",
								BySelectors: &SelectorConfig{
									LabelSelector: map[string]string{"app": "test"},
								},
							},
						},
					},
				},
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateResources(tt.config)
			if tt.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errMsg)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestLoadManifestRefsArray(t *testing.T) {
	// Create temporary directory
	tmpDir := t.TempDir()
	templateDir := filepath.Join(tmpDir, "templates")
	require.NoError(t, os.MkdirAll(templateDir, 0755))

	// Create multiple template files
	require.NoError(t, os.WriteFile(filepath.Join(templateDir, "configmap.yaml"), []byte(`
apiVersion: v1
kind: ConfigMap
metadata:
  name: test-cm
`), 0644))

	require.NoError(t, os.WriteFile(filepath.Join(templateDir, "secret.yaml"), []byte(`
apiVersion: v1
kind: Secret
metadata:
  name: test-secret
`), 0644))

	// Create config with manifest.refs array
	configYAML := `
apiVersion: hyperfleet.redhat.com/v1alpha1
kind: AdapterConfig
metadata:
  name: test-adapter
spec:
  adapter:
    version: "0.0.1"
  hyperfleetApi:
    timeout: 2s
  kubernetes:
    apiVersion: "v1"
  params:
    - name: "clusterId"
      source: "event.cluster_id"
  resources:
    - name: "multiResource"
      manifest:
        refs:
          - "templates/configmap.yaml"
          - "templates/secret.yaml"
      discovery:
        namespace: "*"
        bySelectors:
          labelSelector:
            app: "test"
`
	configPath := filepath.Join(tmpDir, "config.yaml")
	require.NoError(t, os.WriteFile(configPath, []byte(configYAML), 0644))

	// Load config
	config, err := Load(configPath, WithSkipSemanticValidation())
	require.NoError(t, err)
	require.NotNil(t, config)

	// Verify ManifestItems contains both loaded manifests
	require.Len(t, config.Spec.Resources, 1)
	resource := config.Spec.Resources[0]
	require.Len(t, resource.ManifestItems, 2)

	// First item should be configmap
	assert.Equal(t, "v1", resource.ManifestItems[0]["apiVersion"])
	assert.Equal(t, "ConfigMap", resource.ManifestItems[0]["kind"])

	// Second item should be secret
	assert.Equal(t, "v1", resource.ManifestItems[1]["apiVersion"])
	assert.Equal(t, "Secret", resource.ManifestItems[1]["kind"])
}

func TestConditionValuesAlias(t *testing.T) {
	// Test that both "value" and "values" YAML keys are supported
	tests := []struct {
		name     string
		yaml     string
		expected interface{}
	}{
		{
			name: "value with single item",
			yaml: `
field: status
operator: equals
value: "Ready"
`,
			expected: "Ready",
		},
		{
			name: "value with list",
			yaml: `
field: status
operator: in
value:
  - "Ready"
  - "Running"
`,
			expected: []interface{}{"Ready", "Running"},
		},
		{
			name: "values with list (alias)",
			yaml: `
field: status
operator: in
values:
  - "Ready"
  - "Running"
`,
			expected: []interface{}{"Ready", "Running"},
		},
		{
			name: "values takes precedence over value",
			yaml: `
field: status
operator: in
value: "ignored"
values:
  - "Used"
`,
			expected: []interface{}{"Used"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var cond Condition
			err := yaml.Unmarshal([]byte(tt.yaml), &cond)
			require.NoError(t, err)
			assert.Equal(t, tt.expected, cond.Value)
		})
	}
}

