package tests

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/replicate/cog-runtime/internal/server"
)

// func TestPathOut(t *testing.T) {
// 	testCases := []struct {
// 		predictor     string
// 		nested        bool
// 		skipLegacyCog bool
// 	}{
// 		{
// 			predictor:     "path_out",
// 			nested:        false,
// 			skipLegacyCog: false,
// 		},
// 		{
// 			predictor: "path_out_any",
// 			nested:    true,
// 		},
// 		{
// 			predictor:     "path_out_dataclass",
// 			nested:        true,
// 			skipLegacyCog: true,
// 		},
// 		{
// 			predictor:     "path_out_json",
// 			nested:        true,
// 			skipLegacyCog: false,
// 		},
// 		{
// 			predictor:     "path_out_output",
// 			nested:        true,
// 			skipLegacyCog: false,
// 		},
// 		{
// 			predictor:     "path_out_pathlike",
// 			nested:        false,
// 			skipLegacyCog: true,
// 		},
// 		{
// 			predictor:     "path_out_pydantic",
// 			nested:        true,
// 			skipLegacyCog: false,
// 		},
// 	}
// 	for _, testCase := range testCases {
// 		t.Run(testCase.predictor, func(t *testing.T) {
// 			testPathOut(t, testCase.predictor, testCase.nested)
// 		})
// 	}
// }

func testPathOut(t *testing.T, predictor string, nested bool) {
	ct := NewCogTest(t, predictor)
	assert.NoError(t, ct.Start())

	hc := ct.WaitForSetup()
	assert.Equal(t, server.StatusReady.String(), hc.Status)
	assert.Equal(t, server.SetupSucceeded, hc.Setup.Status)

	resp := ct.Prediction(map[string]any{"s": "foo"})
	var b64 string
	if *legacyCog {
		// Compat: different MIME type detection logic
		b64 = b64encodeLegacy("*foo*")
	} else {
		b64 = b64encode("*foo*")
	}
	var output any
	if nested {
		output = map[string]any{"p": b64}
	} else {
		output = b64
	}
	ct.AssertResponse(resp, server.PredictionSucceeded, output, "")

	ct.Shutdown()
	assert.NoError(t, ct.Cleanup())
}

func TestPathOut(t *testing.T) {
	// Output type is Path
	testPathOut(t, "path_out", false)
}

func TestPathOutAny(t *testing.T) {
	// Output type is Any
	testPathOut(t, "path_out_any", true)
}

func TestPathOutDataclass(t *testing.T) {
	// Compat: legacy Cog does not support dataclass
	if *legacyCog {
		t.SkipNow()
	}
	// Output type is a dataclass
	testPathOut(t, "path_out_dataclass", true)
}

func TestPathOutJSON(t *testing.T) {
	// Output type is dict[str, Any]
	testPathOut(t, "path_out_json", true)
}

func TestPathOutOutput(t *testing.T) {
	// Output type is cog.Output
	testPathOut(t, "path_out_output", true)
}

func TestPathOutPathLike(t *testing.T) {
	// Compat: legacy Cog does not support PathLike
	if *legacyCog {
		t.SkipNow()
	}
	// Output type is os.PathLike
	testPathOut(t, "path_out_pathlike", false)
}

func TestPathOutPydantic(t *testing.T) {
	// Output type is a Pydantic base model
	testPathOut(t, "path_out_pydantic", true)
}
