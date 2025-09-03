package server

import (
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"testing/synctest"
	"time"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestProcessInputPaths(t *testing.T) {
	t.Run("NilDoc", func(t *testing.T) {
		t.Parallel()
		input := map[string]any{"test": "value"}
		paths := make([]string, 0)

		result, err := processInputPaths(input, nil, &paths, base64ToInput)

		require.NoError(t, err)
		assert.Equal(t, input, result)
		assert.Empty(t, paths)
	})

	t.Run("NoInputSchema", func(t *testing.T) {
		t.Parallel()
		doc := &openapi3.T{
			Components: &openapi3.Components{
				Schemas: map[string]*openapi3.SchemaRef{},
			},
		}
		input := map[string]any{"test": "value"}
		paths := make([]string, 0)

		result, err := processInputPaths(input, doc, &paths, base64ToInput)

		require.NoError(t, err)
		assert.Equal(t, input, result)
		assert.Empty(t, paths)
	})

	t.Run("NonMapInput", func(t *testing.T) {
		t.Parallel()
		doc := createTestDoc(t)
		input := "not a map"
		paths := make([]string, 0)

		result, err := processInputPaths(input, doc, &paths, base64ToInput)

		require.NoError(t, err)
		assert.Equal(t, input, result)
		assert.Empty(t, paths)
	})

	t.Run("UriFieldProcessing", func(t *testing.T) {
		testCases := []struct {
			name           string
			input          map[string]any
			expectedPaths  int
			expectMutation bool
		}{
			{
				name: "Base64Input",
				input: map[string]any{
					"image": "data:text/plain;base64," + base64.StdEncoding.EncodeToString([]byte("test content")),
				},
				expectedPaths:  1,
				expectMutation: true,
			},
			{
				name: "RegularString",
				input: map[string]any{
					"image": "regular string",
				},
				expectedPaths:  0,
				expectMutation: false,
			},
			{
				name: "EmptyString",
				input: map[string]any{
					"image": "",
				},
				expectedPaths:  0,
				expectMutation: false,
			},
			{
				name: "NonStringValue",
				input: map[string]any{
					"image": 123,
				},
				expectedPaths:  0,
				expectMutation: false,
			},
		}

		for _, tc := range testCases {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()
				doc := createTestDoc(t)
				paths := make([]string, 0)
				originalInput := deepCopyMap(tc.input)

				result, err := processInputPaths(tc.input, doc, &paths, base64ToInput)

				require.NoError(t, err)
				assert.Len(t, paths, tc.expectedPaths)

				if tc.expectMutation {
					assert.NotEqual(t, originalInput["image"], tc.input["image"], "Input should be mutated")
					assert.NotEqual(t, originalInput["image"], result.(map[string]any)["image"], "Result should reflect mutation")
					// Clean up temp files
					t.Cleanup(func() {
						for _, path := range paths {
							os.Remove(path)
						}
					})
				} else {
					assert.Equal(t, originalInput, tc.input, "Input should not be mutated")
				}
			})
		}
	})

	t.Run("UriArrayFieldProcessing", func(t *testing.T) {
		testCases := []struct {
			name           string
			input          map[string]any
			expectedPaths  int
			expectMutation bool
		}{
			{
				name: "Base64Array",
				input: map[string]any{
					"images": []any{
						"data:text/plain;base64," + base64.StdEncoding.EncodeToString([]byte("content1")),
						"data:text/plain;base64," + base64.StdEncoding.EncodeToString([]byte("content2")),
					},
				},
				expectedPaths:  2,
				expectMutation: true,
			},
			{
				name: "MixedArray",
				input: map[string]any{
					"images": []any{
						"data:text/plain;base64," + base64.StdEncoding.EncodeToString([]byte("content1")),
						"regular string",
						123,
					},
				},
				expectedPaths:  1,
				expectMutation: true,
			},
			{
				name: "EmptyArray",
				input: map[string]any{
					"images": []any{},
				},
				expectedPaths:  0,
				expectMutation: false,
			},
			{
				name: "NonArrayValue",
				input: map[string]any{
					"images": "not an array",
				},
				expectedPaths:  0,
				expectMutation: false,
			},
		}

		for _, tc := range testCases {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()
				doc := createTestDocWithArrays(t)
				paths := make([]string, 0)
				originalInput := deepCopyMap(tc.input)

				result, err := processInputPaths(tc.input, doc, &paths, base64ToInput)

				require.NoError(t, err)
				assert.Len(t, paths, tc.expectedPaths)

				if tc.expectMutation {
					assert.NotEqual(t, originalInput, tc.input, "Input should be mutated")
					// Clean up temp files
					t.Cleanup(func() {
						for _, path := range paths {
							os.Remove(path)
						}
					})
				}

				assert.NotNil(t, result)
			})
		}
	})

	t.Run("ObjectFieldProcessing", func(t *testing.T) {
		t.Parallel()
		doc := createTestDocWithObjects(t)
		input := map[string]any{
			"metadata": map[string]any{
				"file": "data:text/plain;base64," + base64.StdEncoding.EncodeToString([]byte("nested content")),
				"nested": map[string]any{
					"deep": "data:text/plain;base64," + base64.StdEncoding.EncodeToString([]byte("deep content")),
				},
			},
		}
		paths := make([]string, 0)

		result, err := processInputPaths(input, doc, &paths, base64ToInput)

		require.NoError(t, err)
		assert.Len(t, paths, 2, "Should process nested objects")
		assert.NotNil(t, result)

		// Clean up temp files
		t.Cleanup(func() {
			for _, path := range paths {
				os.Remove(path)
			}
		})
	})

	t.Run("FunctionError", func(t *testing.T) {
		t.Parallel()
		doc := createTestDoc(t)
		input := map[string]any{
			"image": "data:text/plain;base64,invalid_base64!@#",
		}
		paths := make([]string, 0)

		_, err := processInputPaths(input, doc, &paths, base64ToInput)

		assert.Error(t, err, "Should return error when base64 decoding fails")
	})

	t.Run("URLProcessing", func(t *testing.T) {
		t.Parallel()
		// Create test HTTP server
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/plain")
			w.Write([]byte("test file content"))
		}))
		defer server.Close()

		doc := createTestDoc(t)
		input := map[string]any{
			"image": server.URL + "/test.txt",
		}
		paths := make([]string, 0)

		result, err := processInputPaths(input, doc, &paths, urlToInput)

		require.NoError(t, err)
		assert.Len(t, paths, 1, "Should download URL to file")
		assert.NotEqual(t, server.URL+"/test.txt", result.(map[string]any)["image"], "Should replace URL with file path")

		// Verify file was created and has content
		filePath := result.(map[string]any)["image"].(string)
		content, err := os.ReadFile(filePath)
		require.NoError(t, err)
		assert.Equal(t, "test file content", string(content))

		// Clean up temp files
		t.Cleanup(func() {
			for _, path := range paths {
				os.Remove(path)
			}
		})
	})

	t.Run("NonUriField", func(t *testing.T) {
		t.Parallel()
		doc := createTestDocWithNonURIField(t)
		input := map[string]any{
			"text": "data:text/plain;base64," + base64.StdEncoding.EncodeToString([]byte("content")),
		}
		paths := make([]string, 0)

		result, err := processInputPaths(input, doc, &paths, base64ToInput)

		require.NoError(t, err)
		assert.Empty(t, paths, "Should not process non-URI fields")
		assert.Equal(t, input, result)
	})

	t.Run("UnknownField", func(t *testing.T) {
		t.Parallel()
		doc := createTestDoc(t)
		input := map[string]any{
			"unknown_field": "data:text/plain;base64," + base64.StdEncoding.EncodeToString([]byte("content")),
		}
		paths := make([]string, 0)

		result, err := processInputPaths(input, doc, &paths, base64ToInput)

		require.NoError(t, err)
		assert.Empty(t, paths, "Should skip unknown fields")
		assert.Equal(t, input, result)
	})
}

