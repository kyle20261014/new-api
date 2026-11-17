package model

import (
	"path/filepath"
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/schema"
)

func setupRoutingTaskListDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "task-list.db")), &gorm.Config{NamingStrategy: schema.NamingStrategy{TablePrefix: "union_test_"}})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
	require.NoError(t, db.AutoMigrate(&Task{}, &Midjourney{}, &RoutingSubmission{}, &RoutingFundingSession{}))
	for _, task := range []Task{
		{ID: 1, TaskID: "first", UserId: 1, ChannelId: 1, Platform: "video", Action: "generate", SubmitTime: 100, Status: TaskStatusSuccess, Data: []byte(`{"secret":"snapshot"}`)},
		{ID: 2, TaskID: "last", UserId: 1, ChannelId: 2, Platform: "audio", Action: "music", SubmitTime: 300, Status: TaskStatusQueued},
		{ID: 3, TaskID: "foreign", UserId: 2, ChannelId: 2, SubmitTime: 500},
	} {
		require.NoError(t, db.Create(&task).Error)
	}
	for _, s := range []RoutingSubmission{
		{SubmissionID: "unknown", UserID: 1, State: "unknown", CreatedAt: 200, UpdatedAt: 201, ChannelID: 1},
		{SubmissionID: "linked", UserID: 1, State: "accepted", LocalTaskID: 1, CreatedAt: 90, UpdatedAt: 400},
		{SubmissionID: "older-linked", UserID: 1, State: "accepted", LocalTaskID: 1, CreatedAt: 50, UpdatedAt: 50},
		{SubmissionID: "foreign-sub", UserID: 2, State: "unknown", CreatedAt: 400},
	} {
		s.RequestID, s.AttemptNo, s.FundingID = s.SubmissionID, 1, "fund-"+s.SubmissionID
		s.PriceSnapshot, s.RecoveryJSON, s.UpstreamTaskID = "private-price", "private-recovery", "private-upstream"
		require.NoError(t, db.Create(&s).Error)
		require.NoError(t, db.Create(&RoutingFundingSession{FundingID: s.FundingID, UserID: s.UserID, Status: "reserved"}).Error)
	}
	return db
}

func TestRoutingTaskListUnionPagesAndMergesLinkedSubmissions(t *testing.T) {
	db := setupRoutingTaskListDB(t)
	q := RoutingTaskListQuery{OwnerID: 1}
	var identities []string
	for start := 0; start < 4; start++ {
		rows, total, err := GetRoutingTaskList(db, start, 1, q)
		require.NoError(t, err)
		require.EqualValues(t, 3, total)
		if start == 3 {
			require.Empty(t, rows)
			continue
		}
		require.Len(t, rows, 1)
		row := rows[0]
		if row.Task != nil {
			identities = append(identities, row.Task.TaskID)
			assert.Empty(t, row.Task.Data)
			assert.Zero(t, row.Task.ChannelId)
		} else {
			identities = append(identities, row.Submission.SubmissionID)
		}
		if row.Submission != nil {
			assert.Empty(t, row.Submission.PriceSnapshot)
			assert.Empty(t, row.Submission.RecoveryJSON)
			assert.Empty(t, row.Submission.UpstreamTaskID)
		}
		if start == 2 {
			require.NotNil(t, row.Submission)
			assert.Equal(t, "linked", row.Submission.SubmissionID)
		}
	}
	assert.Equal(t, []string{"last", "unknown", "first"}, identities)
}

