package k8s_client

import (
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// GVKFromKindAndApiVersion creates a GroupVersionKind from kind and apiVersion strings.
//
// This is the PRIMARY method for extracting GVK from adapter config templates.
// All production code should use this function with values from the config YAML.
//
// Example usage with config:
//   // From adapter-config-template.yaml:
//   //   - kind: "Deployment"
//   //     apiVersion: "apps/v1"
//
//   gvk, err := GVKFromKindAndApiVersion(resource.Kind, resource.ApiVersion)
//   if err != nil {
//       return fmt.Errorf("invalid GVK in config: %w", err)
//   }
//
//   // Now use gvk with client operations:
//   obj, err := client.GetResource(ctx, gvk, namespace, name)
func GVKFromKindAndApiVersion(kind, apiVersion string) (schema.GroupVersionKind, error) {
	gv, err := schema.ParseGroupVersion(apiVersion)
	if err != nil {
		return schema.GroupVersionKind{}, err
	}

	return schema.GroupVersionKind{
		Group:   gv.Group,
		Version: gv.Version,
		Kind:    kind,
	}, nil
}

// GVKFromUnstructured extracts GroupVersionKind from an unstructured object.
//
// This is useful when you've rendered a template and need to extract the GVK
// before creating the resource.
//
// Example:
//   obj, _ := RenderAndParseResource(template, variables)
//   gvk := GVKFromUnstructured(obj)
//   created, err := client.CreateResource(ctx, obj)
func GVKFromUnstructured(obj *unstructured.Unstructured) schema.GroupVersionKind {
	if obj == nil {
		return schema.GroupVersionKind{}
	}
	return obj.GroupVersionKind()
}

// NOTE: CommonResourceKinds has been moved to test_helpers.go
// It is for testing purposes only and should not be used in production code.
// Production code must extract GVK from config using GVKFromKindAndApiVersion().
