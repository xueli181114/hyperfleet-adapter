package k8s_client

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestDiscoveryConfig(t *testing.T) {
	t.Run("single resource discovery", func(t *testing.T) {
		d := &DiscoveryConfig{
			Namespace: "default",
			ByName:    "my-resource",
		}

		assert.Equal(t, "default", d.GetNamespace())
		assert.Equal(t, "my-resource", d.GetName())
		assert.Equal(t, "", d.GetLabelSelector())
		assert.True(t, d.IsSingleResource())
	})

	t.Run("list resource discovery", func(t *testing.T) {
		d := &DiscoveryConfig{
			Namespace:     "kube-system",
			LabelSelector: "app=myapp,env=prod",
		}

		assert.Equal(t, "kube-system", d.GetNamespace())
		assert.Equal(t, "", d.GetName())
		assert.Equal(t, "app=myapp,env=prod", d.GetLabelSelector())
		assert.False(t, d.IsSingleResource())
	})

	t.Run("cluster-scoped discovery", func(t *testing.T) {
		d := &DiscoveryConfig{
			Namespace:     "",
			LabelSelector: "type=cluster",
		}

		// Verify all four interface methods consistently
		assert.Equal(t, "", d.GetNamespace())
		assert.Equal(t, "", d.GetName())
		assert.Equal(t, "type=cluster", d.GetLabelSelector())
		assert.False(t, d.IsSingleResource())
	})
}

func TestBuildLabelSelector(t *testing.T) {
	tests := []struct {
		name   string
		labels map[string]string
		want   string
	}{
		{
			name:   "empty labels",
			labels: nil,
			want:   "",
		},
		{
			name:   "empty map",
			labels: map[string]string{},
			want:   "",
		},
		{
			name:   "single label",
			labels: map[string]string{"app": "myapp"},
			want:   "app=myapp",
		},
		{
			name: "multiple labels sorted alphabetically",
			labels: map[string]string{
				"env": "prod",
				"app": "myapp",
			},
			// Keys are sorted: app < env
			want: "app=myapp,env=prod",
		},
		{
			name: "three labels sorted alphabetically",
			labels: map[string]string{
				"version": "v1",
				"app":     "myapp",
				"env":     "prod",
			},
			// Keys are sorted: app < env < version
			want: "app=myapp,env=prod,version=v1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := BuildLabelSelector(tt.labels)
			assert.Equal(t, tt.want, got)
		})
	}
}