func TestRoutingTaskListAllExistingFiltersAndSubmissionStatus(t *testing.T) {
	db := setupRoutingTaskListDB(t)
	for _, tc := range []struct {
		name  string
		q     RoutingTaskListQuery
		count int64
	}{
		{"admin", RoutingTaskListQuery{}, 5},
		{"owner", RoutingTaskListQuery{OwnerID: 1}, 3},
		{"user", RoutingTaskListQuery{SyncTaskQueryParams: SyncTaskQueryParams{UserID: "2"}}, 2},
		{"users", RoutingTaskListQuery{SyncTaskQueryParams: SyncTaskQueryParams{UserIDs: []int{2}}}, 2},
		{"channel", RoutingTaskListQuery{OwnerID: 1, SyncTaskQueryParams: SyncTaskQueryParams{ChannelID: "2"}}, 1},
		{"platform", RoutingTaskListQuery{OwnerID: 1, SyncTaskQueryParams: SyncTaskQueryParams{Platform: "video"}}, 1},
		{"action", RoutingTaskListQuery{OwnerID: 1, SyncTaskQueryParams: SyncTaskQueryParams{Action: "music"}}, 1},
		{"task", RoutingTaskListQuery{OwnerID: 1, SyncTaskQueryParams: SyncTaskQueryParams{TaskID: "first"}}, 1},
		{"submission", RoutingTaskListQuery{OwnerID: 1, SubmissionID: "linked"}, 1},
		{"foreign submission", RoutingTaskListQuery{OwnerID: 1, SubmissionID: "foreign-sub"}, 0},
		{"unknown", RoutingTaskListQuery{OwnerID: 1, SyncTaskQueryParams: SyncTaskQueryParams{Status: "unknown"}}, 1},
		{"legacy unknown", RoutingTaskListQuery{OwnerID: 1, SyncTaskQueryParams: SyncTaskQueryParams{Status: "UNKNOWN"}}, 1},
		{"accepted", RoutingTaskListQuery{OwnerID: 1, SyncTaskQueryParams: SyncTaskQueryParams{Status: "accepted"}}, 1},
		{"success", RoutingTaskListQuery{OwnerID: 1, SyncTaskQueryParams: SyncTaskQueryParams{Status: "SUCCESS"}}, 1},
		{"time", RoutingTaskListQuery{OwnerID: 1, SyncTaskQueryParams: SyncTaskQueryParams{StartTimestamp: 150, EndTimestamp: 250}}, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rows, total, err := GetRoutingTaskList(db, 0, 10, tc.q)
			require.NoError(t, err)
			assert.Equal(t, tc.count, total)
			assert.Len(t, rows, int(tc.count))
		})
	}
	_, _, err := GetRoutingTaskList(db, 0, 10, RoutingTaskListQuery{SyncTaskQueryParams: SyncTaskQueryParams{Status: "made-up"}})
	require.ErrorContains(t, err, "Invalid task status")
}

func TestRoutingTaskListDatabaseFailuresAreErrors(t *testing.T) {
	for _, table := range []string{"union_test_tasks", "mowan_routing_submissions", "mowan_routing_funding_sessions"} {
		t.Run(table, func(t *testing.T) {
			db := setupRoutingTaskListDB(t)
			require.NoError(t, db.Migrator().DropTable(table))
			_, _, err := GetRoutingTaskList(db, 0, 10, RoutingTaskListQuery{OwnerID: 1})
			require.Error(t, err)
		})
	}
}

func TestRoutingTaskListMidjourneyLinkDoesNotCollideWithGenericTaskID(t *testing.T) {
	db := setupRoutingTaskListDB(t)
	require.NoError(t, db.Create(&Midjourney{Id: 1, UserId: 1, MjId: "mj-public", Action: "IMAGINE"}).Error)
	s := RoutingSubmission{SubmissionID: "mj-sub", RequestID: "mj-request", AttemptNo: 1, UserID: 1, FundingID: "mj-fund", State: "accepted", LocalTaskKind: "midjourney", LocalTaskID: 1, CreatedAt: 250, UpdatedAt: 600}
	require.NoError(t, db.Create(&s).Error)
	require.NoError(t, db.Create(&RoutingFundingSession{FundingID: s.FundingID, UserID: 1, Status: "reserved"}).Error)
	rows, total, err := GetRoutingTaskList(db, 0, 10, RoutingTaskListQuery{OwnerID: 1})
	require.NoError(t, err)
	require.EqualValues(t, 4, total)
	require.Nil(t, rows[1].Task)
	require.NotNil(t, rows[1].SubmissionTaskID)
	assert.Equal(t, "mj-public", *rows[1].SubmissionTaskID)
	assert.Equal(t, "linked", rows[3].Submission.SubmissionID)
	rows, total, err = GetRoutingTaskList(db, 0, 10, RoutingTaskListQuery{OwnerID: 1, SyncTaskQueryParams: SyncTaskQueryParams{TaskID: "mj-public", Platform: "mj", Action: "IMAGINE"}})
	require.NoError(t, err)
	require.EqualValues(t, 1, total)
	require.Len(t, rows, 1)
	assert.Nil(t, rows[0].Task)
	assert.Equal(t, "mj-sub", rows[0].Submission.SubmissionID)
	require.NoError(t, db.Model(&Midjourney{}).Where("id = ?", 1).Update("user_id", 2).Error)
	rows, _, err = GetRoutingTaskList(db, 0, 10, RoutingTaskListQuery{OwnerID: 1, SubmissionID: "mj-sub"})
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Nil(t, rows[0].SubmissionTaskID)
}
