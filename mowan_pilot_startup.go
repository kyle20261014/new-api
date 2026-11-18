package main

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"runtime/debug"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"gorm.io/gorm"
)

// Stamped by the release build when .git is unavailable in the build context.
var fundingPilotBuildCommit string

type fundingPilotStartup struct {
	receipt model.RoutingFundingPilotReceipt
	lock    *model.RoutingFundingPilotLock
	stop    context.CancelFunc
	done    chan struct{}
}

func pilotBuildCommit() string {
	if build, ok := debug.ReadBuildInfo(); ok {
		var revision string
		for _, s := range build.Settings {
			if s.Key == "vcs.modified" && s.Value == "true" {
				return ""
			}
			if s.Key == "vcs.revision" {
				revision = s.Value
			}
		}
		if revision != "" {
			return revision
		}
	}
	return fundingPilotBuildCommit
}

func (p *fundingPilotStartup) configure(raw, deployment, commit string, nodes int) error {
	if !common.IsMasterNode || common.BatchUpdateEnabled || os.Getenv("BATCH_UPDATE_ENABLED") == "true" || os.Getenv("ENABLE_PPROF") == "true" || commit == "" || commit != pilotBuildCommit() {
		return fmt.Errorf("%w: pilot requires the reviewed build, one master, and no batch updater or profiling listener", model.ErrRoutingFundingMigrationBlocked)
	}
	var err error
	p.receipt, err = model.ParseRoutingFundingPilotReceipt(raw, deployment, commit, nodes)
	return err
}

func (p *fundingPilotStartup) acquire(db *gorm.DB) error {
	if p.receipt.Mode != model.RoutingFundingPilotMode {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	lock, err := model.AcquireRoutingFundingPilotLock(ctx, db, p.receipt)
	if err != nil {
		return err
	}
	if !lock.Held(ctx) {
		lock.Close()
		return fmt.Errorf("%w: pilot requires a dedicated PostgreSQL session", model.ErrRoutingFundingMigrationBlocked)
	}
	p.lock = lock
	watch, stop := context.WithCancel(context.Background())
	p.stop, p.done = stop, make(chan struct{})
	go func() {
		defer close(p.done)
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-watch.Done():
				return
			case <-ticker.C:
				check, cancel := context.WithTimeout(watch, time.Second)
				held := lock.Held(check)
				cancel()
				if !held && watch.Err() == nil {
					common.FatalLog(common.LogText("funding pilot writer lock lost; stopping gateway"))
				}
			}
		}
	}()
	return nil
}

func (p *fundingPilotStartup) close() {
	p.stop()
	<-p.done
	p.lock.Close()
}

func (p *fundingPilotStartup) listenAddress(port string) string {
	return net.JoinHostPort(p.receipt.BindIP, port)
}

func (p *fundingPilotStartup) limitHTTP(next http.Handler) http.Handler {
	if p.receipt.Mode != model.RoutingFundingPilotMode {
		return next
	}
	// ponytail: one shared in-flight ceiling; isolate provider callbacks at the
	// proxy first if pilot requests routinely exhaust the reviewed limit.
	slots := make(chan struct{}, p.receipt.MaxInFlight)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case slots <- struct{}{}:
			defer func() { <-slots }()
			next.ServeHTTP(w, r)
		default:
			w.Header().Set("Retry-After", "1")
			http.Error(w, http.StatusText(http.StatusServiceUnavailable), http.StatusServiceUnavailable)
		}
	})
}
