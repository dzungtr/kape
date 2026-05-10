package proxy_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kape-io/kape/kapeproxy/internal/proxy"
)

func TestRedactor_BlanksTopLevelString(t *testing.T) {
	r := proxy.NewRedactor()
	in := map[string]any{"token": "secret", "keep": "ok"}
	out, err := r.Apply(in, []proxy.JSONPathRule{{JSONPath: "$.token"}})
	require.NoError(t, err)
	m := out.(map[string]any)
	assert.Equal(t, "", m["token"])
	assert.Equal(t, "ok", m["keep"])
}

func TestRedactor_BlanksNestedString(t *testing.T) {
	r := proxy.NewRedactor()
	in := map[string]any{
		"data": map[string]any{
			"email": "user@example.com",
			"name":  "Alice",
		},
	}
	out, err := r.Apply(in, []proxy.JSONPathRule{{JSONPath: "$.data.email"}})
	require.NoError(t, err)
	m := out.(map[string]any)
	d := m["data"].(map[string]any)
	assert.Equal(t, "", d["email"])
	assert.Equal(t, "Alice", d["name"])
}

func TestRedactor_UnknownPathIsNoOp(t *testing.T) {
	r := proxy.NewRedactor()
	in := map[string]any{"a": 1}
	out, err := r.Apply(in, []proxy.JSONPathRule{{JSONPath: "$.does.not.exist"}})
	require.NoError(t, err)
	assert.Equal(t, in, out)
}

func TestRedactor_NoRulesIsIdentity(t *testing.T) {
	r := proxy.NewRedactor()
	in := map[string]any{"a": 1}
	out, err := r.Apply(in, nil)
	require.NoError(t, err)
	assert.Equal(t, in, out)
}

func TestRedactor_MultipleRules(t *testing.T) {
	r := proxy.NewRedactor()
	in := map[string]any{"token": "s", "data": map[string]any{"email": "x@y"}}
	out, err := r.Apply(in, []proxy.JSONPathRule{
		{JSONPath: "$.token"},
		{JSONPath: "$.data.email"},
	})
	require.NoError(t, err)
	m := out.(map[string]any)
	assert.Equal(t, "", m["token"])
	assert.Equal(t, "", m["data"].(map[string]any)["email"])
}

func TestRedactor_OutputAcceptsArbitraryRoot(t *testing.T) {
	// Outputs may be any JSON shape — exercise an array-rooted result.
	r := proxy.NewRedactor()
	in := []any{map[string]any{"secret": "s", "ok": 1}}
	out, err := r.Apply(in, []proxy.JSONPathRule{{JSONPath: "$[0].secret"}})
	require.NoError(t, err)
	arr := out.([]any)
	assert.Equal(t, "", arr[0].(map[string]any)["secret"])
}
