package logger

import (
	"context"
	"testing"
)

func TestNewLogger(t *testing.T) {
	tests := []struct {
		name string
		ctx  context.Context
	}{
		{
			name: "create_logger_with_empty_context",
			ctx:  context.Background(),
		},
		{
			name: "create_logger_with_nil_context",
			ctx:  nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			log := NewLogger(tt.ctx)
			if log == nil {
				t.Fatal("NewLogger returned nil")
			}

			// Type assertion to check implementation
			if _, ok := log.(*logger); !ok {
				t.Error("NewLogger didn't return *logger type")
			}
		})
	}
}

func TestLoggerV(t *testing.T) {
	ctx := context.Background()
	log := NewLogger(ctx)

	tests := []struct {
		name  string
		level int32
	}{
		{
			name:  "verbosity_level_0",
			level: 0,
		},
		{
			name:  "verbosity_level_1",
			level: 1,
		},
		{
			name:  "verbosity_level_2",
			level: 2,
		},
		{
			name:  "verbosity_level_5",
			level: 5,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			vlog := log.V(tt.level)
			if vlog == nil {
				t.Fatal("V() returned nil")
			}

			// Verify it returns a Logger
			if _, ok := vlog.(*logger); !ok {
				t.Error("V() didn't return *logger type")
			}

			// Verify level is set correctly
			if impl, ok := vlog.(*logger); ok {
				if impl.level != tt.level {
					t.Errorf("Expected level %d, got %d", tt.level, impl.level)
				}
			}
		})
	}
}

func TestLoggerExtra(t *testing.T) {
	ctx := context.Background()
	log := NewLogger(ctx)

	tests := []struct {
		name  string
		key   string
		value interface{}
	}{
		{
			name:  "add_string_extra",
			key:   "request_id",
			value: "12345",
		},
		{
			name:  "add_int_extra",
			key:   "status_code",
			value: 200,
		},
		{
			name:  "add_bool_extra",
			key:   "success",
			value: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := log.Extra(tt.key, tt.value)
			if result == nil {
				t.Fatal("Extra() returned nil")
			}

			// Verify it returns a Logger
			if _, ok := result.(*logger); !ok {
				t.Error("Extra() didn't return *logger type")
			}
		})
	}
}

func TestLoggerContextValues(t *testing.T) {
	tests := []struct {
		name          string
		ctxSetup      func() context.Context
		expectedKey   interface{} // Can be either contextKey or string for backward compatibility
		expectedValue interface{}
	}{
		{
		name: "context_with_txid_int64",
		ctxSetup: func() context.Context {
			return context.WithValue(context.Background(), TxIDKey, int64(12345))
		},
		expectedKey:   TxIDKey,
		expectedValue: int64(12345),
		},
		{
		name: "context_with_txid_string",
		ctxSetup: func() context.Context {
			return context.WithValue(context.Background(), TxIDKey, "tx-12345")
		},
		expectedKey:   TxIDKey,
		expectedValue: "tx-12345",
		},
		{
			name: "context_with_adapter_id",
			ctxSetup: func() context.Context {
				return context.WithValue(context.Background(), AdapterIDKey, "adapter-1")
			},
			expectedKey:   AdapterIDKey,
			expectedValue: "adapter-1",
		},
		{
			name: "context_with_cluster_id",
			ctxSetup: func() context.Context {
				return context.WithValue(context.Background(), ClusterIDKey, "cluster-1")
			},
			expectedKey:   ClusterIDKey,
			expectedValue: "cluster-1",
		},
		{
			name: "context_with_opid",
			ctxSetup: func() context.Context {
				return context.WithValue(context.Background(), OpIDKey, "op-12345")
			},
			expectedKey:   OpIDKey,
			expectedValue: "op-12345",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := tt.ctxSetup()
			log := NewLogger(ctx)

			if log == nil {
				t.Fatal("NewLogger returned nil")
			}

			// Verify context value is accessible
			if impl, ok := log.(*logger); ok {
				val := impl.context.Value(tt.expectedKey)
				if val == nil {
					t.Errorf("Expected context value for key %s, got nil", tt.expectedKey)
				}
			}
		})
	}
}

func TestLoggerMethods(t *testing.T) {
	// These tests just verify the methods don't panic
	// glog writes to stderr, so we can't easily capture output in tests
	ctx := context.Background()
	log := NewLogger(ctx)

	t.Run("Infof_does_not_panic", func(t *testing.T) {
		defer func() {
			if r := recover(); r != nil {
				t.Errorf("Infof panicked: %v", r)
			}
		}()
		log.Infof("Test message: %s", "value")
	})

	t.Run("Info_does_not_panic", func(t *testing.T) {
		defer func() {
			if r := recover(); r != nil {
				t.Errorf("Info panicked: %v", r)
			}
		}()
		log.Info("Test message")
	})

	t.Run("Warning_does_not_panic", func(t *testing.T) {
		defer func() {
			if r := recover(); r != nil {
				t.Errorf("Warning panicked: %v", r)
			}
		}()
		log.Warning("Test warning")
	})

	t.Run("Error_does_not_panic", func(t *testing.T) {
		defer func() {
			if r := recover(); r != nil {
				t.Errorf("Error panicked: %v", r)
			}
		}()
		log.Error("Test error")
	})
}

func TestLoggerChaining(t *testing.T) {
	ctx := context.Background()
	log := NewLogger(ctx)

	t.Run("chain_V_and_Extra", func(t *testing.T) {
		defer func() {
			if r := recover(); r != nil {
				t.Errorf("Chaining panicked: %v", r)
			}
		}()

		// Test method chaining
		log.V(2).Extra("key", "value").Infof("Test: %s", "chaining")
	})

	t.Run("chain_Extra_multiple_times", func(t *testing.T) {
		defer func() {
			if r := recover(); r != nil {
				t.Errorf("Multiple Extra panicked: %v", r)
			}
		}()

		log.Extra("key1", "value1").Extra("key2", "value2").Info("Test multiple extras")
	})
}

func TestLoggerConstants(t *testing.T) {
	tests := []struct {
		name     string
		constant contextKey
		expected string
	}{
		{
			name:     "TxIDKey",
			constant: TxIDKey,
			expected: "txid",
		},
		{
			name:     "AdapterIDKey",
			constant: AdapterIDKey,
			expected: "adapter_id",
		},
		{
			name:     "ClusterIDKey",
			constant: ClusterIDKey,
			expected: "cluster_id",
		},
		{
			name:     "OpIDKey",
			constant: OpIDKey,
			expected: "opid",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if string(tt.constant) != tt.expected {
				t.Errorf("Expected %s, got %s", tt.expected, string(tt.constant))
			}
		})
	}
}
