// Package catalogtest checks that a catalog.Catalog behaves as the interface
// documents, the way testing/slogtest checks a slog.Handler. The backend
// package's tests run it against every registered engine.
package catalogtest

import (
	"testing"

	"github.com/sandlayth/uvsh/internal/catalog"
)

// Opener returns a new, empty catalog that is closed when t ends.
type Opener func(t *testing.T) catalog.Catalog

func Run(t *testing.T, open Opener) {
	for _, tt := range []struct {
		name string
		test func(*testing.T, Opener)
	}{
		{"ScanWritesTree", testScanWritesTree},
		{"ScanBatches", testScanBatches},
		{"Rescan", testRescan},
		{"ScanRootsAreSeparate", testScanRootsAreSeparate},
		{"ScanAbortDiscardsOpenBatch", testScanAbortDiscardsOpenBatch},
		{"ScanFinishAfterCancel", testScanFinishAfterCancel},
		{"ScanRejects", testScanRejects},
		{"ScanLinks", testScanLinks},
		{"ScanKindChange", testScanKindChange},

		{"Missing", testMissing},
		{"MissingNeedsCompleteScan", testMissingNeedsCompleteScan},
		{"SkipKeepsWhatWasRecorded", testSkipKeepsWhatWasRecorded},

		{"ScanRootOverlap", testScanRootOverlap},
		{"ScanRootOverlapConcurrent", testScanRootOverlapConcurrent},
		{"Roots", testRoots},

		{"ListPages", testListPages},
		{"ListInsertBeforeCursor", testListInsertBeforeCursor},
		{"ListRejects", testListRejects},
		{"Stat", testStat},
		{"ReadSnapshot", testReadSnapshot},
	} {
		t.Run(tt.name, func(t *testing.T) { tt.test(t, open) })
	}
}
