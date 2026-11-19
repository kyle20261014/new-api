package service

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"slices"
	"sort"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/internal/mowan/smartrouting"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service/authz"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm/clause"
)

const diagnosticOriginKey = "mowan_diagnostic_origin"

type diagnosticOrigin struct {
	identity AuthIdentity
	group    string
	request  string
	runID    string
	stepID   string
	owner    string
	info     *relaycommon.RelayInfo
}

// The origin is installed only by the in-process worker after session validation.
// No public route, JSON member, header or playground URL can construct this value.
func diagnosticRelayOrigin(c *gin.Context) *diagnosticOrigin {
	v, _ := c.Get(diagnosticOriginKey)
	o, _ := v.(*diagnosticOrigin)
	return o
}

func IsDiagnosticRelay(c *gin.Context) bool { return diagnosticRelayOrigin(c) != nil }

func DiagnosticHash(value any) string {
	b, err := common.Marshal(value)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func DiagnosticGroups(c *gin.Context, userID int, name string) ([]string, bool, error) {
	if name == "" || len(name) > 255 {
		return nil, false, errors.New("invalid diagnostic model")
	}
	req := CatalogRequest{UserID: userID, Model: name, Page: 1, PageSize: 100}
	if err := catalogValidate(&req); err != nil {
		return nil, false, err
	}
	items, err := routingCatalogItems(c.Request.Context(), req)
	if err != nil {
		return nil, false, err
	}
	groups := []string{}
	for _, item := range items {
		if item.Model == name {
			groups = append(groups, item.Group)
		}
	}
	sort.Strings(groups)
	groups = slices.Compact(groups)
	chat := false
	for _, p := range model.GetPricing() {
		if p.ModelName == name {
			chat = diagnosticChatCapable(p)
			break
		}
	}
	return groups, chat, nil
}

// diagnosticChatCapable reuses the model square's single category rule instead of
// requiring a per-model endpoint declaration. The homepage CanChat signal keeps
// its stricter declared-endpoint rule; only diagnostics widen to text models.
func diagnosticChatCapable(p model.Pricing) bool {
	return catalogCategory(p) == "text"
}

// Config changes mark reports stale without exposing configuration or credentials.
func DiagnosticConfigHash(name, group string) (string, error) {
	var abilities []model.Ability
	if err := model.DB.Where("model IN ? AND enabled = ?", []string{name, ratio_setting.RoutingMatchModelName(name)}, true).Order("channel_id asc").Order("model asc").Order(clause.OrderByColumn{Column: clause.Column{Name: "group"}}).Find(&abilities).Error; err != nil {
		return "", err
	}
	ids := []int{}
	values := []any{name, group, ratio_setting.GetGroupRatio(group), ratio_setting.GroupGroupRatio2JSONString()}
	for _, a := range abilities {
		if a.Group == group || a.Group == "all" {
			ids = append(ids, a.ChannelId)
			values = append(values, []any{a.ChannelId, a.Group, a.Priority, a.Weight})
		}
	}
	sort.Ints(ids)
	var channels []model.Channel
	if len(ids) > 0 {
		if err := model.DB.Where("id IN ?", ids).Order("id asc").Find(&channels).Error; err != nil {
			return "", err
		}
	}
	for _, ch := range channels {
		values = append(values, diagnosticChannelConfig(ch))
	}
	for _, p := range model.GetPricing() {
		if p.ModelName == name {
			values = append(values, p.PricingVersion, p.ModelRatio, p.ModelPrice, p.CompletionRatio, p.BillingExpr)
		}
	}
	return DiagnosticHash(values), nil
}

func diagnosticChannelConfig(ch model.Channel) any {
	return []any{ch.Id, ch.Type, ch.Status, ch.Key, ch.BaseURL, ch.Models, ch.ModelMapping, ch.ParamOverride, ch.HeaderOverride, ch.Setting, ch.OtherSettings, ch.Other, ch.OpenAIOrganization, ch.StatusCodeMapping, ch.Priority, ch.Weight, ch.ChannelInfo.IsMultiKey, ch.ChannelInfo.MultiKeyMode, ch.ChannelInfo.MultiKeyStatusList}
}

func PrepareDiagnosticRelay(c *gin.Context, run *model.DiagnosticRun, step *model.DiagnosticStep, owner string) error {
	identity := AuthIdentity{UserID: run.UserID, SessionID: run.SessionID, UserAuthVersion: run.UserAuthVersion, SessionVersion: run.SessionVersion}
	_, user, err := ValidateLoginSession(identity)
	if err != nil {
		return err
	}
	if !authz.Can(run.UserID, user.Role, authz.DiagnosticsCreate) {
		return errors.New("diagnostic permission unavailable")
	}
	groups, chat, err := DiagnosticGroups(c, run.UserID, run.ModelName)
	if err != nil || !slices.Contains(groups, run.GroupName) || !chat {
		return errors.New("diagnostic permission unavailable")
	}
	policy := smartrouting.Policy{SchemaVersion: 1, Mode: "fixed", FixedGroup: &run.GroupName}
	canonical, err := policy.CanonicalJSON()
	if err != nil {
		return err
	}
	user.WriteContext(c)
	c.Set("id", run.UserID)
	c.Set(common.RequestIdKey, step.RequestID)
	c.Set(smartrouting.ContextKey, &model.TokenRouting{SchemaVersion: 1, Mode: "fixed", PolicyJSON: canonical, Version: 1})
	c.Set(diagnosticOriginKey, &diagnosticOrigin{identity: identity, group: run.GroupName, request: step.RequestID, runID: run.ID, stepID: step.ID, owner: owner})
	return nil
}

func CaptureDiagnosticRelay(c *gin.Context, info *relaycommon.RelayInfo) {
	if origin := diagnosticRelayOrigin(c); origin != nil {
		// Reuse the existing session-funded token-zero semantics; the transport
		// remains /v1/chat/completions and never impersonates a playground route.
		info.IsPlayground = true
		origin.info = info
	}
}

func DiagnosticWasFree(c *gin.Context) bool {
	o := diagnosticRelayOrigin(c)
	return o != nil && o.info != nil && o.info.PriceData.FreeModel
}

func diagnosticBeforeSend(c *gin.Context, channelID int) error {
	o := diagnosticRelayOrigin(c)
	if o == nil {
		return nil
	}
	var channel model.Channel
	if err := model.DB.First(&channel, channelID).Error; err != nil {
		return err
	}
	return model.UpdateDiagnosticStep(o.runID, o.owner, o.stepID, map[string]any{
		"state": "sending", "channel_id": channelID,
		"source_hash": DiagnosticHash([]any{diagnosticChannelConfig(channel), common.GetContextKeyString(c, constant.ContextKeyChannelKey)}),
	})
}
