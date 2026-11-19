package controller

import (
	"errors"
	"net/http"
	"slices"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/internal/mowan/smartrouting"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

func (request *tokenRequest) UnmarshalJSON(data []byte) error {
	type plain tokenRequest
	var decoded plain
	if err := common.Unmarshal(data, &decoded.fields); err != nil {
		return err
	}
	if decoded.fields == nil {
		return common.NewMessage("Token request must be an object")
	}
	if err := common.Unmarshal(data, &decoded); err != nil {
		for _, field := range []string{"routing", "expected_routing_version", "allow_routing_self_write"} {
			if _, ok := decoded.fields[field]; ok {
				return common.NewMessage("Invalid routing parameters")
			}
		}
		return err
	}
	for _, field := range []string{"group", "cross_group_retry", "expected_routing_version", "allow_routing_self_write"} {
		if raw, ok := decoded.fields[field]; ok && strings.TrimSpace(string(raw)) == "null" {
			return common.NewMessage("Token field cannot be null: {{field}}", map[string]any{"field": field})
		}
	}
	*request = tokenRequest(decoded)
	return nil
}

func (request tokenRequest) has(field string) bool { _, ok := request.fields[field]; return ok }
func (request tokenRequest) hasLegacyRouting() bool {
	return request.has("group") || request.AutoGroups.Set || request.has("cross_group_retry")
}

func tokenRequestError(c *gin.Context, err error) {
	var message *common.Message
	if errors.As(err, &message) {
		common.ApiErrorStatus(c, http.StatusBadRequest, err)
		return
	}
	common.ApiError(c, err)
}

func (request tokenRequest) routingChange(create bool) (*smartrouting.Policy, bool, bool, error) {
	if request.ExpectedRoutingVersion != nil && *request.ExpectedRoutingVersion < 0 {
		return nil, false, false, common.NewMessage("Routing version must be nonnegative")
	}
	if create && request.ExpectedRoutingVersion != nil && *request.ExpectedRoutingVersion != 0 {
		return nil, false, false, common.NewMessage("New token routing version must be zero")
	}
	write := request.has("routing") || request.AllowRoutingSelfWrite != nil
	if request.has("routing") && request.hasLegacyRouting() {
		return nil, false, false, common.NewMessage("Routing conflicts with legacy routing fields")
	}
	if !create && write && request.ExpectedRoutingVersion == nil {
		return nil, false, false, common.NewMessage("Expected routing version is required")
	}
	if request.has("routing") {
		if strings.TrimSpace(string(request.Routing)) == "null" {
			if create {
				return nil, false, false, common.NewMessage("New token cannot restore legacy routing")
			}
			return nil, true, true, nil
		}
		policy, err := smartrouting.ParsePolicy(request.Routing)
		return &policy, false, true, err
	}
	if create && !request.hasLegacyRouting() {
		policy := smartrouting.DefaultPolicy()
		return &policy, false, true, nil
	}
	return nil, false, write, nil
}

func tokenRoutingError(c *gin.Context, err error) {
	if errors.Is(err, model.ErrRoutingVersionConflict) {
		common.ApiErrorStatus(c, http.StatusConflict, common.NewMessage("routing_version_conflict"), gin.H{"code": "routing_version_conflict"})
		return
	}
	var message *common.Message
	if errors.As(err, &message) {
		common.ApiErrorStatus(c, http.StatusBadRequest, err)
		return
	}
	if err.Error() == "routing_legacy_snapshot_unavailable" {
		common.ApiErrorStatus(c, http.StatusBadRequest, common.NewMessage("routing_legacy_snapshot_unavailable"))
		return
	}
	common.SysError(common.LogText("token routing operation failed: %v", err))
	common.ApiErrorStatus(c, http.StatusServiceUnavailable, common.NewMessage("Routing configuration is unavailable"), gin.H{"code": "routing_configuration_unavailable"})
}

func loadMaskedTokenResponse(db *gorm.DB, token *model.Token) (*tokenResponse, error) {
	response := buildMaskedTokenResponse(token)
	row, err := model.GetTokenRouting(db, token.Id)
	if err != nil && !errors.Is(err, model.ErrRoutingPolicyNeedsUpdate) {
		return nil, err
	}
	response.RoutingRequiresUpdate = errors.Is(err, model.ErrRoutingPolicyNeedsUpdate)
	if row != nil {
		response.RoutingVersion, response.AllowRoutingSelfWrite = row.Version, row.AllowSelfWrite
		if row.Mode != "legacy" && !response.RoutingRequiresUpdate {
			response.Routing = common.RawMessage(row.PolicyJSON)
		}
	}
	return response, nil
}

func loadMaskedTokenResponses(db *gorm.DB, tokens []*model.Token) ([]*tokenResponse, error) {
	responses := make([]*tokenResponse, 0, len(tokens))
	for _, token := range tokens {
		response, err := loadMaskedTokenResponse(db, token)
		if err != nil {
			return nil, err
		}
		responses = append(responses, response)
	}
	return responses, nil
}

// Validate only explicitly supplied policies; existing values remain readable after permissions change.
func validateTokenRoutingPolicy(userGroup string, db *gorm.DB, token *model.Token, policy *smartrouting.Policy, row *model.TokenRouting, create bool) error {
	if policy == nil {
		return nil
	}
	if policy.Mode == "fixed" && policy.FixedGroup != nil && *policy.FixedGroup == "" {
		inherited := !create && token.Group == "" && (row == nil || row.Mode == "legacy")
		if row != nil && row.LegacySnapshot != "" {
			var snapshot model.LegacyTokenRouting
			if err := common.UnmarshalJsonStr(row.LegacySnapshot, &snapshot); err != nil {
				return err
			}
			inherited = !create && snapshot.Group == ""
		}
		if !inherited {
			return common.NewMessage("Fixed routing requires one explicit group")
		}
		return nil
	}
	// The default dynamic policy has no explicit candidates to authorize.
	if policy.FixedGroup == nil && len(policy.Models) == 0 && len(policy.ExcludedGroups) == 0 && len(policy.ExcludedModels) == 0 && len(policy.ExcludedModelGroups) == 0 {
		return nil
	}
	validateGroup := func(group string) error {
		if !service.IsUserSelectableGroup(userGroup, group) || group == "all" {
			return common.NewMessage("Routing group is unavailable or unauthorized: {{group}}", map[string]any{"group": group})
		}
		return nil
	}
	// Query directly because the legacy model-list helper does not return storage errors.
	modelsForGroup := map[string][]string{}
	limits := token.GetModelLimitsMap()
	supports := func(names []string, name string) bool {
		return slices.Contains(names, name) || slices.Contains(names, ratio_setting.RoutingMatchModelName(name))
	}
	enabled := func(group string) ([]string, error) {
		if names, ok := modelsForGroup[group]; ok {
			return names, nil
		}
		var names []string
		err := db.Model(&model.Ability{}).Where(map[string]any{"enabled": true}).Where(map[string]any{"group": []string{group, "all"}}).Distinct("model").Pluck("model", &names).Error
		modelsForGroup[group] = names
		return names, err
	}
	validateModel := func(name, group string) error {
		if token.ModelLimitsEnabled && !middleware.TokenModelLimitAllows(limits, name) {
			return common.NewMessage("Routing model is outside token model limits: {{model}}", map[string]any{"model": name})
		}
		names, err := enabled(group)
		if err != nil {
			return err
		}
		if !supports(names, name) {
			return common.NewMessage("Routing model is unavailable in group: {{model}}", map[string]any{"model": name})
		}
		return nil
	}
	if policy.FixedGroup != nil {
		return validateGroup(*policy.FixedGroup)
	}
	for name, item := range policy.Models {
		for _, group := range item.Groups {
			if err := validateGroup(group); err != nil {
				return err
			}
			if err := validateModel(name, group); err != nil {
				return err
			}
		}
	}
	for _, group := range policy.ExcludedGroups {
		if err := validateGroup(group); err != nil {
			return err
		}
	}
	for name, groups := range policy.ExcludedModelGroups {
		for _, group := range groups {
			if err := validateGroup(group); err != nil {
				return err
			}
			if err := validateModel(name, group); err != nil {
				return err
			}
		}
	}
	for _, name := range policy.ExcludedModels {
		found := false
		for group := range service.GetUserUsableGroups(userGroup) {
			if !service.IsUserSelectableGroup(userGroup, group) || group == "all" {
				continue
			}
			names, err := enabled(group)
			if err != nil {
				return err
			}
			if supports(names, name) {
				found = true
				break
			}
		}
		if !found || (token.ModelLimitsEnabled && !middleware.TokenModelLimitAllows(limits, name)) {
			return common.NewMessage("Routing model is unavailable or unauthorized: {{model}}", map[string]any{"model": name})
		}
	}
	return nil
}

func (request tokenRequest) applyFields(token *model.Token) {
	if request.has("name") {
		token.Name = request.Name
	}
	if request.has("expired_time") {
		token.ExpiredTime = request.ExpiredTime
	}
	if request.has("remain_quota") {
		token.RemainQuota = request.RemainQuota
	}
	if request.has("unlimited_quota") {
		token.UnlimitedQuota = request.UnlimitedQuota
	}
	if request.has("model_limits_enabled") {
		token.ModelLimitsEnabled = request.ModelLimitsEnabled
	}
	if request.has("model_limits") {
		token.ModelLimits = request.ModelLimits
	}
	if request.has("allow_ips") {
		token.AllowIps = request.AllowIps
	}
	if request.AllowUserQuotaQuery != nil {
		token.AllowUserQuotaQuery = *request.AllowUserQuotaQuery
	}
	if request.has("group") {
		token.Group = request.Group
	}
	if request.has("cross_group_retry") {
		token.CrossGroupRetry = request.CrossGroupRetry
	}
}
