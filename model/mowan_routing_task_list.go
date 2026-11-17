package model

import (
	"errors"
	"fmt"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

type RoutingTaskListQuery struct {
	SyncTaskQueryParams
	OwnerID      int
	SubmissionID string
}

type RoutingTaskListRow struct {
	Task             *Task
	Submission       *RoutingSubmission
	FundingStatus    string
	SubmissionTaskID *string
}

type routingTaskIdentity struct {
	TaskPK       int64
	SubmissionID string
	SortTime     int64
}

// GetRoutingTaskList pages identities in SQL before loading bounded public data.
func GetRoutingTaskList(db *gorm.DB, start, limit int, q RoutingTaskListQuery) ([]RoutingTaskListRow, int64, error) {
	if db == nil || start < 0 || limit < 1 || limit > 1000 || q.OwnerID < 0 {
		return nil, 0, common.NewMessage("Invalid task query")
	}
	rows := make([]RoutingTaskListRow, 0)
	var total int64
	err := db.Transaction(func(tx *gorm.DB) error {
		taskTable := tx.Statement.Quote(tx.NamingStrategy.TableName("Task"))
		tasks := tx.Table(taskTable + " AS t").Select("t.id AS task_pk, COALESCE(s.submission_id, '') AS submission_id, t.submit_time AS sort_time").Joins(`LEFT JOIN mowan_routing_submissions s ON s.local_task_id = t.id AND s.user_id = t.user_id AND (s.local_task_kind IS NULL OR s.local_task_kind IN ('', 'task')) AND NOT EXISTS (SELECT 1 FROM mowan_routing_submissions newer WHERE newer.local_task_id = t.id AND newer.user_id = t.user_id AND (newer.local_task_kind IS NULL OR newer.local_task_kind IN ('', 'task')) AND (newer.updated_at > s.updated_at OR (newer.updated_at = s.updated_at AND newer.submission_id > s.submission_id)))`)
		submissions := tx.Table("mowan_routing_submissions AS s").Select("0 AS task_pk, s.submission_id, s.created_at AS sort_time").Where("NOT EXISTS (SELECT 1 FROM " + taskTable + " linked WHERE linked.id = s.local_task_id AND linked.user_id = s.user_id AND (s.local_task_kind IS NULL OR s.local_task_kind IN ('', 'task')))")
		for _, branch := range []struct {
			query                **gorm.DB
			owner, time, channel string
		}{
			{&tasks, "t.user_id", "t.submit_time", "t.channel_id"}, {&submissions, "s.user_id", "s.created_at", "s.channel_id"},
		} {
			query := *branch.query
			if q.OwnerID > 0 {
				query = query.Where(branch.owner+" = ?", q.OwnerID)
			}
			if q.UserID != "" {
				query = query.Where(branch.owner+" = ?", q.UserID)
			}
			if len(q.UserIDs) > 0 {
				query = query.Where(branch.owner+" IN ?", q.UserIDs)
			}
			if q.ChannelID != "" {
				query = query.Where(branch.channel+" = ?", q.ChannelID)
			}
			if q.StartTimestamp != 0 {
				query = query.Where(branch.time+" >= ?", q.StartTimestamp)
			}
			if q.EndTimestamp != 0 {
				query = query.Where(branch.time+" <= ?", q.EndTimestamp)
			}
			if q.SubmissionID != "" {
				query = query.Where("s.submission_id = ?", q.SubmissionID)
			}
			*branch.query = query
		}
		if q.TaskID != "" {
			tasks = tasks.Where("t.task_id = ?", q.TaskID)
		}
		if q.Platform != "" {
			tasks = tasks.Where("t.platform = ?", q.Platform)
		}
		if q.Action != "" {
			tasks = tasks.Where("t.action = ?", q.Action)
		}
		// Unlinked journals have no task identity; linked MJ identities remain journals.
		if q.TaskID != "" || q.Platform != "" || q.Action != "" {
			if q.Platform != "" && q.Platform != "mj" {
				submissions = submissions.Where("1 = 0")
			} else {
				mj := tx.Model(&Midjourney{}).Select("1").Where("id = s.local_task_id AND user_id = s.user_id")
				if q.TaskID != "" {
					mj = mj.Where("mj_id = ?", q.TaskID)
				}
				if q.Action != "" {
					mj = mj.Where("action = ?", q.Action)
				}
				submissions = submissions.Where("s.local_task_kind = ? AND EXISTS (?)", "midjourney", mj)
			}
		}
		switch q.Status {
		case "":
		case "prepared", "sending", "accepted", "unknown", "not_sent", "rejected":
			tasks = tasks.Where("s.state = ?", q.Status)
			submissions = submissions.Where("s.state = ?", q.Status)
		case string(TaskStatusUnknown):
			tasks = tasks.Where("t.status = ?", q.Status)
			submissions = submissions.Where("s.state = ?", "unknown")
		case string(TaskStatusNotStart), TaskStatusSubmitted, TaskStatusQueued, TaskStatusInProgress, TaskStatusFailure, TaskStatusSuccess:
			tasks = tasks.Where("t.status = ?", q.Status)
			submissions = submissions.Where("1 = 0")
		default:
			return common.NewMessage("Invalid task status")
		}
		union := tx.Raw("? UNION ALL ?", tasks, submissions)
		if err := tx.Table("(?) AS routing_items", union).Count(&total).Error; err != nil {
			return fmt.Errorf("count routing task list: %w", err)
		}
		var page []routingTaskIdentity
		if err := tx.Table("(?) AS routing_items", union).Order("sort_time DESC, task_pk DESC, submission_id DESC").Offset(start).Limit(limit).Find(&page).Error; err != nil {
			return fmt.Errorf("page routing task list: %w", err)
		}
		if len(page) == 0 {
			return nil
		}
		var taskIDs []int64
		var submissionIDs []string
		for _, identity := range page {
			if identity.TaskPK > 0 {
				taskIDs = append(taskIDs, identity.TaskPK)
			}
			if identity.SubmissionID != "" {
				submissionIDs = append(submissionIDs, identity.SubmissionID)
			}
		}
		taskMap := map[int64]*Task{}
		if len(taskIDs) > 0 {
			var loaded []*Task
			query := tx.Omit("data").Where("id IN ?", taskIDs)
			if q.OwnerID > 0 {
				query = query.Omit("data", "channel_id").Where("user_id = ?", q.OwnerID)
			}
			if err := query.Find(&loaded).Error; err != nil {
				return fmt.Errorf("load routing tasks: %w", err)
			}
			for _, task := range loaded {
				taskMap[task.ID] = task
			}
		}
		submissionMap := map[string]*RoutingSubmission{}
		fundingIDs := []string{}
		var mjIDs []int64
		if len(submissionIDs) > 0 {
			var loaded []*RoutingSubmission
			if err := tx.Select("submission_id", "funding_id", "user_id", "state", "quota", "client_model", "business_group", "local_task_id", "local_task_kind", "created_at", "updated_at").Where("submission_id IN ?", submissionIDs).Find(&loaded).Error; err != nil {
				return fmt.Errorf("load routing submissions: %w", err)
			}
			for _, submission := range loaded {
				submissionMap[submission.SubmissionID] = submission
				fundingIDs = append(fundingIDs, submission.FundingID)
				if submission.LocalTaskKind == "midjourney" && submission.LocalTaskID > 0 {
					mjIDs = append(mjIDs, submission.LocalTaskID)
				}
			}
		}
		mjMap := map[int]*Midjourney{}
		if len(mjIDs) > 0 {
			var loaded []*Midjourney
			if err := tx.Select("id", "user_id", "mj_id").Where("id IN ?", mjIDs).Find(&loaded).Error; err != nil {
				return fmt.Errorf("load routing Midjourney identities: %w", err)
			}
			for _, mj := range loaded {
				mjMap[mj.Id] = mj
			}
		}
		funds := map[string]RoutingFundingSession{}
		if len(fundingIDs) > 0 {
			var loaded []RoutingFundingSession
			if err := tx.Select("funding_id", "user_id", "status").Where("funding_id IN ?", fundingIDs).Find(&loaded).Error; err != nil {
				return fmt.Errorf("load routing funding status: %w", err)
			}
			for _, fund := range loaded {
				funds[fund.FundingID] = fund
			}
		}
		for _, identity := range page {
			row := RoutingTaskListRow{Task: taskMap[identity.TaskPK], Submission: submissionMap[identity.SubmissionID]}
			if identity.TaskPK > 0 && row.Task == nil || identity.SubmissionID != "" && row.Submission == nil {
				return errors.New("routing task page changed during load")
			}
			if row.Submission != nil {
				if q.OwnerID > 0 && row.Submission.UserID != q.OwnerID {
					return errors.New("routing submission owner changed during load")
				}
				switch row.Submission.LocalTaskKind {
				case "", "task":
				case "midjourney":
					if mj := mjMap[int(row.Submission.LocalTaskID)]; mj != nil && mj.UserId == row.Submission.UserID {
						row.SubmissionTaskID = &mj.MjId
					}
				default:
					return errors.New("invalid routing submission task kind")
				}
				fund, ok := funds[row.Submission.FundingID]
				if !ok || fund.UserID != row.Submission.UserID {
					return errors.New("routing submission funding identity unavailable")
				}
				row.FundingStatus = fund.Status
			}
			rows = append(rows, row)
		}
		return nil
	})
	return rows, total, err
}
