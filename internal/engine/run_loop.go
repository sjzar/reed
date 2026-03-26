package engine

import (
	"os"
	"path/filepath"
	"time"

	"github.com/sjzar/reed/internal/model"
)

const newGraceTimeout = 3 * time.Second

// loop is the event-driven owner loop for a run.
// doneCh is NOT closed here — it is closed by rt.onRunDone after terminalization.
func (rs *runState) loop() {
	// Create run-scoped temp dir; cleaned up when loop exits.
	rs.runTempDir = filepath.Join(os.TempDir(), "reedruns", rs.id)
	if err := os.MkdirAll(rs.runTempDir, 0o755); err != nil {
		rs.runTempDir = ""
	}
	defer os.RemoveAll(rs.runTempDir)

	rs.setStatus(model.RunStarting)
	rs.initStepRuns()
	rs.dispatchReady()
	rs.setStatus(model.RunRunning)

	// If all steps were synchronously resolved (skip/fail), we're already terminal.
	if rs.isTerminal() {
		rs.drain()
		return
	}
	if rs.isStuck() {
		rs.cancelAllPending()
		rs.drain()
		return
	}

	graceTimer := time.NewTimer(time.Hour)
	graceTimer.Stop()
	defer graceTimer.Stop()

	// Run-level timeout: if set, fires stop after deadline.
	var runTimeoutCh <-chan time.Time
	if rs.request.Timeout > 0 {
		runTimeout := time.NewTimer(rs.request.Timeout)
		defer runTimeout.Stop()
		runTimeoutCh = runTimeout.C
	}

	for {
		select {
		case <-rs.stopCh:
			rs.onStopRequested(graceTimer)

		case res := <-rs.stepRunResultCh:
			rs.applyStepRunResult(res)
			if rs.isTerminal() {
				rs.drain()
				return
			}
			rs.dispatchReady()
			// Re-check terminal: dispatchReady may have synchronously
			// failed/skipped the last pending steps (e.g. render error).
			if rs.isTerminal() {
				rs.drain()
				return
			}
			if rs.isStuck() {
				rs.cancelAllPending()
				rs.drain()
				return
			}

		case <-runTimeoutCh:
			runTimeoutCh = nil // prevent re-firing
			rs.onStopRequested(graceTimer)

		case <-graceTimer.C:
			rs.cancelAllPending()
			rs.drain()
			return

		case <-rs.rootCtx.Done():
			if !rs.isTerminal() {
				rs.cancelAllPending()
			}
			rs.drain()
			return
		}
	}
}

// drain closes drainCh to unblock stuck senders, waits for all workers, then finalizes.
func (rs *runState) drain() {
	close(rs.drainCh)
	rs.wg.Wait()
	rs.finalize()
}

// onStopRequested implements two-phase graceful stop:
//
//  1. Cancel rootCtx → workers receive ctx.Done and should exit promptly.
//     Worker results flow back via stepRunResultCh and are processed normally.
//  2. Start graceTimer (3s) → if workers haven't returned results by then,
//     graceTimer.C fires → cancelAllPending marks remaining steps SKIPPED/CANCELED.
//
// After rootCancel(), rootCtx.Done is ready in the select. Go's select picks
// a random ready case, so stepRunResultCh (worker results) competes fairly.
// Fast-responding workers get their results processed normally; graceTimer
// is the backstop for unresponsive workers.
func (rs *runState) onStopRequested(graceTimer *time.Timer) {
	rs.stateMu.Lock()
	rs.stopRequested = true
	rs.stateMu.Unlock()
	rs.setStatus(model.RunStopping)
	rs.emitRunEvent(EventStopRequested, "graceful stop requested")
	rs.rootCancel()
	graceTimer.Reset(newGraceTimeout)
}
