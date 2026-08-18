package runtime

import (
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/guregu/null.v4"

	"github.com/flatcar/nebraska/backend/pkg/api/types"
)

func retentionFixture(t *testing.T, a interface{ Close() }, as interface {
	AddTeam(*types.Team) (*types.Team, error)
	AddApp(*types.Application) (*types.Application, error)
	AddPackage(*types.Package) (*types.Package, error)
	AddChannel(*types.Channel) (*types.Channel, error)
	AddGroup(*types.Group) (*types.Group, error)
}, rs *Service, n int) (*types.Group, []string) {
	t.Helper()

	tTeam, err := as.AddTeam(&types.Team{Name: "retention_team"})
	require.NoError(t, err)
	tApp, err := as.AddApp(&types.Application{Name: "retention_app", TeamID: tTeam.ID})
	require.NoError(t, err)
	tPkg, err := as.AddPackage(&types.Package{Type: types.PkgTypeOther, URL: "http://sample.url/pkg", Version: "1.0.0", ApplicationID: tApp.ID})
	require.NoError(t, err)
	tChannel, err := as.AddChannel(&types.Channel{Name: "retention_channel", Color: "blue", ApplicationID: tApp.ID, PackageID: null.StringFrom(tPkg.ID)})
	require.NoError(t, err)
	tGroup, err := as.AddGroup(&types.Group{
		Name: "retention_group", ApplicationID: tApp.ID, ChannelID: null.StringFrom(tChannel.ID),
		PolicyUpdatesEnabled: true, PolicyPeriodInterval: "15 minutes",
		PolicyMaxUpdatesPerPeriod: 2, PolicyUpdateTimeout: "60 minutes",
	})
	require.NoError(t, err)

	ids := make([]string, 0, n)
	for i := 0; i < n; i++ {
		id := uuid.New().String()
		_, err := rs.RegisterInstance(
			types.Instance{ID: id, IP: fmt.Sprintf("10.0.1.%d", i+1)},
			NewInstanceApplication(tApp.ID, tGroup.ID, "1.0.0"),
		)
		require.NoError(t, err)
		ids = append(ids, id)
	}

	return tGroup, ids
}

func (s *Service) ageInstance(t *testing.T, instanceID string, d time.Duration) {
	t.Helper()
	_, err := s.db.Exec(
		`UPDATE instance_application SET last_check_for_updates = now() - $2::interval
		 WHERE instance_id = $1`,
		instanceID, postgresInterval(d))
	require.NoError(t, err)
}

func (s *Service) countInstances(t *testing.T) int {
	t.Helper()
	var n int
	require.NoError(t, s.db.QueryRow(`SELECT count(*) FROM instance`).Scan(&n))
	return n
}

func TestPruneOldDataDisabledByDefault(t *testing.T) {
	a := newForTest(t)
	defer a.Close()
	rs := runtimeSvc(a)

	_, ids := retentionFixture(t, a, adminSvc(a), rs, 3)
	rs.ageInstance(t, ids[0], 400*24*time.Hour)

	before := rs.countInstances(t)

	report, err := rs.PruneOldData(RetentionConfig{})
	require.NoError(t, err)
	assert.Equal(t, int64(0), report.Total())
	assert.Equal(t, before, rs.countInstances(t), "nothing may be deleted when retention is off")
}

func TestPruneOldDataDryRunDeletesNothing(t *testing.T) {
	a := newForTest(t)
	defer a.Close()
	rs := runtimeSvc(a)

	_, ids := retentionFixture(t, a, adminSvc(a), rs, 4)
	for _, id := range ids[:3] {
		rs.ageInstance(t, id, 200*24*time.Hour)
	}

	before := rs.countInstances(t)

	report, err := rs.PruneOldData(RetentionConfig{Instances: 90 * 24 * time.Hour, DryRun: true})
	require.NoError(t, err)

	assert.True(t, report.DryRun)
	assert.Equal(t, int64(3), report.Instances, "should report the three abandoned instances")
	assert.Equal(t, before, rs.countInstances(t), "dry run must not delete anything")
}