// Helper functions

func createTestDoc(t *testing.T) *openapi3.T {
	t.Helper()
	return &openapi3.T{
		Components: &openapi3.Components{
			Schemas: map[string]*openapi3.SchemaRef{
				"Input": {
					Value: &openapi3.Schema{
						Type: &openapi3.Types{"object"},
						Properties: map[string]*openapi3.SchemaRef{
							"image": {
								Value: &openapi3.Schema{
									Type:   &openapi3.Types{"string"},
									Format: "uri",
								},
							},
						},
					},
				},
			},
		},
	}
}

func createTestDocWithArrays(t *testing.T) *openapi3.T {
	t.Helper()
	return &openapi3.T{
		Components: &openapi3.Components{
			Schemas: map[string]*openapi3.SchemaRef{
				"Input": {
					Value: &openapi3.Schema{
						Type: &openapi3.Types{"object"},
						Properties: map[string]*openapi3.SchemaRef{
							"images": {
								Value: &openapi3.Schema{
									Type: &openapi3.Types{"array"},
									Items: &openapi3.SchemaRef{
										Value: &openapi3.Schema{
											Type:   &openapi3.Types{"string"},
											Format: "uri",
										},
									},
								},
							},
						},
					},
				},
			},
		},
	}
}

func createTestDocWithObjects(t *testing.T) *openapi3.T {
	t.Helper()
	return &openapi3.T{
		Components: &openapi3.Components{
			Schemas: map[string]*openapi3.SchemaRef{
				"Input": {
					Value: &openapi3.Schema{
						Type: &openapi3.Types{"object"},
						Properties: map[string]*openapi3.SchemaRef{
							"metadata": {
								Value: &openapi3.Schema{
									Type: &openapi3.Types{"object"},
								},
							},
						},
					},
				},
			},
		},
	}
}

