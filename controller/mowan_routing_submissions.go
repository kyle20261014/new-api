package controller

import (
	"errors"
	"math"
	"net/http"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type routingSubmissionSummary struct {
	SubmissionID     string  `json:"submission_id"`
	SubmissionStatus string  `json:"submission_status"`
	ReservedAmount   float64 `json:"reserved_amount"`
	ReservedUnit     string  `json:"reserved_unit"`
	FundingStatus    string  `json:"funding_status"`
	Model            string  `json:"model"`
}

type routingSubmissionDetail struct {
	routingSubmissionSummary
	TaskID     *string `json:"task_id"`
	Group      string  `json:"group"`
	UpdatedAt  int64   `json:"updated_at"`
	SubmitTime int64   `json:"submit_time"`
}

func projectRoutingSubmission(s *model.RoutingSubmission, funding string) (routingSubmissionDetail, error) {
	if s == nil {
		return routingSubmissionDetail{}, errors.New("routing submission unavailable")
	}
	switch s.State {
	case "prepared", "sending", "accepted", "unknown", "not_sent", "rejected":
	default:
		return routingSubmissionDetail{}, errors.New("invalid routing submission state")
	}
	amount, unit := float64(s.Quota), operation_setting.GetQuotaDisplayType()
	if s.Quota < 0 {
		return routingSubmissionDetail{}, errors.New("invalid routing reserved quota")
	}
	if unit != operation_setting.QuotaDisplayTypeTokens {
		if common.QuotaPerUnit <= 0 || math.IsNaN(common.QuotaPerUnit) || math.IsInf(common.QuotaPerUnit, 0) {
			return routingSubmissionDetail{}, errors.New("invalid quota display divisor")
		}
		rate := operation_setting.GetUsdToCurrencyRate(operation_setting.USDExchangeRate)
		if rate <= 0 || math.IsNaN(rate) || math.IsInf(rate, 0) {
			return routingSubmissionDetail{}, errors.New("invalid quota display rate")
		}
		amount = amount / common.QuotaPerUnit * rate
		if unit == operation_setting.QuotaDisplayTypeCustom {
			unit = operation_setting.GetCurrencySymbol()
		}
	}
	if math.IsNaN(amount) || math.IsInf(amount, 0) || amount < 0 {
		return routingSubmissionDetail{}, errors.New("invalid routing reserved amount")
	}
	status := "pending"
	switch funding {
	case "settled":
		status = "settled"
	case "refunded":
		status = "released"
	case "reserved":
		if s.State == "unknown" {
			status = "manual_review"
		}
	default:
		return routingSubmissionDetail{}, errors.New("invalid routing funding status")
	}
	return routingSubmissionDetail{routingSubmissionSummary: routingSubmissionSummary{SubmissionID: s.SubmissionID, SubmissionStatus: s.State, ReservedAmount: amount, ReservedUnit: unit, FundingStatus: status, Model: s.ClientModel}, Group: s.BusinessGroup, UpdatedAt: s.UpdatedAt, SubmitTime: s.CreatedAt}, nil
}

func routingTaskReadError(c *gin.Context, err error) {
	common.SysError(common.LogText("routing task query failed: %v", err))
	common.ApiErrorStatus(c, http.StatusServiceUnavailable, common.NewMessage("Routing task information is temporarily unavailable"))
}

func getRoutingTaskList(c *gin.Context, admin bool, start, end int64) {
	page := common.GetPageQuery(c)
	query := model.RoutingTaskListQuery{SubmissionID: c.Query("submission_id"), SyncTaskQueryParams: model.SyncTaskQueryParams{TaskID: c.Query("task_id"), UserID: c.Query("user_id"), Action: c.Query("action"), Status: c.Query("status"), StartTimestamp: start, EndTimestamp: end}}
	query.Platform = constant.TaskPlatform(c.Query("platform"))
	if admin {
		query.ChannelID = c.Query("channel_id")
	} else {
		query.OwnerID = c.GetInt("id")
		if query.OwnerID <= 0 {
			common.ApiErrorStatus(c, http.StatusUnauthorized, common.NewMessage("Unauthorized"))
			return
		}
	}
	rows, total, err := model.GetRoutingTaskList(model.DB, page.GetStartIdx(), page.GetPageSize(), query)
	if err != nil {
		var message *common.Message
		if errors.As(err, &message) {
			common.ApiErrorStatus(c, http.StatusBadRequest, err)
			return
		}
		routingTaskReadError(c, err)
		return
	}
	tasks := make([]*model.Task, 0, len(rows))
	for _, row := range rows {
		if row.Task != nil {
			tasks = append(tasks, row.Task)
		}
	}
	role := common.RoleCommonUser
	if admin {
		role = c.GetInt("role")
	}
	dtos := tasksToDto(tasks, admin, role)
	byID := make(map[int64]*dto.TaskDto, len(dtos))
	for _, item := range dtos {
		byID[item.ID] = item
	}
	items := make([]any, 0, len(rows))
	for _, row := range rows {
		var detail routingSubmissionDetail
		if row.Submission != nil {
			detail, err = projectRoutingSubmission(row.Submission, row.FundingStatus)
			if err != nil {
				routingTaskReadError(c, err)
				return
			}
			detail.TaskID = row.SubmissionTaskID
		}
		if row.Task != nil {
			item := byID[row.Task.ID]
			var summary *routingSubmissionSummary
			var submission *routingSubmissionDetail
			if row.Submission != nil {
				summary = &detail.routingSubmissionSummary
				detail.TaskID = &item.TaskID
				submission = &detail
			}
			items = append(items, struct {
				*dto.TaskDto
				*routingSubmissionSummary
				RowType    string                   `json:"row_type"`
				Submission *routingSubmissionDetail `json:"submission,omitempty"`
			}{item, summary, "task", submission})
		} else {
			items = append(items, struct {
				routingSubmissionDetail
				RowType string `json:"row_type"`
			}{detail, "submission"})
		}
	}
	page.SetTotal(int(total))
	page.SetItems(items)
	common.ApiSuccess(c, page)
}

// GetRoutingSubmissionDetail is owner-only even when the current caller is admin.
func GetRoutingSubmissionDetail(c *gin.Context) {
	c.Header("Cache-Control", "private, no-store")
	if c.GetInt("id") <= 0 {
		common.ApiErrorStatus(c, http.StatusUnauthorized, common.NewMessage("Unauthorized"))
		return
	}
	id := c.Param("submission_id")
	if id == "" || len(id) > 128 {
		common.ApiErrorStatus(c, http.StatusBadRequest, common.NewMessage("Invalid submission ID"))
		return
	}
	submission, err := model.GetRoutingSubmission(id)
	if errors.Is(err, gorm.ErrRecordNotFound) || err == nil && submission.UserID != c.GetInt("id") {
		common.ApiErrorStatus(c, http.StatusNotFound, common.NewMessage("Submission not found"))
		return
	}
	if err != nil {
		routingTaskReadError(c, err)
		return
	}
	var fund model.RoutingFundingSession
	if err = model.DB.Select("status").Where("funding_id = ? AND user_id = ?", submission.FundingID, submission.UserID).First(&fund).Error; err != nil {
		routingTaskReadError(c, err)
		return
	}
	detail, err := projectRoutingSubmission(submission, fund.Status)
	if err != nil {
		routingTaskReadError(c, err)
		return
	}
	if submission.LocalTaskID > 0 {
		var publicTaskID string
		switch submission.LocalTaskKind {
		case "", "task":
			var task model.Task
			err = model.DB.Select("task_id").Where("id = ? AND user_id = ?", submission.LocalTaskID, submission.UserID).First(&task).Error
			publicTaskID = task.TaskID
		case "midjourney":
			var task model.Midjourney
			err = model.DB.Select("mj_id").Where("id = ? AND user_id = ?", submission.LocalTaskID, submission.UserID).First(&task).Error
			publicTaskID = task.MjId
		default:
			routingTaskReadError(c, errors.New("invalid routing submission task kind"))
			return
		}
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			routingTaskReadError(c, err)
			return
		}
		if err == nil {
			detail.TaskID = &publicTaskID
		}
	}
	common.ApiSuccess(c, detail)
}
