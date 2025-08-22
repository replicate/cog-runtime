package server

import (
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

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