func createTestDocWithNonURIField(t *testing.T) *openapi3.T {
	t.Helper()
	return &openapi3.T{
		Components: &openapi3.Components{
			Schemas: map[string]*openapi3.SchemaRef{
				"Input": {
					Value: &openapi3.Schema{
						Type: &openapi3.Types{"object"},
						Properties: map[string]*openapi3.SchemaRef{
							"text": {
								Value: &openapi3.Schema{
									Type: &openapi3.Types{"string"},
									// No format: "uri"
								},
							},
						},
					},
				},
			},
		},
	}
}

// deepCopyMap creates a deep copy of a map[string]any with nested slices.
// This is needed for testing because processInputPaths mutates nested arrays in-place,
// and maps.Copy only does shallow copying (the map structure is copied but nested
// slices still reference the same underlying arrays). Without deep copying, both the
// "original" and "copy" would be affected by mutations to nested slices, making our
// mutation assertions fail incorrectly.
func deepCopyMap(m map[string]any) map[string]any {
	result := make(map[string]any)
	for k, v := range m {
		switch val := v.(type) {
		case []any:
			// Deep copy slice - create new slice with copied elements
			newSlice := make([]any, len(val))
			copy(newSlice, val)
			result[k] = newSlice
		case map[string]any:
			// Recursively deep copy nested maps
			result[k] = deepCopyMap(val)
		default:
			// Primitive types can be copied directly
			result[k] = v
		}
	}
	return result
}

