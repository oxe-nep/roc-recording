package bootstrap

import (
	"log"
	"time"

	"github.com/roc-recording/backend/internal/playout"
	"github.com/roc-recording/backend/internal/recording"
	"github.com/roc-recording/backend/internal/runtimestate"
	"github.com/roc-recording/backend/internal/srt"
	"github.com/roc-recording/backend/internal/tcloop"
)

// RestoreRuntime replays decode/SRT/recording channels that were running before restart.
func RestoreRuntime(
	store *runtimestate.Store,
	playMgr *playout.Manager,
	srtMgr *srt.Manager,
	recMgr *recording.Manager,
	tcMgr *tcloop.Manager,
) {
	if store == nil {
		return
	}

	for _, id := range store.PlayoutIDs() {
		if tcMgr != nil && (tcMgr.IsEnabled(id) || tcMgr.IsRunning(id)) {
			log.Printf("[runtime] skip playout restore ch %d (TC burn-in active)", id)
			continue
		}
		if _, err := playMgr.Start(id); err != nil {
			log.Printf("[runtime] playout restore ch %d: %v", id, err)
		} else {
			log.Printf("[runtime] restored playout ch %d", id)
		}
		time.Sleep(300 * time.Millisecond)
	}

	for _, id := range store.SRTIDs() {
		if _, err := srtMgr.Start(id); err != nil {
			log.Printf("[runtime] SRT restore ch %d: %v", id, err)
		} else {
			log.Printf("[runtime] restored SRT ch %d", id)
		}
		time.Sleep(300 * time.Millisecond)
	}

	for _, id := range store.RecordingIDs() {
		if _, err := recMgr.Start(id); err != nil {
			log.Printf("[runtime] recording restore ch %d: %v", id, err)
		} else {
			log.Printf("[runtime] restored recording ch %d", id)
		}
		time.Sleep(300 * time.Millisecond)
	}
}
