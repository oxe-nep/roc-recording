package api

import (
	"log"
	"time"

	"github.com/roc-recording/backend/internal/capture"
	"github.com/roc-recording/backend/internal/commentator"
	"github.com/roc-recording/backend/internal/playout"
	"github.com/roc-recording/backend/internal/runtimestate"
	"github.com/roc-recording/backend/internal/srt"
	"github.com/roc-recording/backend/internal/tcloop"
	"github.com/roc-recording/backend/internal/workflow"
)

func applyWorkflowChange(
	id int,
	prev, cfg workflow.Config,
	mgr *capture.Manager,
	playMgr *playout.Manager,
	srtMgr *srt.Manager,
	tcMgr *tcloop.Manager,
	commMgr *commentator.Manager,
	runtimeStore *runtimestate.Store,
) {
	leavingRC := prev.Mode == workflow.ModeRemoteCommentator && cfg.Mode != workflow.ModeRemoteCommentator
	enteringRC := cfg.Mode == workflow.ModeRemoteCommentator && prev.Mode != workflow.ModeRemoteCommentator
	leavingTC := prev.Mode == workflow.ModeTC && cfg.Mode != workflow.ModeTC
	enteringTC := cfg.Mode == workflow.ModeTC && prev.Mode != workflow.ModeTC

	if leavingRC && commMgr != nil {
		commMgr.Stop(id)
	}

	if leavingTC && tcMgr != nil {
		if tcMgr.IsEnabled(id) || tcMgr.IsRunning(id) {
			disabled := false
			if _, err := tcMgr.Update(id, tcloop.UpdateInput{Enabled: &disabled}); err != nil {
				log.Printf("[workflow] channel %d: stop TC: %v", id, err)
			}
		}
	}

	if enteringRC && commMgr != nil {
		releaseChannelPair(id, mgr, playMgr, srtMgr, tcMgr, runtimeStore)
		commMgr.EnsureChannel(id)
		commMgr.Enable(id)
		log.Printf("[workflow] channel %d: remote commentator enabled", id)
	}

	if enteringTC && tcMgr != nil {
		releaseChannelPair(id, mgr, playMgr, srtMgr, tcMgr, runtimeStore)
		if commMgr != nil && commMgr.IsActive(id) {
			commMgr.Stop(id)
		}
		tcMgr.EnsureChannel(id)
		enabled := true
		if _, err := tcMgr.Update(id, tcloop.UpdateInput{Enabled: &enabled}); err != nil {
			log.Printf("[workflow] channel %d: auto-start TC: %v", id, err)
		}
	}

	if cfg.Mode == workflow.ModePair && (leavingTC || leavingRC) {
		restartEncodeAfterWorkflow(id, mgr, runtimeStore)
	}
}

func releaseChannelPair(
	id int,
	mgr *capture.Manager,
	playMgr *playout.Manager,
	srtMgr *srt.Manager,
	tcMgr *tcloop.Manager,
	runtimeStore *runtimestate.Store,
) {
	if tcMgr != nil && (tcMgr.IsEnabled(id) || tcMgr.IsRunning(id)) {
		disabled := false
		if _, err := tcMgr.Update(id, tcloop.UpdateInput{Enabled: &disabled}); err != nil {
			log.Printf("[workflow] channel %d: stop TC during release: %v", id, err)
		}
	}
	if mgr != nil {
		_ = mgr.Stop(id)
	}
	if playMgr != nil {
		_, _ = playMgr.Stop(id)
	}
	if srtMgr != nil {
		if info, err := srtMgr.Get(id); err == nil && info.Status == srt.StatusStreaming {
			_, _ = srtMgr.Stop(id)
		}
	}
	if runtimeStore != nil {
		runtimeStore.SetCapture(id, false)
		runtimeStore.SetPlayout(id, false)
	}
}

func restartEncodeAfterWorkflow(id int, mgr *capture.Manager, runtimeStore *runtimestate.Store) {
	if mgr == nil {
		return
	}
	var startErr error
	for attempt := 0; attempt < 3; attempt++ {
		if mgr.IsActive(id) {
			startErr = nil
			break
		}
		startErr = mgr.Start(id)
		if startErr == nil {
			break
		}
		log.Printf("[workflow] channel %d: start encode after workflow (attempt %d): %v",
			id, attempt+1, startErr)
		time.Sleep(2 * time.Second)
	}
	if startErr != nil {
		log.Printf("[workflow] channel %d: failed to restart encode after workflow: %v", id, startErr)
	} else if runtimeStore != nil {
		runtimeStore.SetCapture(id, true)
	}
}
