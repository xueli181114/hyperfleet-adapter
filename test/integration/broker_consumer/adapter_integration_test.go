package broker_consumer_integration

// adapter_integration_test.go is the main entry point for broker_consumer adapter integration tests.
//
// IMPORTANT: This test suite focuses ONLY on adapter-specific functionality.
// Comprehensive broker functionality (publish/subscribe, message handling, acknowledgement,
// shared subscriptions, error handling, etc.) is already tested in the hyperfleet-broker
// library. We do NOT duplicate those tests here.
//
// The adapter is a thin wrapper that:
// 1. Reads BROKER_SUBSCRIPTION_ID from environment variables
// 2. Adds logging with glog
// 3. Wraps errors with additional context
//
// Tests in this file:
// - TestAdapterEnvironmentVariable: Tests environment variable configuration
// - TestAdapterSmokeTest: Basic smoke test that the wrapper properly delegates to hyperfleet-broker
// - TestAdapterConcurrentSubscribers: Tests race condition handling for concurrent subscriber creation

import (
	"context"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/cloudevents/sdk-go/v2/event"
	broker_consumer "github.com/openshift-hyperfleet/hyperfleet-adapter/internal/broker_consumer"
	"github.com/stretchr/testify/require"
)

// TestAdapterEnvironmentVariable tests the adapter-specific logic for
// reading subscription ID from environment variables
func TestAdapterEnvironmentVariable(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	// Setup Pub/Sub emulator container
	projectID, emulatorHost, cleanup := setupPubSubEmulatorContainer(t)
	defer cleanup()

	t.Run("uses BROKER_SUBSCRIPTION_ID when set", func(t *testing.T) {
		// Setup test environment
		_, cleanupEnv := setupTestEnvironment(t, projectID, emulatorHost, "broker-sub-id-test")
		defer cleanupEnv()

		require.NoError(t, os.Setenv("BROKER_SUBSCRIPTION_ID", "broker-sub-id-test"))
		defer func() {
			require.NoError(t, os.Unsetenv("BROKER_SUBSCRIPTION_ID"))
		}()

		// Create subscriber with empty string (forces reading from env)
		subscriber, subscriptionID, err := broker_consumer.NewSubscriber("")
		require.NoError(t, err, fmt.Sprintf("Should read BROKER_SUBSCRIPTION_ID from environment: %s", subscriptionID))
		require.NotNil(t, subscriber)
		defer func() {
			require.NoError(t, subscriber.Close())
		}()
	})

	t.Run("returns error when BROKER_SUBSCRIPTION_ID is not set", func(t *testing.T) {
		// Setup test environment
		_, cleanupEnv := setupTestEnvironment(t, projectID, emulatorHost, "dummy-sub")
		defer cleanupEnv()

		require.NoError(t, os.Unsetenv("BROKER_SUBSCRIPTION_ID"))

		// Create subscriber with empty string (forces reading from env)
		subscriber, subscriptionID, err := broker_consumer.NewSubscriber("")
		require.Error(t, err, fmt.Sprintf("Should return error when no subscription ID available: %s", subscriptionID))
		require.Nil(t, subscriber)
		require.Contains(t, err.Error(), "subscriptionID is required")
	})
}

// TestAdapterSmokeTest is a simple smoke test to verify the adapter wrapper
// properly delegates to the underlying broker library. This is NOT testing
// broker functionality (that's done in hyperfleet-broker), but rather verifying
// that our thin wrapper doesn't break the connection.
func TestAdapterSmokeTest(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	// Setup Pub/Sub emulator container
	projectID, emulatorHost, cleanup := setupPubSubEmulatorContainer(t)
	defer cleanup()

	// Setup test environment
	_, cleanupEnv := setupTestEnvironment(t, projectID, emulatorHost, "adapter-smoke-test")
	defer cleanupEnv()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// Create subscriber through adapter
	subscriber, subscriptionID, err := broker_consumer.NewSubscriber("adapter-smoke-test")
	require.NoError(t, err, fmt.Sprintf("Adapter should successfully create subscriber: %s", subscriptionID))
	require.NotNil(t, subscriber)
	defer func() {
		require.NoError(t, subscriber.Close())
	}()

	// Channel to signal when a message is received
	messageReceived := make(chan struct{}, 1)

	handler := func(ctx context.Context, evt *event.Event) error {
		t.Logf("Smoke test received message: %s", evt.ID())
		select {
		case messageReceived <- struct{}{}:
		default:
			// Channel already has a signal, don't block
		}
		return nil
	}

	// Subscribe through adapter
	topic := "smoke-test-topic"
	err = broker_consumer.Subscribe(ctx, subscriber, topic, handler)
	require.NoError(t, err, "Adapter should successfully subscribe")

	// Publish messages with retry until one is received or timeout
	// This handles the race condition where the subscriber may not be fully ready
	// immediately after Subscribe() returns
	if !publishAndWaitForMessage(t, ctx, topic, messageReceived) {
		t.Fatal("Timed out waiting for message - adapter should successfully receive message through broker")
	}
}

