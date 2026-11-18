package main

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/require"
)

func TestFundingPilotRuntimeLimits(t *testing.T) {
	p := fundingPilotStartup{receipt: model.RoutingFundingPilotReceipt{Mode: model.RoutingFundingPilotMode, BindIP: "127.0.0.1", MaxInFlight: 1}}
	require.Equal(t, "127.0.0.1:3000", p.listenAddress("3000"))
	started, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	handler := p.limitHTTP(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/held" {
			close(started)
			<-release
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	go func() {
		defer close(done)
		handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/held", nil))
	}()
	<-started
	busy := httptest.NewRecorder()
	handler.ServeHTTP(busy, httptest.NewRequest(http.MethodGet, "/api/status", nil))
	close(release)
	<-done
	require.Equal(t, http.StatusServiceUnavailable, busy.Code)
	require.Equal(t, "1", busy.Header().Get("Retry-After"))
	ready := httptest.NewRecorder()
	handler.ServeHTTP(ready, httptest.NewRequest(http.MethodGet, "/api/status", nil))
	require.Equal(t, http.StatusNoContent, ready.Code)
}

func TestFundingPilotRejectsBatchAndWrongBuild(t *testing.T) {
	oldMaster := common.IsMasterNode
	common.IsMasterNode = true
	t.Cleanup(func() { common.IsMasterNode = oldMaster })
	t.Setenv("ENABLE_PPROF", "false")
	t.Setenv("BATCH_UPDATE_ENABLED", "true")
	var p fundingPilotStartup
	require.ErrorIs(t, p.configure("", "test", pilotBuildCommit(), 1), model.ErrRoutingFundingMigrationBlocked)
	t.Setenv("BATCH_UPDATE_ENABLED", "false")
	require.ErrorIs(t, p.configure("", "test", "wrong-build", 1), model.ErrRoutingFundingMigrationBlocked)
}
