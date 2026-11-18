package main

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/require"
)

func TestFundingPilotListenAddress(t *testing.T) {
	p := fundingPilotStartup{receipt: model.RoutingFundingPilotReceipt{Mode: model.RoutingFundingPilotMode, BindIP: "127.0.0.1"}}
	require.Equal(t, "127.0.0.1:3000", p.listenAddress("3000"))
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

func TestFundingPilotRequiresHostFenceBeforeDatabase(t *testing.T) {
	p := fundingPilotStartup{receipt: model.RoutingFundingPilotReceipt{Mode: model.RoutingFundingPilotMode}}
	require.ErrorIs(t, p.acquire(nil), model.ErrRoutingFundingMigrationBlocked)
}