// publishAndWaitForMessage publishes messages periodically until one is received
// or the context times out. This avoids hardcoded sleep times by actively polling.
// Returns true if message was received, false on timeout.
func publishAndWaitForMessage(t *testing.T, ctx context.Context, topic string, messageReceived <-chan struct{}) bool {
	t.Helper()

	// Publish interval - how often to retry publishing
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	// Publish first message immediately
	publishTestMessages(t, topic, 1)

	for {
		select {
		case <-messageReceived:
			t.Log("Message received successfully")
			return true
		case <-ticker.C:
			// Subscriber might not be ready yet, publish another message
			t.Log("Retrying message publish...")
			publishTestMessages(t, topic, 1)
		case <-ctx.Done():
			return false
		}
	}
}

// TestAdapterConcurrentSubscribers tests that multiple subscribers can be created
// concurrently with the same subscription ID and topic without race condition errors.
// This verifies that the retry logic handles concurrent creation gracefully.
func TestAdapterConcurrentSubscribers(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	// Setup Pub/Sub emulator container
	projectID, emulatorHost, cleanup := setupPubSubEmulatorContainer(t)
	defer cleanup()

		// Setup test environment
	_, cleanupEnv := setupTestEnvironment(t, projectID, emulatorHost, "concurrent-test")
		defer cleanupEnv()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()

	const numConcurrentSubscribers = 5
	const subscriptionID = "concurrent-test"
	const topic = "concurrent-test-topic"

	// Channels to collect results
	type result struct {
		subscriber broker_consumer.Subscriber
		err        error
	}
	results := make(chan result, numConcurrentSubscribers)

	// WaitGroup to synchronize goroutine start
	var startWg sync.WaitGroup
	startWg.Add(1)

	// WaitGroup to wait for all goroutines to complete
	var doneWg sync.WaitGroup

	// Create subscribers concurrently
	for i := 0; i < numConcurrentSubscribers; i++ {
		doneWg.Add(1)
		go func(id int) {
			defer doneWg.Done()

			// Wait for all goroutines to be ready before starting
			startWg.Wait()

			t.Logf("Goroutine %d: Creating subscriber", id)
			subscriber, _, err := broker_consumer.NewSubscriber(subscriptionID)
			if err != nil {
				t.Logf("Goroutine %d: Failed to create subscriber: %v", id, err)
				results <- result{nil, err}
				return
			}

			t.Logf("Goroutine %d: Subscribing to topic", id)
			handler := func(ctx context.Context, evt *event.Event) error {
				return nil
			}
			err = broker_consumer.Subscribe(ctx, subscriber, topic, handler)
			if err != nil {
				t.Logf("Goroutine %d: Failed to subscribe: %v", id, err)
				// Don't close here - send subscriber to be closed later to avoid race conditions
				results <- result{subscriber, err}
				return
			}

			t.Logf("Goroutine %d: Success", id)
			results <- result{subscriber, nil}
		}(i)
	}

	// Start all goroutines at the same time to maximize race condition chance
	startWg.Done()

	// Wait for all goroutines to complete
	doneWg.Wait()
	close(results)

	// Collect and verify results
	var subscribers []broker_consumer.Subscriber
	var errors []error

	for r := range results {
		if r.err != nil {
			errors = append(errors, r.err)
		}
		// Collect all subscribers (successful or not) for cleanup
		if r.subscriber != nil {
			subscribers = append(subscribers, r.subscriber)
		}
	}

	// Clean up all subscribers (successful or failed) after test completes
	defer func() {
		for _, sub := range subscribers {
			if err := sub.Close(); err != nil {
				t.Errorf("Error closing subscriber: %v", err)
			}
		}
	}()

	// All concurrent subscribers should succeed (retry logic should handle race conditions)
	require.Empty(t, errors, "All concurrent subscriber creations should succeed, but got errors: %v", errors)
	require.Len(t, subscribers, numConcurrentSubscribers, "All %d subscribers should be created successfully", numConcurrentSubscribers)
		
	t.Logf("Successfully created %d concurrent subscribers with same subscription ID", len(subscribers))
}
