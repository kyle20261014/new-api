package smartrouting

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPolicyBoundaryAndCanonicalOrder(t *testing.T) {
	for _, data := range []string{
		`null`, `[]`, `{}`, `{"schema_version":2,"mode":"smart","strategy":"balanced"}`,
		`{"schema_version":1,"mode":"smart","strategy":"balanced","models":{}}`,
		`{"schema_version":1,"mode":"fixed","fixed_group":"auto"}`,
		`{"schema_version":1,"mode":"fixed","fixed_group":"a","strategy":"price"}`,
		`{"schema_version":1,"mode":"smart","strategy":"custom","models":{}}`,
		`{"schema_version":1,"mode":"smart","strategy":"custom","models":{"m":{"groups":[],"strategy":"price"}}}`,
		`{"schema_version":1,"mode":"smart","strategy":"custom","models":{"m":{"groups":["a","a"],"strategy":"manual"}}}`,
		`{"schema_version":1,"mode":"smart","strategy":"custom","models":{"m":{"groups":["a"],"strategy":"custom"}}}`,
		`{"schema_version":1,"mode":"smart","strategy":"custom","models":{"m":{"groups":["a"],"strategy":"price","token_id":3}}}`,
		`{"schema_version":1,"mode":"smart","strategy":"balanced","excluded_groups":["all"]}`,
		strings.Repeat(" ", MaxPolicyBytes+1),
	} {
		_, err := ParsePolicy([]byte(data))
		require.Error(t, err, data)
	}
	p, err := ParsePolicy([]byte(`{"schema_version":1,"mode":"smart","strategy":"custom","models":{"manual":{"groups":["z","a"],"strategy":"manual"}}}`))
	require.NoError(t, err)
	data, err := p.CanonicalJSON()
	require.NoError(t, err)
	parsed, err := ParsePolicy([]byte(data))
	require.NoError(t, err)
	assert.Equal(t, []string{"z", "a"}, parsed.Models["manual"].Groups)
	p.Models["manual"] = ModelPolicy{Groups: []string{"z", "a"}, Strategy: "speed"}
	data, err = p.CanonicalJSON()
	require.NoError(t, err)
	parsed, err = ParsePolicy([]byte(data))
	require.NoError(t, err)
	assert.Equal(t, []string{"a", "z"}, parsed.Models["manual"].Groups)
	assert.Equal(t, []string{"z", "a"}, p.Models["manual"].Groups, "normalization cannot mutate an editing draft")
	_, err = DefaultPolicy().CanonicalJSON()
	require.NoError(t, err)
}

func TestCustomGroupOfflinePreferencesAndSingleStrategy(t *testing.T) {
	valid := `{"schema_version":1,"mode":"smart","strategy":"custom","models":{"m":{"groups":["z","a"],"strategy":"manual","try_soft_offline_groups":["z"]},"n":{"groups":["b"],"strategy":"manual"}}}`
	p, err := ParsePolicy([]byte(valid))
	require.NoError(t, err)
	encoded, err := p.CanonicalJSON()
	require.NoError(t, err)
	require.Contains(t, encoded, `"try_soft_offline_groups":["z"]`)
	for _, invalid := range []string{
		strings.Replace(valid, `["z"]`, `["missing"]`, 1),
		strings.Replace(valid, `["z"]`, `["z","z"]`, 1),
		strings.Replace(valid, `"try_soft_offline_groups":["z"]`, `"try_soft_offline":true`, 1),
		strings.Replace(valid, `"strategy":"manual"`, `"strategy":"speed"`, 1),
	} {
		_, err := ParsePolicy([]byte(invalid))
		require.Error(t, err, invalid)
	}
}
