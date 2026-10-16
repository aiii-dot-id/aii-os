package continuity

import "time"

func (r *Runtime) ReadForTest(query string, now time.Time) (string, error) {
	return r.continuityRead(query, now)
}

func (r *Runtime) SnapshotKeyPathForTest() string {
	return r.snapshotKeyPath(r.w.Settings())
}

func PlanRestoreMovesForTest(cfg Settings, req RestoreRequest, set, staging string, through uint64, fp, label string) (RestoreJournal, error) {
	return planRestoreMoves(cfg, req, set, staging, through, fp, label)
}