func TestRunner_ForceKill(t *testing.T) {
	t.Run("ForceKill with active process", func(t *testing.T) {
		t.Parallel()

		// Create a runner with a mock process
		runner := &Runner{
			cmd: exec.Cmd{
				Process: &os.Process{Pid: 12345}, // fake PID
			},
			shutdownGracePeriod: 0,
			stopped:             make(chan bool),
		}

		// Mock kill function to track calls
		var killedPid int
		var killedSignal syscall.Signal
		runner.killFn = func(pid int, sig syscall.Signal) error {
			killedPid = pid
			killedSignal = sig
			return nil
		}

		// Call ForceKill
		runner.ForceKill()

		// Verify correct kill call
		assert.Equal(t, -12345, killedPid, "Should kill process group (negative PID)")
		assert.Equal(t, syscall.SIGKILL, killedSignal, "Should use SIGKILL")
		assert.True(t, runner.killed, "Should mark runner as killed")
	})

	t.Run("ForceKill is idempotent", func(t *testing.T) {
		t.Parallel()

		runner := &Runner{
			cmd: exec.Cmd{
				Process: &os.Process{Pid: 12345},
			},
			shutdownGracePeriod: 0,
			stopped:             make(chan bool),
		}

		// Track number of kill calls
		killCallCount := 0
		runner.killFn = func(pid int, sig syscall.Signal) error {
			killCallCount++
			return nil
		}

		// Call ForceKill multiple times
		runner.ForceKill()
		runner.ForceKill()
		runner.ForceKill()

		// Should only kill once
		assert.Equal(t, 1, killCallCount, "Should only kill once despite multiple calls")
		assert.True(t, runner.killed, "Should mark runner as killed")
	})

	t.Run("ForceKill with nil process", func(t *testing.T) {
		t.Parallel()

		runner := &Runner{
			cmd:                 exec.Cmd{Process: nil},
			shutdownGracePeriod: 0,
			stopped:             make(chan bool),
		}

		// Track kill calls
		killCalled := false
		runner.killFn = func(pid int, sig syscall.Signal) error {
			killCalled = true
			return nil
		}

		// Should not panic and not call kill
		require.NotPanics(t, func() {
			runner.ForceKill()
		})
		assert.False(t, killCalled, "Should not call kill with nil process")
		assert.False(t, runner.killed, "Should not mark as killed")
	})

	t.Run("ForceKill with already exited process", func(t *testing.T) {
		t.Parallel()

		runner := &Runner{
			cmd: exec.Cmd{
				Process:      &os.Process{Pid: 12345},
				ProcessState: &os.ProcessState{}, // Non-nil means exited
			},
			shutdownGracePeriod: 0,
			stopped:             make(chan bool),
		}

		// Track kill calls
		killCalled := false
		runner.killFn = func(pid int, sig syscall.Signal) error {
			killCalled = true
			return nil
		}

		runner.ForceKill()

		assert.False(t, killCalled, "Should not kill already exited process")
		assert.False(t, runner.killed, "Should not mark as killed")
	})
}

func TestRunner_StopGracePeriod(t *testing.T) {
	t.Run("Grace period timeout triggers ForceKill", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			runner := &Runner{
				cmd: exec.Cmd{
					Process: &os.Process{Pid: 12345},
				},
				shutdownGracePeriod: 10 * time.Millisecond,
				stopped:             make(chan bool),
				workingDir:          t.TempDir(),
			}

			// Track kill calls
			killCalled := false
			runner.killFn = func(pid int, sig syscall.Signal) error {
				killCalled = true
				return nil
			}

			// Start graceful shutdown
			err := runner.Stop()
			require.NoError(t, err)

			// Wait for grace period to expire
			time.Sleep(50 * time.Millisecond)

			assert.True(t, killCalled, "Should call kill after grace period")
			assert.True(t, runner.killed, "Should mark runner as killed")
		})
	})

	t.Run("Graceful exit cancels ForceKill", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			runner := &Runner{
				cmd: exec.Cmd{
					Process: &os.Process{Pid: 12345},
				},
				shutdownGracePeriod: 100 * time.Millisecond,
				stopped:             make(chan bool),
				workingDir:          t.TempDir(),
			}

			// Track kill calls
			killCalled := false
			runner.killFn = func(pid int, sig syscall.Signal) error {
				killCalled = true
				return nil
			}

			// Start graceful shutdown
			err := runner.Stop()
			require.NoError(t, err)

			// Simulate graceful exit before grace period
			time.Sleep(10 * time.Millisecond)
			close(runner.stopped)

			// Wait past grace period
			time.Sleep(150 * time.Millisecond)

			assert.False(t, killCalled, "Should not call kill after graceful exit")
			assert.False(t, runner.killed, "Should not mark as killed after graceful exit")
		})
	})

	t.Run("Zero grace period calls ForceKill immediately", func(t *testing.T) {
		t.Parallel()

		runner := &Runner{
			cmd: exec.Cmd{
				Process: &os.Process{Pid: 12345},
			},
			shutdownGracePeriod: 0, // No grace period
			stopped:             make(chan bool),
			workingDir:          t.TempDir(),
		}

		// Track kill calls
		killCalled := false
		var killedPid int
		var killedSignal syscall.Signal
		runner.killFn = func(pid int, sig syscall.Signal) error {
			killCalled = true
			killedPid = pid
			killedSignal = sig
			return nil
		}

		// Start shutdown with zero grace period
		err := runner.Stop()
		require.NoError(t, err)

		// Should call kill immediately, no need to wait
		assert.True(t, killCalled, "Should call kill immediately with zero grace period")
		assert.Equal(t, -12345, killedPid, "Should kill process group")
		assert.Equal(t, syscall.SIGKILL, killedSignal, "Should use SIGKILL")
		assert.True(t, runner.killed, "Should mark runner as killed")
	})
}