func TestPruneOldDataRemovesAbandonedInstances(t *testing.T) {
	a := newForTest(t)
	defer a.Close()
	rs := runtimeSvc(a)

	_, ids := retentionFixture(t, a, adminSvc(a), rs, 5)
	for _, id := range ids[:2] {
		rs.ageInstance(t, id, 200*24*time.Hour)
	}

	before := rs.countInstances(t)

	report, err := rs.PruneOldData(RetentionConfig{Instances: 90 * 24 * time.Hour})
	require.NoError(t, err)
	assert.Equal(t, int64(2), report.Instances)
	assert.Equal(t, before-2, rs.countInstances(t))

	for _, id := range ids[2:] {
		var n int
		require.NoError(t, rs.db.QueryRow(`SELECT count(*) FROM instance WHERE id = $1`, id).Scan(&n))
		assert.Equal(t, 1, n, "a live instance must not be pruned")
	}

	report, err = rs.PruneOldData(RetentionConfig{Instances: 90 * 24 * time.Hour})
	require.NoError(t, err)
	assert.Equal(t, int64(0), report.Instances)
}

func TestPruneOldDataCascadesToInstanceHistory(t *testing.T) {
	a := newForTest(t)
	defer a.Close()
	rs := runtimeSvc(a)

	tGroup, ids := retentionFixture(t, a, adminSvc(a), rs, 2)
	doomed := ids[0]

	require.NoError(t, rs.updateInstanceStatus(doomed, tGroup.ApplicationID, types.InstanceStatusDownloading))
	require.NoError(t, rs.updateInstanceStatus(doomed, tGroup.ApplicationID, types.InstanceStatusComplete))

	var historyBefore int
	require.NoError(t, rs.db.QueryRow(
		`SELECT count(*) FROM instance_status_history WHERE instance_id = $1`, doomed).Scan(&historyBefore))
	require.Positive(t, historyBefore, "fixture should have produced status history")

	rs.ageInstance(t, doomed, 200*24*time.Hour)

	_, err := rs.PruneOldData(RetentionConfig{Instances: 90 * 24 * time.Hour})
	require.NoError(t, err)

	var historyAfter, appRows int
	require.NoError(t, rs.db.QueryRow(
		`SELECT count(*) FROM instance_status_history WHERE instance_id = $1`, doomed).Scan(&historyAfter))
	require.NoError(t, rs.db.QueryRow(
		`SELECT count(*) FROM instance_application WHERE instance_id = $1`, doomed).Scan(&appRows))

	assert.Zero(t, historyAfter, "status history should cascade away with the instance")
	assert.Zero(t, appRows, "instance_application should cascade away with the instance")
}

func TestPruneOldDataBatching(t *testing.T) {
	a := newForTest(t)
	defer a.Close()
	rs := runtimeSvc(a)

	_, ids := retentionFixture(t, a, adminSvc(a), rs, 7)
	for _, id := range ids {
		rs.ageInstance(t, id, 200*24*time.Hour)
	}

	before := rs.countInstances(t)

	report, err := rs.PruneOldData(RetentionConfig{
		Instances: 90 * 24 * time.Hour,
		BatchSize: 2,
	})
	require.NoError(t, err)
	assert.Equal(t, int64(7), report.Instances, "batching must not lose or double-count rows")

	assert.Equal(t, before-7, rs.countInstances(t))
	for _, id := range ids {
		var n int
		require.NoError(t, rs.db.QueryRow(`SELECT count(*) FROM instance WHERE id = $1`, id).Scan(&n))
		assert.Zero(t, n, "every aged instance should be gone")
	}
}

func TestPostgresInterval(t *testing.T) {
	assert.Equal(t, "3600 seconds", postgresInterval(time.Hour))
	assert.Equal(t, "7776000 seconds", postgresInterval(90*24*time.Hour))
	assert.Equal(t, "0 seconds", postgresInterval(0))
}
