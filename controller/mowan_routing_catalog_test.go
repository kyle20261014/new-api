package controller

import (
	"net/http"
	"testing"

	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRoutingCatalogHTTPTrustBoundary(t *testing.T) {
	for _, query := range []string{"?p=-1", "?page_size=101", "?token_id=-2", "?category=all%7Ctext"} {
		c, w := newAuthenticatedContext(t, http.MethodGet, "/api/mowan/smart-routing/candidates"+query, nil, 1)
		GetRoutingCandidates(c)
		assert.Equal(t, http.StatusBadRequest, w.Code)
	}
	policy := map[string]any{"schema_version": 1, "mode": "smart", "strategy": "price"}
	for _, body := range []map[string]any{
		{"routing": policy, "model": "model", "user_id": 999},
		{"routing": policy, "model": "model", "request": map[string]any{"group": "unauthorized"}},
		{"routing": policy, "model": "model", "request": nil},
		{"routing": policy, "model": ""},
	} {
		c, w := newAuthenticatedContext(t, http.MethodPost, "/api/mowan/smart-routing/preview", body, 1)
		PreviewRouting(c)
		assert.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
	}
	db := setupTokenControllerTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.Token{}, &model.Channel{}, &model.Ability{}, &model.Option{}))
	require.NoError(t, model.MigrateRoutingMetrics(db))
	token := seedToken(t, db, 2, "foreign", "private-foreign-key")
	c, w := newAuthenticatedContext(t, http.MethodGet, "/api/mowan/smart-routing/candidates?token_id="+stringInt(token.Id), nil, 1)
	GetRoutingCandidates(c)
	assert.Equal(t, http.StatusForbidden, w.Code, w.Body.String())
	assert.NotContains(t, w.Body.String(), token.Key)
}