func TestRunner_CleanupVerification(t *testing.T) {
	t.Run("HandleIPC waits for cleanup completion", func(t *testing.T) {
		t.Parallel()

		workingDir := t.TempDir()
		// Create necessary files to prevent errors in HandleIPC
		setupResultFile := filepath.Join(workingDir, "setup_result.json")
		require.NoError(t, os.WriteFile(setupResultFile, []byte(`{"status":"succeeded","started_at":"2023-01-01T00:00:00Z","completed_at":"2023-01-01T00:00:01Z"}`), 0o644))
		openAPIFile := filepath.Join(workingDir, "openapi.json")
		require.NoError(t, os.WriteFile(openAPIFile, []byte(`{"openapi":"3.0.0","info":{"title":"Test","version":"1.0.0"}}`), 0o644))

		runner := &Runner{
			cmd: exec.Cmd{
				Process: &os.Process{Pid: 12345},
			},
			status:              StatusStarting,
			cleanupSlot:         make(chan struct{}, 1),
			shutdownGracePeriod: 0,
			cleanupTimeout:      10 * time.Second,
			workingDir:          workingDir,
			stopped:             make(chan bool),
			setupResult:         SetupResult{},
		}

		// Mock kill function that always succeeds
		runner.killFn = func(pid int, sig syscall.Signal) error {
			return nil
		}

		// Call HandleIPC with Ready status while cleanup is in progress
		err := runner.HandleIPC(IPCStatusReady)
		require.NoError(t, err)

		// Should be StatusReady - runner reflects Python state, healthcheck handles cleanup override
		assert.Equal(t, StatusReady, runner.status)

		runner.cleanupSlot <- struct{}{}

		err = runner.HandleIPC(IPCStatusReady)
		require.NoError(t, err)

		runner.mu.Lock()
		actualStatus := runner.status
		runner.mu.Unlock()
		assert.Equal(t, StatusReady, actualStatus)
	})

	t.Run("verifyProcessGroupTerminated detects terminated processes", func(t *testing.T) {
		t.Parallel()

		// Test with a non-existent PID (should return true)
		result := verifyProcessGroupTerminated(999999) // Very unlikely to exist
		assert.True(t, result, "Should detect terminated process group")

		// NOTE: Testing with current process PID may not work reliably in all environments
		// as the current process may not be in its own process group
		// So we'll skip that part of the test for now
	})

	t.Run("ForceKill sets cleanup in progress", func(t *testing.T) {
		t.Parallel()
		synctest.Test(t, func(t *testing.T) {
			runner := &Runner{
				cmd: exec.Cmd{
					Process: &os.Process{Pid: 9999999},
				},
				shutdownGracePeriod: 0,
				cleanupTimeout:      10 * time.Second,
				cleanupSlot:         make(chan struct{}, 1),
				stopped:             make(chan bool),
			}
			runner.cleanupSlot <- struct{}{}

			var killedPid int
			runner.killFn = func(pid int, sig syscall.Signal) error {
				killedPid = pid
				return nil
			}

			// Mock verification to stay in progress initially, then complete later
			callCount := 0
			runner.verifyFn = func(pid int) bool {
				callCount++
				return callCount > 20 // Complete after 20 calls (200ms)
			}

			assert.Len(t, runner.cleanupSlot, 1)

			runner.ForceKill()

			assert.Equal(t, -9999999, killedPid)
			assert.Empty(t, runner.cleanupSlot)
			assert.True(t, runner.killed)

			// Let some time pass - cleanup verification should be running but not complete
			// since PID 1 will still exist after our mock kill
			time.Sleep(50 * time.Millisecond)

			// Cleanup should still be in progress since PID 1 process group still exists
			assert.Empty(t, runner.cleanupSlot)

			// Stop the cleanup verification by closing stopped channel
			close(runner.stopped)
			time.Sleep(10 * time.Millisecond)
		})
	})

	t.Run("Cleanup timeout triggers forceShutdown channel", func(t *testing.T) {
		t.Parallel()
		synctest.Test(t, func(t *testing.T) {
			forceShutdownChan := make(chan struct{}, 1)
			runner := &Runner{
				cmd: exec.Cmd{
					Process: &os.Process{Pid: 12345},
				},
				shutdownGracePeriod: 0,
				cleanupTimeout:      100 * time.Millisecond, // Short timeout for testing
				cleanupSlot:         make(chan struct{}, 1),
				forceShutdown:       forceShutdownChan,
				stopped:             make(chan bool),
			}
			runner.cleanupSlot <- struct{}{}

			runner.killFn = func(pid int, sig syscall.Signal) error {
				return nil
			}

			runner.verifyFn = func(pid int) bool {
				return false
			}

			runner.ForceKill()

			// Cleanup should be in progress
			assert.Empty(t, runner.cleanupSlot)

			time.Sleep(200 * time.Millisecond)

			select {
			case <-forceShutdownChan:
			default:
				t.Fatal("Expected forceShutdown signal after cleanup timeout")
			}
		})
	})

	t.Run("Multiple ForceKill calls are safe", func(t *testing.T) {
		t.Parallel()
		synctest.Test(t, func(t *testing.T) {
			runner := &Runner{
				cmd: exec.Cmd{
					Process: &os.Process{Pid: 12345},
				},
				shutdownGracePeriod: 0,
				cleanupTimeout:      10 * time.Second,
				cleanupSlot:         make(chan struct{}, 1),
				forceShutdown:       make(chan struct{}, 1),
				stopped:             make(chan bool),
			}
			runner.cleanupSlot <- struct{}{}

			killCallCount := 0
			runner.killFn = func(pid int, sig syscall.Signal) error {
				killCallCount++
				return nil
			}

			runner.verifyFn = func(pid int) bool {
				return false
			}

			// First call should take token and start cleanup
			runner.ForceKill()
			assert.Equal(t, 1, killCallCount)
			assert.Empty(t, runner.cleanupSlot)

			// Subsequent calls should return early since killed=true, so no additional kill calls
			runner.ForceKill()
			runner.ForceKill()
			assert.Equal(t, 1, killCallCount) // Still 1, not 3
			assert.Empty(t, runner.cleanupSlot)

			close(runner.stopped)
			time.Sleep(50 * time.Millisecond)
			assert.Empty(t, runner.cleanupSlot)
		})
	})

	t.Run("Cleanup verification respects stopped channel", func(t *testing.T) {
		t.Parallel()
		synctest.Test(t, func(t *testing.T) {
			runner := &Runner{
				cmd: exec.Cmd{
					Process: &os.Process{Pid: 12345},
				},
				shutdownGracePeriod: 0,
				cleanupTimeout:      10 * time.Second,
				cleanupSlot:         make(chan struct{}, 1),
				forceShutdown:       make(chan struct{}, 1),
				stopped:             make(chan bool),
			}
			runner.cleanupSlot <- struct{}{}

			runner.killFn = func(pid int, sig syscall.Signal) error {
				return nil
			}

			runner.ForceKill()
			assert.Empty(t, runner.cleanupSlot)

			// Close stopped channel to simulate runner shutdown
			close(runner.stopped)

			// Wait a bit to let cleanup verification exit
			time.Sleep(50 * time.Millisecond)

			// Cleanup should still be marked as in progress since verification was interrupted
			assert.Empty(t, runner.cleanupSlot)
		})
	})
}
