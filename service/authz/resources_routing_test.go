package authz

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRoutingReconciliationPermission(t *testing.T) {
	require.NoError(t, Init(newAuthzTestDB(t)))
	assert.True(t, Can(1, common.RoleRootUser, RoutingReconcile))
	assert.False(t, Can(42, common.RoleAdminUser, RoutingReconcile))
	assert.False(t, Can(42, common.RoleCommonUser, RoutingReconcile))
	require.NoError(t, SetUserPermissions(42, PermissionsMap{ResourceRouting: {"reconcile": true}}))
	assert.True(t, Can(42, common.RoleAdminUser, RoutingReconcile))
	require.NoError(t, ClearUserAuthorization(42))
	assert.False(t, Can(42, common.RoleAdminUser, RoutingReconcile))
}
