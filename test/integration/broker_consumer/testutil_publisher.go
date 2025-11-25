package broker_consumer_integration

// testutil_publisher.go provides utilities for publishing test messages
// to Pub/Sub topics during integration tests.

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/cloudevents/sdk-go/v2/event"
	"github.com/google/uuid"
	"github.com/openshift-hyperfleet/hyperfleet-broker/broker"
	"github.com/stretchr/testify/require"
)

// publishTestMessages publishes test CloudEvents to the specified topic
func publishTestMessages(t *testing.T, topic string, count int) {
	t.Helper()

	// Create publisher from environment variables
	publisher, err := broker.NewPublisher()
	require.NoError(t, err, "Failed to create publisher")
	defer func() {
		if err := publisher.Close(); err != nil {
			t.Errorf("Error closing publisher: %v", err)
		}
	}()

	t.Logf("Publishing %d test messages to topic: %s", count, topic)

	for i := 0; i < count; i++ {
		evt := createTestEvent(i)
		
		err := publisher.Publish(topic, evt)
		if err != nil {
			// Ignore "AlreadyExists" errors - topic may have been created by subscriber
			if isTopicAlreadyExistsError(err) {
				t.Logf("Topic already exists (expected): %v", err)
				// Retry publish now that we know the topic exists
				err = publisher.Publish(topic, evt)
				require.NoError(t, err, "Failed to publish message %d after topic creation", i)
			} else {
				require.NoError(t, err, "Failed to publish message %d", i)
			}
		}
		
		t.Logf("Published message %d: id=%s", i, evt.ID())
		
		// Small delay between messages
		time.Sleep(50 * time.Millisecond)
	}

	t.Logf("Successfully published %d messages", count)
	
	// Give emulator time to process all messages before closing publisher
	// This prevents race conditions where publisher closes before messages are fully delivered
	time.Sleep(100 * time.Millisecond)
}

// isTopicAlreadyExistsError checks if an error is due to a topic already existing
func isTopicAlreadyExistsError(err error) bool {
	if err == nil {
		return false
	}
	errMsg := strings.ToLower(err.Error())
	// Check for various "already exists" error patterns
	hasAlreadyExists := strings.Contains(errMsg, "already exists") || 
		strings.Contains(errMsg, "alreadyexists") ||
		strings.Contains(errMsg, "code = alreadyexists")
	hasTopic := strings.Contains(errMsg, "topic")
	return hasAlreadyExists && hasTopic
}

// createTestEvent creates a test CloudEvent
func createTestEvent(index int) *event.Event {
	evt := event.New()
	evt.SetID(uuid.New().String())
	evt.SetType("com.hyperfleet.test.event")
	evt.SetSource("test-suite")
	evt.SetTime(time.Now())
	evt.SetDataContentType("application/json")
	
	// Set event data
	data := map[string]interface{}{
		"index":     index,
		"timestamp": time.Now().Unix(),
		"message":   fmt.Sprintf("Test message %d", index),
		"metadata": map[string]string{
			"test":        "true",
			"environment": "integration",
		},
	}
	
	err := evt.SetData("application/json", data)
	if err != nil {
		// This should never happen in tests
		panic(fmt.Sprintf("Failed to set event data: %v", err))
	}
	
	return &evt
}


