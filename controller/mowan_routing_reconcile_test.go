package controller

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/service/authz"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRoutingReconcileSessionPATPermissionBoundary(t *testing.T) {
	user, _ := setupAccessTokenAudit(t)
	_, jwt := createAccessTokenTestSession(t, user.Id, "reconcile-permission-session")
	readPAT, _ := createScopedAccessToken(t, user.Id, time.Now().Add(time.Hour).Unix(), "api_key:read")
	reconcilePAT, _ := createScopedAccessToken(t, user.Id, time.Now().Add(time.Hour).Unix(), service.AccessTokenScopeOf(authz.RoutingReconcile))
	router := gin.New()
	router.Use(middleware.RequestId(), middleware.AccessTokenAudit())
	path := "/api/mowan/smart-routing/submissions/:submission_id/reconcile"
	middleware.DeclareAccessTokenPermissionRoute("POST", path, authz.RoutingReconcile)
	router.POST(path, middleware.TokenOperationAudit(), middleware.AdminAuth(), middleware.RequirePermission(authz.RoutingReconcile), ReconcileRoutingSubmission)
	for _, tc := range []struct {
		name, credential string
		role, status     int
		grant            bool
	}{
		{"common session", jwt, common.RoleCommonUser, 403, false},
		{"common PAT", reconcilePAT, common.RoleCommonUser, 403, true},
		{"admin session without permission", jwt, common.RoleAdminUser, 403, false},
		{"admin PAT without permission", reconcilePAT, common.RoleAdminUser, 403, false},
		{"admin session granted", jwt, common.RoleAdminUser, 400, true},
		{"admin PAT granted", reconcilePAT, common.RoleAdminUser, 400, true},
		{"admin read PAT cannot reconcile", readPAT, common.RoleAdminUser, 403, true},
		{"root session", jwt, common.RoleRootUser, 400, false},
		{"root read PAT cannot reconcile", readPAT, common.RoleRootUser, 403, false},
		{"root reconcile PAT", reconcilePAT, common.RoleRootUser, 400, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.NoError(t, model.DB.Model(user).Update("role", tc.role).Error)
			require.NoError(t, authz.SetUserPermissions(user.Id, authz.PermissionsMap{authz.ResourceRouting: {"reconcile": tc.grant}}))
			response := accessTokenRequest(router, "POST", "/api/mowan/smart-routing/submissions/test/reconcile", tc.credential, "", `{"reason":"private-reconcile-reason"}`)
			require.Equal(t, tc.status, response.Code, response.Body.String())
			var events []model.AuditLog
			require.NoError(t, model.LOG_DB.Where("request_id = ?", response.Header().Get(common.RequestIdKey)).Find(&events).Error)
			require.NotEmpty(t, events, "all reconciliation denials must be audited, including pre-auth role rejection")
			if tc.role == common.RoleCommonUser {
				require.Len(t, events, 1, "pre-auth rejection must not duplicate the PAT access audit")
				assert.Equal(t, user.Id, events[0].UserId)
				assert.Equal(t, tc.role, events[0].ActorRole)
			}
			for _, event := range events {
				assert.False(t, event.Success)
				assert.Equal(t, tc.status, event.Status)
			}
			encoded, err := common.Marshal(events)
			require.NoError(t, err)
			for _, secret := range []string{jwt, readPAT, reconcilePAT, "private-reconcile-reason"} {
				assert.NotContains(t, string(encoded), secret)
				assert.NotContains(t, response.Body.String(), secret)
			}
		})
	}
}

func TestRoutingReconcileNarrowProofCASAndAudit(t *testing.T) {
	db := setupTokenControllerTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.AuditLog{}))
	require.NoError(t, model.MigrateRoutingFunding(db))
	require.NoError(t, model.MigrateRoutingMetrics(db))
	require.NoError(t, db.Model(&model.User{}).Where("id = 1").Update("quota", 100).Error)
	token := seedToken(t, db, 1, "reconcile-key", "private-reconciliation-bearer")
	s := &model.RoutingSubmission{SubmissionID: "reconciliation-http", RequestID: "request-http", AttemptNo: 1, ClientModel: "model", BusinessGroup: "paid", ChannelID: 1, ProtocolClass: "async", ProviderKey: "test:v1", OperationKey: "image.generate"}
	_, err := model.PrepareRoutingFunding(model.RoutingFundingRequest{FundingID: "fund-http", OperationID: "reserve-http", UserID: 1, TokenID: token.Id, Source: "wallet", Kind: "reserve", Quota: 30}, s)
	require.NoError(t, err)
	s, err = model.MarkRoutingSubmissionSending(s.SubmissionID, s.Version, s.SendOwnerID)
	require.NoError(t, err)
	s, err = model.RecordRoutingSubmissionOutcome(s.SubmissionID, s.Version, "unknown", "", nil, s.SendOwnerID)
	require.NoError(t, err)
	proof := map[string]any{"request_id": s.RequestID, "channel_id": s.ChannelID, "provider_key": s.ProviderKey, "operation_key": s.OperationKey, "outcome": "not_sent", "observed_at": common.GetTimestamp(), "evidence_ref": "private-reviewed-evidence", "confirmed": true}
	body := map[string]any{"expected_version": s.Version, "reason": "private-reviewed-reason", "outcome": "not_sent", "proof": proof}
	request := func(role int, fields map[string]any) *httptest.ResponseRecorder {
		data, err := common.Marshal(fields)
		require.NoError(t, err)
		r := gin.New()
		r.Use(func(c *gin.Context) { c.Set("id", 1); c.Set("role", role); c.Next() })
		r.POST("/submissions/:submission_id/reconcile", middleware.RequirePermission(authz.RoutingReconcile), ReconcileRoutingSubmission)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/submissions/"+s.SubmissionID+"/reconcile", bytes.NewReader(data)))
		return w
	}
	assert.Equal(t, http.StatusForbidden, request(common.RoleCommonUser, body).Code)
	body["actor"] = 999
	assert.Equal(t, http.StatusBadRequest, request(common.RoleRootUser, body).Code)
	delete(body, "actor")
	body["recovery"] = map[string]any{"task": "must never reach the server"}
	assert.Equal(t, http.StatusBadRequest, request(common.RoleRootUser, body).Code)
	delete(body, "recovery")
	proof["channel_id"] = 99
	assert.Equal(t, http.StatusBadRequest, request(common.RoleRootUser, body).Code)
	proof["channel_id"] = s.ChannelID
	result := request(common.RoleRootUser, body)
	require.Equal(t, http.StatusOK, result.Code, result.Body.String())
	assert.NotContains(t, result.Body.String(), "private-")
	require.Equal(t, http.StatusOK, request(common.RoleRootUser, body).Code, "exact replay must not refund twice")
	var user model.User
	require.NoError(t, db.First(&user, 1).Error)
	assert.Equal(t, 100, user.Quota)
	body["reason"] = "different-proof"
	assert.Equal(t, http.StatusConflict, request(common.RoleRootUser, body).Code)
	var audits []model.AuditLog
	require.NoError(t, db.Find(&audits).Error)
	require.NotEmpty(t, audits)
	for _, audit := range audits {
		data, err := common.Marshal(audit)
		require.NoError(t, err)
		assert.NotContains(t, string(data), "private-", "audit includes only allowlisted identifiers and versions")
	}
}
