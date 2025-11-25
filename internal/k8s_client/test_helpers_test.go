package k8s_client

// This file contains test-only helpers and constants.
// DO NOT use these in production code - they are for testing purposes only.

import (
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// CommonResourceKinds provides commonly used GroupVersionKinds for testing purposes ONLY.
//
// This is a test helper and should NOT be used in production code.
// Production code must extract GVK from config using GVKFromKindAndApiVersion().
//
// Available only in test builds for:
//   - Unit tests (internal/k8s_client/*_test.go)
//   - Integration tests (test/integration/k8s_client/*_test.go)
//
// Example usage in tests:
//   ns := &unstructured.Unstructured{...}
//   ns.SetGroupVersionKind(k8s_client.CommonResourceKinds.Namespace)
//   _, err := client.CreateResource(ctx, ns)
var CommonResourceKinds = struct {
	// Core resources (v1)
	Namespace             schema.GroupVersionKind
	Pod                   schema.GroupVersionKind
	Service               schema.GroupVersionKind
	ConfigMap             schema.GroupVersionKind
	Secret                schema.GroupVersionKind
	ServiceAccount        schema.GroupVersionKind
	PersistentVolumeClaim schema.GroupVersionKind

	// Apps resources (apps/v1)
	Deployment  schema.GroupVersionKind
	StatefulSet schema.GroupVersionKind
	DaemonSet   schema.GroupVersionKind
	ReplicaSet  schema.GroupVersionKind

	// Batch resources (batch/v1)
	Job     schema.GroupVersionKind
	CronJob schema.GroupVersionKind

	// RBAC resources (rbac.authorization.k8s.io/v1)
	Role               schema.GroupVersionKind
	RoleBinding        schema.GroupVersionKind
	ClusterRole        schema.GroupVersionKind
	ClusterRoleBinding schema.GroupVersionKind

	// Networking resources (networking.k8s.io/v1)
	Ingress       schema.GroupVersionKind
	NetworkPolicy schema.GroupVersionKind

	// Storage resources (storage.k8s.io/v1)
	StorageClass schema.GroupVersionKind
}{
	// Core v1
	Namespace:             schema.GroupVersionKind{Group: "", Version: "v1", Kind: "Namespace"},
	Pod:                   schema.GroupVersionKind{Group: "", Version: "v1", Kind: "Pod"},
	Service:               schema.GroupVersionKind{Group: "", Version: "v1", Kind: "Service"},
	ConfigMap:             schema.GroupVersionKind{Group: "", Version: "v1", Kind: "ConfigMap"},
	Secret:                schema.GroupVersionKind{Group: "", Version: "v1", Kind: "Secret"},
	ServiceAccount:        schema.GroupVersionKind{Group: "", Version: "v1", Kind: "ServiceAccount"},
	PersistentVolumeClaim: schema.GroupVersionKind{Group: "", Version: "v1", Kind: "PersistentVolumeClaim"},

	// Apps v1
	Deployment:  schema.GroupVersionKind{Group: "apps", Version: "v1", Kind: "Deployment"},
	StatefulSet: schema.GroupVersionKind{Group: "apps", Version: "v1", Kind: "StatefulSet"},
	DaemonSet:   schema.GroupVersionKind{Group: "apps", Version: "v1", Kind: "DaemonSet"},
	ReplicaSet:  schema.GroupVersionKind{Group: "apps", Version: "v1", Kind: "ReplicaSet"},

	// Batch v1
	Job:     schema.GroupVersionKind{Group: "batch", Version: "v1", Kind: "Job"},
	CronJob: schema.GroupVersionKind{Group: "batch", Version: "v1", Kind: "CronJob"},

	// RBAC v1
	Role:               schema.GroupVersionKind{Group: "rbac.authorization.k8s.io", Version: "v1", Kind: "Role"},
	RoleBinding:        schema.GroupVersionKind{Group: "rbac.authorization.k8s.io", Version: "v1", Kind: "RoleBinding"},
	ClusterRole:        schema.GroupVersionKind{Group: "rbac.authorization.k8s.io", Version: "v1", Kind: "ClusterRole"},
	ClusterRoleBinding: schema.GroupVersionKind{Group: "rbac.authorization.k8s.io", Version: "v1", Kind: "ClusterRoleBinding"},

	// Networking v1
	Ingress:       schema.GroupVersionKind{Group: "networking.k8s.io", Version: "v1", Kind: "Ingress"},
	NetworkPolicy: schema.GroupVersionKind{Group: "networking.k8s.io", Version: "v1", Kind: "NetworkPolicy"},

	// Storage v1
	StorageClass: schema.GroupVersionKind{Group: "storage.k8s.io", Version: "v1", Kind: "StorageClass"},
}

