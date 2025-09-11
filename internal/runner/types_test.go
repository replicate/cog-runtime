package runner

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStatus(t *testing.T) {
	t.Parallel()

	tests := []struct {
		status Status
		want   string
	}{
		{StatusStarting, "STARTING"},
		{StatusSetupFailed, "SETUP_FAILED"},
		{StatusReady, "READY"},
		{StatusBusy, "BUSY"},
		{StatusDefunct, "DEFUNCT"},
		{Status(999), "INVALID"},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			t.Parallel()

			got := tt.status.String()
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestGenerateRunnerId(t *testing.T) {
	t.Parallel()

	t.Run("generates unique IDs", func(t *testing.T) {
		t.Parallel()

		ids := make(map[string]bool)
		const numIDs = 1000

		for i := 0; i < numIDs; i++ {
			id := GenerateRunnerID()
			idStr := id.String()

			// Check format: 8-character string
			assert.Len(t, idStr, 8)

			// Check uniqueness
			assert.False(t, ids[idStr], "ID %s was generated twice", idStr)
			ids[idStr] = true

			// Check no leading zeros (should be replaced with 'a')
			assert.NotEqual(t, '0', idStr[0], "ID should not start with '0'")

			// Check only valid base32 characters (lowercase)
			for _, c := range idStr {
				assert.True(t, (c >= 'a' && c <= 'z') || (c >= '2' && c <= '7'),
					"Invalid character %c in ID %s", c, idStr)
			}
		}
	})

	t.Run("String method", func(t *testing.T) {
		t.Parallel()

		id := RunnerID("test1234")
		assert.Equal(t, "test1234", id.String())
	})

	t.Run("consistent format", func(t *testing.T) {
		t.Parallel()

		// Generate multiple IDs and check they all follow the same format
		for i := 0; i < 100; i++ {
			id := GenerateRunnerID()
			idStr := id.String()

			// Should be exactly 8 characters
			assert.Len(t, idStr, 8)

			// Should be all lowercase alphanumeric (base32 subset)
			for _, c := range idStr {
				assert.True(t,
					(c >= 'a' && c <= 'z') || (c >= '2' && c <= '7'),
					"Invalid character in ID: %c", c)
			}

			// Should not start with 0
			assert.NotEqual(t, '0', idStr[0])
		}
	})
}

func TestPredictionRequest(t *testing.T) {
	t.Parallel()

	t.Run("struct fields", func(t *testing.T) {
		t.Parallel()

		req := PredictionRequest{
			ID:                 "test-id",
			Input:              map[string]any{"key": "value"},
			Webhook:            "http://example.com/webhook",
			ProcedureSourceURL: "abc123",
		}

		assert.Equal(t, "test-id", req.ID)
		assert.Equal(t, map[string]any{"key": "value"}, req.Input)
		assert.Equal(t, "http://example.com/webhook", req.Webhook)
		assert.Equal(t, "abc123", req.ProcedureSourceURL)
	})
}

func TestPredictionResponse(t *testing.T) {
	t.Parallel()

	t.Run("struct fields", func(t *testing.T) {
		t.Parallel()

		resp := PredictionResponse{
			ID:         "test-id",
			Status:     "succeeded",
			Output:     map[string]any{"result": "success"},
			Error:      "",
			Logs:       []string{"log1", "log2"},
			Metrics:    map[string]any{"duration": 1.5},
			WebhookURL: "http://example.com/webhook",
		}

		assert.Equal(t, "test-id", resp.ID)
		assert.Equal(t, PredictionSucceeded, resp.Status)
		assert.Equal(t, map[string]any{"result": "success"}, resp.Output)
		assert.Empty(t, resp.Error)
		assert.Equal(t, []string{"log1", "log2"}, resp.Logs)
		assert.Equal(t, map[string]any{"duration": 1.5}, resp.Metrics)
		assert.Equal(t, "http://example.com/webhook", resp.WebhookURL)
	})
}

func TestConcurrency(t *testing.T) {
	t.Parallel()

	t.Run("struct fields", func(t *testing.T) {
		t.Parallel()

		c := Concurrency{
			Max:     10,
			Current: 5,
		}

		assert.Equal(t, 10, c.Max)
		assert.Equal(t, 5, c.Current)
	})
}

func TestConstants(t *testing.T) {
	t.Parallel()

	t.Run("default values", func(t *testing.T) {
		t.Parallel()

		assert.Equal(t, 0, DefaultRunnerID)
		assert.Equal(t, "default", DefaultRunnerName)
	})

	t.Run("regex patterns", func(t *testing.T) {
		t.Parallel()

		// Test LogRegex
		testLog := "[pid=12345] This is a test message"
		matches := LogRegex.FindStringSubmatch(testLog)
		assert.Len(t, matches, 3)
		assert.Equal(t, "12345", matches[1])
		assert.Equal(t, "This is a test message", matches[2])

		// Test ResponseRegex
		testResponse := "response-abc123-1234567890.json"
		matches = ResponseRegex.FindStringSubmatch(testResponse)
		assert.Len(t, matches, 3)
		assert.Equal(t, "abc123", matches[1])
		assert.Equal(t, "1234567890", matches[2])

		// Test CancelFmt
		cancelFile := fmt.Sprintf(CancelFmt, "test-pid")
		assert.Equal(t, "cancel-test-pid", cancelFile)
	})
}

func TestPredictionResponseMarshalUnmarshalJSON(t *testing.T) {
	t.Parallel()

	t.Run("nil logs", func(t *testing.T) {
		t.Parallel()

		original := PredictionResponse{
			ID:     "test-id",
			Status: PredictionSucceeded,
			Output: "test output",
			Logs:   nil,
		}

		data, err := json.Marshal(original)
		require.NoError(t, err)

		// Verify logs field is omitted for nil/empty
		var jsonData map[string]any
		err = json.Unmarshal(data, &jsonData)
		require.NoError(t, err)
		_, exists := jsonData["logs"]
		assert.False(t, exists, "logs field should not exist for nil logs")

		var unmarshaled PredictionResponse
		err = json.Unmarshal(data, &unmarshaled)
		require.NoError(t, err)

		assert.Equal(t, original.ID, unmarshaled.ID)
		assert.Equal(t, original.Status, unmarshaled.Status)
		assert.Equal(t, original.Output, unmarshaled.Output)
		assert.Nil(t, unmarshaled.Logs)
	})

	t.Run("empty slice logs", func(t *testing.T) {
		t.Parallel()

		original := PredictionResponse{
			ID:     "test-id",
			Status: PredictionSucceeded,
			Output: "test output",
			Logs:   []string{},
		}

		data, err := json.Marshal(original)
		require.NoError(t, err)

		// Verify logs field is omitted for empty slice
		var jsonData map[string]any
		err = json.Unmarshal(data, &jsonData)
		require.NoError(t, err)
		_, exists := jsonData["logs"]
		assert.False(t, exists, "logs field should not exist for empty logs")

		var unmarshaled PredictionResponse
		err = json.Unmarshal(data, &unmarshaled)
		require.NoError(t, err)

		assert.Equal(t, original.ID, unmarshaled.ID)
		assert.Equal(t, original.Status, unmarshaled.Status)
		assert.Equal(t, original.Output, unmarshaled.Output)
		// After JSON round-trip, empty slice becomes nil
		assert.Nil(t, unmarshaled.Logs)
	})

	t.Run("single log line", func(t *testing.T) {
		t.Parallel()

		original := PredictionResponse{
			ID:     "test-id",
			Status: PredictionSucceeded,
			Output: "test output",
			Logs:   []string{"hello world"},
		}

		data, err := json.Marshal(original)
		require.NoError(t, err)

		var jsonData map[string]any
		err = json.Unmarshal(data, &jsonData)
		require.NoError(t, err)
		assert.Equal(t, "hello world\n", jsonData["logs"])

		var unmarshaled PredictionResponse
		err = json.Unmarshal(data, &unmarshaled)
		require.NoError(t, err)

		assert.Equal(t, original.ID, unmarshaled.ID)
		assert.Equal(t, original.Status, unmarshaled.Status)
		assert.Equal(t, original.Output, unmarshaled.Output)
		assert.Equal(t, original.Logs, unmarshaled.Logs)
	})

	t.Run("multiple log lines", func(t *testing.T) {
		t.Parallel()

		original := PredictionResponse{
			ID:     "test-id",
			Status: PredictionSucceeded,
			Output: "test output",
			Logs:   []string{"starting prediction", "prediction in progress 1/2", "prediction in progress 2/2", "completed prediction"},
		}

		data, err := json.Marshal(original)
		require.NoError(t, err)

		var jsonData map[string]any
		err = json.Unmarshal(data, &jsonData)
		require.NoError(t, err)
		assert.Equal(t, "starting prediction\nprediction in progress 1/2\nprediction in progress 2/2\ncompleted prediction\n", jsonData["logs"])

		var unmarshaled PredictionResponse
		err = json.Unmarshal(data, &unmarshaled)
		require.NoError(t, err)

		assert.Equal(t, original.ID, unmarshaled.ID)
		assert.Equal(t, original.Status, unmarshaled.Status)
		assert.Equal(t, original.Output, unmarshaled.Output)
		assert.Equal(t, original.Logs, unmarshaled.Logs)
	})
}

func TestPredictionResponseUnmarshalFromExternalJSON(t *testing.T) {
	t.Parallel()

	// Test unmarshalling from JSON with logs as string (external format)
	jsonStr := `{
		"id": "test-id",
		"status": "succeeded", 
		"output": "test output",
		"logs": "starting prediction\nprediction in progress 1/2\nprediction in progress 2/2\ncompleted prediction\n"
	}`

	var response PredictionResponse
	err := json.Unmarshal([]byte(jsonStr), &response)
	require.NoError(t, err)

	expected := []string{
		"starting prediction",
		"prediction in progress 1/2",
		"prediction in progress 2/2",
		"completed prediction",
	}

	assert.Equal(t, "test-id", response.ID)
	assert.Equal(t, PredictionSucceeded, response.Status)
	assert.Equal(t, "test output", response.Output)
	assert.Equal(t, expected, response.Logs)
}
