package server

import (
	"os"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUIDMinimum(t *testing.T) {
	counter := newUIDCounter()
	assert.Equal(t, counter.Load(), BaseUID)
}

func TestUID(t *testing.T) {
	t.Run("AllocationThreadSafety", func(t *testing.T) {
		uidCounter := uidCounter{}

		const numGoroutines = 10
		const uidsPerGoroutine = 5

		var wg sync.WaitGroup
		uidChan := make(chan int, numGoroutines*uidsPerGoroutine)

		for range numGoroutines {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for range uidsPerGoroutine {
					uid, err := uidCounter.allocate()
					if err != nil {
						t.Errorf("allocateUID failed: %v", err)
						return
					}
					uidChan <- uid
				}
			}()
		}

		wg.Wait()
		close(uidChan)

		uidSet := make(map[int]bool)
		uidCount := 0

		for uid := range uidChan {
			assert.False(t, uidSet[uid], "Duplicate UID allocated: %d", uid)
			uidSet[uid] = true
			uidCount++
			assert.GreaterOrEqual(t, uid, BaseUID, "UID should be >= BaseUID")
		}

		expectedCount := numGoroutines * uidsPerGoroutine
		assert.Equal(t, expectedCount, uidCount, "Should allocate expected number of UIDs")

		for i := range expectedCount {
			expectedUID := BaseUID + i
			assert.True(t, uidSet[expectedUID], "Missing expected UID: %d", expectedUID)
		}
	})

	t.Run("SequentialAllocation", func(t *testing.T) {
		uidCounter := uidCounter{}
		firstUID, err := uidCounter.allocate()
		require.NoError(t, err, "allocateUID should not error")
		assert.Equal(t, BaseUID, firstUID, "First UID should be BaseUID")

		secondUID, err := uidCounter.allocate()
		require.NoError(t, err, "allocateUID should not error")
		assert.Equal(t, BaseUID+1, secondUID, "Second UID should be BaseUID+1")

		thirdUID, err := uidCounter.allocate()
		require.NoError(t, err, "allocateUID should not error")
		assert.Equal(t, BaseUID+2, thirdUID, "Third UID should be BaseUID+2")
	})

	t.Run("ErrorHandling", func(t *testing.T) {
		// Test that allocateUID returns proper error when it fails
		// This is hard to test in practice since UIDs 9000+ are usually available
		// But this documents the expected error behavior

		uidCounter := uidCounter{}

		// Normal allocation should work
		uid, err := uidCounter.allocate()
		require.NoError(t, err, "Normal allocation should succeed")
		assert.GreaterOrEqual(t, uid, BaseUID, "UID should be >= BaseUID")
	})

}

func TestTempDirectoryCleanup(t *testing.T) {
	t.Run("CleansUpTempDirectory", func(t *testing.T) {
		runner := &Runner{}

		tmpDir, err := os.MkdirTemp("", "test-cog-runner-tmp-")
		require.NoError(t, err, "Failed to create temp directory")

		runner.SetTmpDir(tmpDir)

		testFile := tmpDir + "/test-file.txt"
		err = os.WriteFile(testFile, []byte("test content"), 0644)
		require.NoError(t, err, "Failed to create test file")

		_, err = os.Stat(tmpDir)
		assert.False(t, os.IsNotExist(err), "Temp directory should exist before cleanup")

		_, err = os.Stat(testFile)
		assert.False(t, os.IsNotExist(err), "Test file should exist before cleanup")

		err = runner.Stop()
		require.NoError(t, err, "Runner.Stop() should not error")

		time.Sleep(100 * time.Millisecond)

		_, err = os.Stat(tmpDir)
		assert.True(t, os.IsNotExist(err), "Temp directory should be cleaned up after Stop()")
	})

	t.Run("HandlesEmptyTmpDir", func(t *testing.T) {
		runner := &Runner{}

		err := runner.Stop()
		assert.NoError(t, err, "Runner.Stop() should not error when tmpDir is empty")
	})
}
