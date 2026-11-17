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
	p, err := ParsePolicy([]byte(`{"schema_version":1,"mode":"smart","strategy":"custom","models":{"manual":{"groups":["z","a"],"strategy":"manual"},"dynamic":{"groups":["z","a"],"strategy":"speed"}}}`))
	require.NoError(t, err)
	data, err := p.CanonicalJSON()
	require.NoError(t, err)
	parsed, err := ParsePolicy([]byte(data))
	require.NoError(t, err)
	assert.Equal(t, []string{"z", "a"}, parsed.Models["manual"].Groups)
	assert.Equal(t, []string{"a", "z"}, parsed.Models["dynamic"].Groups)
	assert.Equal(t, []string{"z", "a"}, p.Models["dynamic"].Groups, "normalization cannot mutate an editing draft")
	_, err = DefaultPolicy().CanonicalJSON()
	require.NoError(t, err)
}
