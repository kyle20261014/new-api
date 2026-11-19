package service

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"sort"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/internal/mowan/smartrouting"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/jsplugin"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const routingPlanKey = "mowan_routing_plan"

type routingBinding struct {
	Group     string
	ChannelID int
}

// RoutingPlan lives for the whole request, including group switches and policy reloads.
type RoutingPlan struct {
	mu                   sync.Mutex
	RequestID            string
	Model                string
	Version              int64
	UserGroup            string
	Reloads              int
	Budget, Sent         int
	Tried                map[int]bool
	Keys                 map[string]bool
	Selected             routingBinding
	FirstSelected        routingBinding
	Binding              routingBinding
	OriginGroup          string
	Barrier              string
	Deadline             time.Time
	StartedAt            time.Time
	TrySoftOfflineGroups []string
	Probes               []model.RoutingProbeClaim
}

func IsSmartRouting(c *gin.Context) bool {
	if c == nil {
		return false
	}
	if _, ok := c.Get(routingPlanKey); ok {
		return true
	}
	value, ok := c.Get(smartrouting.ContextKey)
	row, valid := value.(*model.TokenRouting)
	return ok && valid && row != nil && row.Mode != "legacy"
}

func RoutingPlanFor(c *gin.Context) *RoutingPlan {
	if !IsSmartRouting(c) {
		return nil
	}
	if value, ok := c.Get(routingPlanKey); ok {
		return value.(*RoutingPlan)
	}
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		panic(fmt.Errorf("routing request identity: %w", err))
	}
	p := &RoutingPlan{RequestID: hex.EncodeToString(id[:]), Budget: max(1, 1+common.RetryTimes), Tried: map[int]bool{}, Keys: map[string]bool{}, StartedAt: time.Now()}
	if c.Request != nil {
		p.Deadline, _ = c.Request.Context().Deadline()
	}
	if common.RelayTimeout > 0 {
		deadline := time.Now().Add(time.Duration(common.RelayTimeout) * time.Second)
		if p.Deadline.IsZero() || deadline.Before(p.Deadline) {
			p.Deadline = deadline
		}
	}
	c.Set(routingPlanKey, p)
	return p
}

func routingFailure(reason string) *ChannelSelectError {
	status := http.StatusServiceUnavailable
	if reason == "routing_model_not_configured" || reason == "routing_model_forbidden" || reason == "routing_identity_unavailable" || reason == "routing_ip_forbidden" {
		status = http.StatusForbidden
	}
	return &ChannelSelectError{StatusCode: status, Code: types.ErrorCode(reason), Message: reason}
}

// routingAuthority deliberately avoids token, user and channel caches. Lock order
// matches funding/config mutation: User, Token, then routing configuration.
func routingAuthority(c *gin.Context, name string, fn func(*gorm.DB, *model.TokenRouting, []string, string) error) error {
	return model.DB.Transaction(func(tx *gorm.DB) error {
		var user model.User
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&user, "id = ?", c.GetInt("id")).Error; err != nil {
			return fmt.Errorf("routing user: %w", err)
		}
		if user.Status != common.UserStatusEnabled {
			return errors.New("routing_identity_unavailable")
		}
		user.ToBaseUser().WriteContext(c)
		var row *model.TokenRouting
		if c.GetBool("mowan_routing_playground") {
			if c.Request == nil || c.Request.URL.Path != "/pg/chat/completions" || c.GetInt("token_id") != 0 || c.GetInt("access_token_id") > 0 || c.GetString("access_token_ref") != "" || c.GetBool("access_token_legacy") {
				return errors.New("routing_identity_unavailable")
			}
			value, _ := c.Get(smartrouting.ContextKey)
			row, _ = value.(*model.TokenRouting)
		} else {
			var token model.Token
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&token, "id = ? AND user_id = ?", c.GetInt("token_id"), user.Id).Error; err != nil {
				return fmt.Errorf("routing token: %w", err)
			}
			if token.Status != common.TokenStatusEnabled || (token.ExpiredTime != -1 && token.ExpiredTime <= common.GetTimestamp()) {
				return errors.New("routing_identity_unavailable")
			}
			if ips := token.GetIpLimits(); len(ips) > 0 && !common.IsIpInCIDRList(common.ParseIP(c.ClientIP()), ips) {
				return errors.New("routing_ip_forbidden")
			}
			if token.ModelLimitsEnabled && !TokenModelLimitAllows(token.GetModelLimitsMap(), name) {
				return errors.New("routing_model_forbidden")
			}
			var err error
			row, err = model.GetTokenRouting(tx, token.Id)
			if err != nil {
				return err
			}
		}
		if row == nil || row.Mode == "legacy" {
			return errors.New("routing_configuration_changed")
		}
		policy, err := smartrouting.ParsePolicy([]byte(row.PolicyJSON))
		if err != nil {
			return err
		}
		strategy := policy.Strategy
		groups := []string{}
		if policy.Mode == "fixed" {
			g := *policy.FixedGroup
			if g == "" {
				g = user.Group
			}
			groups = append(groups, g)
			strategy = "manual"
		} else if policy.Strategy == "custom" {
			mp, ok := policy.Models[name]
			if !ok {
				return errors.New("routing_model_not_configured")
			}
			groups = append(groups, mp.Groups...)
			strategy = mp.Strategy
		} else {
			if slices.Contains(policy.ExcludedModels, name) {
				return errors.New("routing_model_forbidden")
			}
			for g := range GetUserUsableGroups(user.Group) {
				if !slices.Contains(policy.ExcludedGroups, g) && !slices.Contains(policy.ExcludedModelGroups[name], g) {
					groups = append(groups, g)
				}
			}
			sort.Strings(groups)
		}
		groups = slices.DeleteFunc(groups, func(g string) bool { return g == "all" || !IsUserSelectableGroup(user.Group, g) })
		if len(groups) == 0 {
			return errors.New("routing_no_candidate")
		}
		if p := RoutingPlanFor(c); p != nil {
			p.TrySoftOfflineGroups = nil
			if policy.Strategy == "custom" {
				p.TrySoftOfflineGroups = policy.Models[name].TrySoftOfflineGroups
			}
		}
		return fn(tx, row, groups, strategy)
	})
}

func TokenModelLimitAllows(limit map[string]bool, name string) bool {
	return limit[name] || limit[ratio_setting.FormatMatchingModelName(name)] || limit[ratio_setting.RoutingMatchModelName(name)]
}

func selectRoutingChannel(c *gin.Context, name string) (*model.Channel, string, *ChannelSelectError) {
	p := RoutingPlanFor(c)
	ReleaseRoutingUnusedProbes(c)
	p.mu.Lock()
	defer p.mu.Unlock()
	c.Set("mowan_routing_send_claimed", false)
	c.Set("mowan_routing_transport_started", false)
	c.Set("mowan_routing_rebuild_required", false)
	c.Set("mowan_routing_probes_prepared", false)
	p.Probes = nil
	c.Set("mowan_routing_health_skipped", false)
	if reason := p.stopReason(c); reason != "" {
		return nil, "", routingFailure(reason)
	}
	p.Model = name
	var groups []string
	var strategy string
	err := routingAuthority(c, name, func(_ *gorm.DB, row *model.TokenRouting, allowed []string, s string) error {
		if p.Version != 0 && p.Version != row.Version {
			p.Selected = routingBinding{}
		}
		p.Version = row.Version
		groups = allowed
		strategy = s
		return nil
	})
	if err != nil {
		return nil, "", routingFailure(err.Error())
	}
	p.UserGroup = common.GetContextKeyString(c, constant.ContextKeyUserGroup)
	constraints := GetChannelConstraints(c)
	if p.Sent == 0 {
		p.Binding = routingBinding{}
		GetPreferredChannelByAffinity(c, name, "smart")
	}
	eligible := func(b routingBinding) (*model.Channel, bool) {
		if !slices.Contains(groups, b.Group) || b.ChannelID <= 0 || p.Tried[b.ChannelID] {
			return nil, false
		}
		ch, err := model.GetChannelById(b.ChannelID, true)
		if err != nil || ch.Status != common.ChannelStatusEnabled || !routingChannelHasKey(ch) || !routingAbilityEnabled(model.DB, b.Group, name, ch.Id) {
			return nil, false
		}
		ok, _ := model.ChannelSatisfiesFilters(ch, name, constraints.Filters)
		return ch, ok && routingHealthTryable(c, p, ch, b.Group)
	}
	choose := func(ch *model.Channel, g string) (*model.Channel, string, *ChannelSelectError) {
		p.Selected = routingBinding{g, ch.Id}
		if p.FirstSelected.ChannelID == 0 {
			p.FirstSelected = p.Selected
		}
		common.SetContextKey(c, constant.ContextKeyUsingGroup, g)
		common.SetContextKey(c, constant.ContextKeyAutoGroup, g)
		return ch, g, nil
	}
	if pin, ok, _ := constraints.ResolvedPin(); ok {
		if p.Binding.ChannelID > 0 && RequestPolicy(c).SessionMode == "strict" && (pin.ChannelId != p.Binding.ChannelID || (p.OriginGroup != "" && p.OriginGroup != p.Binding.Group)) {
			return nil, "", routingFailure("strict_session_binding_unavailable")
		}
		// Origin retries may rotate a key, but never choose another billing group.
		pinnedGroups := groups
		if p.Binding.ChannelID > 0 && RequestPolicy(c).SessionMode == "strict" {
			pinnedGroups = []string{p.Binding.Group}
		}
		if p.OriginGroup != "" {
			pinnedGroups = []string{p.OriginGroup}
		}
		for _, g := range pinnedGroups {
			tried := p.Tried[pin.ChannelId]
			if pin.Source == dto.PinSourceOriginTask {
				delete(p.Tried, pin.ChannelId)
			}
			ch, valid := eligible(routingBinding{g, pin.ChannelId})
			if tried {
				p.Tried[pin.ChannelId] = true
			}
			if valid {
				if pin.Source == dto.PinSourceOriginTask {
					p.OriginGroup = g
				}
				return choose(ch, g)
			}
		}
		if p.Binding.ChannelID > 0 && RequestPolicy(c).SessionMode == "strict" {
			return nil, "", routingFailure("strict_session_binding_unavailable")
		}
		return nil, "", routingFailure("pinned_channel_unavailable")
	}
	if p.Sent == 0 {
		if p.Binding.ChannelID > 0 {
			if ch, ok := eligible(p.Binding); ok {
				MarkChannelAffinityUsed(c, p.Binding.Group, ch.Id)
				return choose(ch, p.Binding.Group)
			}
			if RequestPolicy(c).SessionMode == "strict" {
				return nil, "", routingFailure("strict_session_binding_unavailable")
			}
		}
	}
	if ch, ok := eligible(p.Selected); ok {
		return choose(ch, p.Selected.Group)
	}
	if p.Binding.ChannelID > 0 && RequestPolicy(c).SessionMode == "strict" {
		return nil, "", routingFailure("strict_session_binding_unavailable")
	}
	// Keep exhausting the current group's priority tiers before ranking another
	// group. The exclusion set is global, including channels shared by groups.
	if p.Sent > 0 && slices.Contains(groups, p.Selected.Group) {
		ch, err := routingGroupChannel(c, p, p.Selected.Group)
		if err != nil {
			return nil, "", routingFailure(err.Error())
		}
		if ch != nil {
			return choose(ch, p.Selected.Group)
		}
	}
	var candidates []smartrouting.RankCandidate
	remaining := make(map[string]*model.Channel, len(groups))
	if strategy == "manual" {
		for _, group := range groups {
			candidates = append(candidates, smartrouting.RankCandidate{Group: group})
		}
	} else {
		available := make([]string, 0, len(groups))
		for _, group := range groups {
			ch, scanErr := routingGroupChannel(c, p, group)
			if scanErr != nil {
				return nil, "", routingFailure(scanErr.Error())
			}
			if ch != nil {
				available = append(available, group)
				remaining[group] = ch
			}
		}
		candidates, err = RoutingRankCandidates(c, name, available)
	}
	if err != nil {
		return nil, "", routingFailure(err.Error())
	}
	ranked, err := smartrouting.Rank(smartrouting.RankRequest{Strategy: strategy, ClientModel: name, RequestID: p.RequestID, Attempt: uint64(p.Sent), Now: time.Now(), Candidates: candidates})
	if err != nil {
		return nil, "", routingFailure(err.Error())
	}
	passes := 1
	if strategy == "manual" {
		passes = 2
	}
	for pass := 0; pass < passes; pass++ {
		for _, candidate := range ranked.Candidates {
			if ch := remaining[candidate.Group]; ch != nil {
				return choose(ch, candidate.Group)
			}
			ch, err := routingGroupChannel(c, p, candidate.Group, strategy == "manual" && pass == 0)
			if err != nil {
				return nil, "", routingFailure(err.Error())
			}
			if ch != nil {
				return choose(ch, candidate.Group)
			}
		}
	}
	if c.GetBool("mowan_routing_health_skipped") {
		return nil, "", routingFailure("routing_no_healthy_candidate")
	}
	return nil, "", routingFailure("routing_channels_exhausted")
}

func (p *RoutingPlan) stopReason(c *gin.Context) string {
	timeout := 0
	switch RoutingProtocolClass(c) {
	case "stream", "realtime", "responses_ws":
		timeout = constant.StreamingTimeout
	case "async":
		timeout = constant.TaskPluginProtocolTimeoutSeconds
	}
	if timeout > 0 {
		deadline := p.StartedAt.Add(time.Duration(timeout) * time.Second)
		if p.Deadline.IsZero() || deadline.Before(p.Deadline) {
			p.Deadline = deadline
		}
	}
	if p.Barrier != "" {
		return p.Barrier
	}
	if c.Request != nil && c.Request.Context().Err() != nil {
		return "routing_context_cancelled"
	}
	if !p.Deadline.IsZero() && !time.Now().Before(p.Deadline) {
		return "routing_deadline_exceeded"
	}
	if p.Sent >= p.Budget {
		return "attempt_budget_exhausted"
	}
	return ""
}

// BeginRoutingSend is called immediately before transport submission. Repeated
// calls inside one adaptor are idempotent; the next selection clears the claim.
func BeginRoutingSend(c *gin.Context) error {
	p := RoutingPlanFor(c)
	if p == nil {
		return nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if c.GetBool("mowan_routing_send_claimed") {
		return nil
	}
	if p.Barrier == "" && c.Writer != nil && c.Writer.Written() && !c.GetBool("mowan_responses_ws") && !c.GetBool("mowan_realtime") {
		p.Barrier = "response_started"
	}
	if reason := p.stopReason(c); reason != "" {
		return errors.New(reason)
	}
	b := p.Selected
	if b.ChannelID != c.GetInt("channel_id") || b.Group != common.GetContextKeyString(c, constant.ContextKeyUsingGroup) {
		return errors.New("routing_selection_changed")
	}
	key := common.GetContextKeyString(c, constant.ContextKeyChannelKey)
	digest := sha256.Sum256([]byte(key))
	fingerprint := hex.EncodeToString(digest[:])
	err := routingAuthority(c, p.Model, func(tx *gorm.DB, row *model.TokenRouting, groups []string, _ string) error {
		if (p.Version != 0 && row.Version != p.Version) || (p.UserGroup != "" && p.UserGroup != common.GetContextKeyString(c, constant.ContextKeyUserGroup)) {
			c.Set("mowan_routing_rebuild_required", true)
			return errors.New("routing_configuration_changed")
		}
		if !slices.Contains(groups, b.Group) {
			return errors.New("routing_group_forbidden")
		}
		var ch model.Channel
		if err := tx.First(&ch, "id = ? AND status = ?", b.ChannelID, common.ChannelStatusEnabled).Error; err != nil {
			return err
		}
		ok, _ := model.ChannelSatisfiesFilters(&ch, p.Model, GetChannelConstraints(c).Filters)
		if !ok {
			return errors.New("routing_filter_changed")
		}
		if !routingAbilityEnabled(tx, b.Group, p.Model, b.ChannelID) {
			return errors.New("routing_ability_unavailable")
		}
		keys := ch.GetKeys()
		if !ch.ChannelInfo.IsMultiKey {
			keys = []string{ch.Key}
		}
		keyIndex := common.GetContextKeyInt(c, constant.ContextKeyChannelMultiKeyIndex)
		status, known := ch.ChannelInfo.MultiKeyStatusList[keyIndex]
		if !slices.Contains(keys, key) || (ch.ChannelInfo.IsMultiKey && (keyIndex < 0 || keyIndex >= len(keys) || keys[keyIndex] != key || (known && status != common.ChannelStatusEnabled))) {
			return errors.New("routing_key_changed")
		}
		pin, pinned, _ := GetChannelConstraints(c).ResolvedPin()
		if p.Tried[b.ChannelID] && (!pinned || pin.Source != dto.PinSourceOriginTask || p.Keys[fmt.Sprintf("%d:%s", b.ChannelID, fingerprint)]) {
			return errors.New("routing_channel_already_sent")
		}
		p.Version = row.Version
		return nil
	})
	if err != nil {
		return fmt.Errorf("routing before send: %w", err)
	}
	if err := CheckRoutingGroupRateLimit(c, p.Selected.Group); err != nil {
		return err
	}
	if !c.GetBool("mowan_routing_probes_prepared") {
		if err := claimRoutingProbes(c, p); err != nil {
			return err
		}
	}
	for _, claim := range p.Probes {
		view, err := (model.RoutingMetricsStore{DB: model.DB}).ReadHealth(claim.Key, claim.Stage, 0, time.Time{})
		if err != nil {
			return fmt.Errorf("routing probe validation: %w", err)
		}
		if view.Health.State != model.RoutingHealthHalfOpen || view.Health.BreakerEpoch != claim.Epoch || view.Health.ProbeLeaseID != claim.LeaseID {
			return errors.New("routing_probe_unavailable")
		}
	}
	if RoutingProtocolClass(c) == "async" {
		submission := CurrentRoutingSubmission(c)
		if submission == nil || submission.RequestID != p.RequestID || submission.AttemptNo != p.Sent+1 {
			return errors.New("routing_submission_not_prepared")
		}
		for _, claim := range p.Probes {
			if claim.Stage == model.RoutingStageTaskTerminal && (submission.TerminalProbeEpoch != claim.Epoch || submission.TerminalProbeLeaseID != claim.LeaseID) {
				return errors.New("routing_terminal_probe_not_prepared")
			}
		}
		sending, err := model.MarkRoutingSubmissionSending(submission.SubmissionID, submission.Version, submission.SendOwnerID)
		if err != nil {
			return fmt.Errorf("routing sending qualification: %w", err)
		}
		c.Set("mowan_routing_submission", sending)
	}
	p.Sent++
	p.Tried[b.ChannelID] = true
	p.Keys[fmt.Sprintf("%d:%s", b.ChannelID, fingerprint)] = true
	c.Set("mowan_routing_send_claimed", true)
	return nil
}

func BeginRoutingTransportSend(c *gin.Context) error {
	if IsSmartRouting(c) && c.GetBool("mowan_routing_transport_started") {
		return errors.New("routing_channel_already_sent")
	}
	if err := BeginRoutingSend(c); err != nil {
		return err
	}
	if IsSmartRouting(c) {
		c.Set("mowan_routing_transport_started", true)
	}
	return nil
}

// Rebuilds are preflight-only and cannot extend the actual-send allowance or deadline.
func RetryRoutingRebuild(c *gin.Context, retry *RetryParam) bool {
	p := RoutingPlanFor(c)
	if p == nil || !c.GetBool("mowan_routing_rebuild_required") || c.GetBool("mowan_routing_send_claimed") || RoutingProtocolClass(c) == "async" {
		return false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.Reloads >= p.Budget || p.stopReason(c) != "" {
		return false
	}
	p.Reloads++
	retry.ResetRetryNextTry()
	return true
}

func MarkRoutingBarrier(c *gin.Context, reason string) {
	if p := RoutingPlanFor(c); p != nil {
		p.mu.Lock()
		if p.Barrier == "" {
			p.Barrier = reason
		}
		p.mu.Unlock()
	}
}
func RoutingAttemptIdentity(c *gin.Context) (string, int, string) {
	p := RoutingPlanFor(c)
	if p == nil {
		return "", 0, ""
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	sum := sha256.Sum256([]byte(common.GetContextKeyString(c, constant.ContextKeyChannelKey)))
	attempt := p.Sent
	if !c.GetBool("mowan_routing_send_claimed") {
		attempt++
	}
	return p.RequestID, attempt, hex.EncodeToString(sum[:])
}
func RoutingRetryBarrier(c *gin.Context) string {
	p := RoutingPlanFor(c)
	if p == nil {
		return ""
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.Barrier == "" && c.Writer != nil && c.Writer.Written() && !c.GetBool("mowan_responses_ws") && !c.GetBool("mowan_realtime") {
		p.Barrier = "response_started"
	}
	return p.stopReason(c)
}

// BindRoutingOrigin keeps the recorded billing group, including when a channel
// appears in multiple groups. Missing historical groups cannot be guessed.
func BindRoutingOrigin(c *gin.Context, name, group string, channelID int) error {
	p := RoutingPlanFor(c)
	if p == nil {
		return nil
	}
	if group == "" {
		return errors.New("origin_task_group_unavailable")
	}
	if pin, pinned, _ := GetChannelConstraints(c).ResolvedPin(); pinned && pin.ChannelId != channelID {
		return errors.New("origin_task_channel_conflict")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.OriginGroup != "" && p.OriginGroup != group {
		return errors.New("origin_task_group_conflict")
	}
	var err error
	if name != "" {
		err = routingAuthority(c, name, func(_ *gorm.DB, _ *model.TokenRouting, groups []string, _ string) error {
			if !slices.Contains(groups, group) {
				return errors.New("origin_task_group_forbidden")
			}
			return nil
		})
	}
	if err != nil {
		return err
	}
	p.Model = name
	p.OriginGroup = group
	p.Selected = routingBinding{group, channelID}
	common.SetContextKey(c, constant.ContextKeyUsingGroup, group)
	GetChannelConstraints(c).AddPin(dto.ChannelPin{ChannelId: channelID, Source: dto.PinSourceOriginTask, Rank: dto.PinRankOriginTask, RetryMode: dto.PinRetrySameChannel})
	return nil
}

// ValidateRoutingConnection authorizes a subsequent create on an existing WS
// connection. It never runs the ranking path or migrates that connection.
func ValidateRoutingConnection(c *gin.Context, name, group string, channelID int) error {
	p := RoutingPlanFor(c)
	if p == nil {
		return nil
	}
	p.Model = name
	p.Selected = routingBinding{group, channelID}
	if err := BeginRoutingSend(c); err != nil {
		return err
	}
	MarkRoutingBarrier(c, "upstream_websocket_connected")
	return nil
}

func AuthorizeRoutingConnection(c *gin.Context, name, group string, channelID int) error {
	p := RoutingPlanFor(c)
	if p == nil {
		return nil
	}
	return routingAuthority(c, name, func(_ *gorm.DB, row *model.TokenRouting, groups []string, _ string) error {
		if !slices.Contains(groups, group) {
			return errors.New("routing_group_forbidden")
		}
		p.Model = name
		p.Selected = routingBinding{group, channelID}
		p.Version = row.Version
		p.UserGroup = common.GetContextKeyString(c, constant.ContextKeyUserGroup)
		return nil
	})
}

func RoutingProtocolClass(c *gin.Context) string {
	if c.GetBool("mowan_responses_ws") {
		return "responses_ws"
	}
	if c.GetBool("mowan_realtime") || c.GetInt("relay_mode") == relayconstant.RelayModeRealtime || c.Request != nil && c.Request.Method == http.MethodGet && c.Request.URL.Path == "/v1/realtime" {
		return "realtime"
	}
	if c.GetBool("mowan_routing_async") {
		return "async"
	}
	if value, ok := c.Get(jsplugin.ContextKeyPinnedRoute); ok {
		if pin, ok := value.(jsplugin.PinnedRoute); ok && pin.Generation != nil && pin.Plugin != nil && pin.Route.Type != jsplugin.RouteTypeQuery {
			return "async"
		}
	}
	if value, ok := c.Get(jsplugin.ContextKeyPinnedEndpoint); ok {
		if pin, ok := value.(jsplugin.PinnedEndpoint); ok && pin.Generation != nil && pin.Plugin != nil {
			return "async"
		}
	}
	if common.GetContextKeyBool(c, constant.ContextKeyIsStream) {
		return "stream"
	}
	return "sync"
}

func routingHealthTryable(c *gin.Context, p *RoutingPlan, ch *model.Channel, group string) bool {
	key, err := RoutingObservationKey(c, p.Model, group, ch, RoutingProtocolClass(c))
	if err != nil {
		return true
	} // unknown, never normal
	store := model.RoutingMetricsStore{DB: model.DB}
	stages := []model.RoutingMetricStage{model.RoutingStageAttempt}
	if key.ProtocolClass == "async" {
		stages = append(stages, model.RoutingStageTaskTerminal)
	}
	for _, stage := range stages {
		view, err := store.ReadHealth(key, stage, 0, time.Time{})
		if err != nil {
			continue
		}
		if view.Health.State == model.RoutingHealthHalfOpen {
			return false
		}
		if view.Health.State == model.RoutingHealthOpen && !view.ProbeReady && !slices.Contains(p.TrySoftOfflineGroups, group) {
			return false
		}
	}
	return true
}

func routingGroupChannel(c *gin.Context, p *RoutingPlan, group string, skipCongested ...bool) (*model.Channel, error) {
	excluded := make(map[int]bool, len(p.Tried))
	for id, tried := range p.Tried {
		excluded[id] = tried
	}
	for {
		ch, err := model.GetRandomSatisfiedChannelExcluding(group, p.Model, GetChannelConstraints(c).Filters, excluded)
		if err != nil || ch == nil {
			return ch, err
		}
		fresh, err := model.GetChannelById(ch.Id, true)
		if err != nil {
			return nil, err
		}
		valid, _ := model.ChannelSatisfiesFilters(fresh, p.Model, GetChannelConstraints(c).Filters)
		if fresh.Status != common.ChannelStatusEnabled || !routingChannelHasKey(fresh) || !routingAbilityEnabled(model.DB, group, p.Model, fresh.Id) || !valid {
			excluded[ch.Id] = true
			continue
		}
		ch = fresh
		tryable := routingHealthTryable(c, p, ch, group)
		congested := false
		if len(skipCongested) > 0 && skipCongested[0] {
			if key, err := RoutingObservationKey(c, p.Model, group, ch, RoutingProtocolClass(c)); err == nil {
				stages := []model.RoutingMetricStage{model.RoutingStageAttempt}
				if key.ProtocolClass == "async" {
					stages = append(stages, model.RoutingStageTaskTerminal)
				}
				for _, stage := range stages {
					if err := routingMetricsCall(model.DB, func(store model.RoutingMetricsStore) error {
						active, err := store.CountActive(key, stage, time.Time{})
						if err != nil {
							return err
						}
						view, err := store.ReadHealth(key, stage, active, time.Time{})
						congested = congested || err == nil && view.Congested
						return err
					}); err != nil {
						c.Set("mowan_routing_health_unknown", true)
					}
				}
			}
		}
		if tryable && !congested {
			return ch, nil
		}
		if !tryable {
			c.Set("mowan_routing_health_skipped", true)
		}
		excluded[ch.Id] = true
	}
}

func routingAbilityEnabled(db *gorm.DB, group, name string, channelID int) bool {
	var count int64
	err := db.Model(&model.Ability{}).Where("channel_id = ? AND "+db.Statement.Quote("group")+" IN ? AND model IN ? AND enabled = ?", channelID, []string{group, "all"}, []string{name, ratio_setting.RoutingMatchModelName(name)}, true).Count(&count).Error
	return err == nil && count > 0
}

func routingChannelHasKey(ch *model.Channel) bool {
	if !ch.ChannelInfo.IsMultiKey {
		return true
	}
	for i, key := range ch.GetKeys() {
		status, known := ch.ChannelInfo.MultiKeyStatusList[i]
		if key != "" && (!known || status == common.ChannelStatusEnabled) {
			return true
		}
	}
	return false
}

func claimRoutingProbes(c *gin.Context, p *RoutingPlan) error {
	ch, err := model.GetChannelById(p.Selected.ChannelID, true)
	if err != nil {
		return err
	}
	key, err := RoutingObservationKey(c, p.Model, p.Selected.Group, ch, RoutingProtocolClass(c))
	if err != nil {
		return nil
	}
	store := model.RoutingMetricsStore{DB: model.DB}
	stages := []model.RoutingMetricStage{model.RoutingStageAttempt}
	if key.ProtocolClass == "async" {
		stages = append(stages, model.RoutingStageTaskTerminal)
	}
	claims := []model.RoutingProbeClaim{}
	release := func() {
		for _, claim := range claims {
			_, _ = store.ReleaseProbe(claim, time.Time{})
		}
	}
	for _, stage := range stages {
		view, err := store.ReadHealth(key, stage, 0, time.Time{})
		if err != nil {
			continue
		} // unknown advisory health
		if view.Health.State == model.RoutingHealthClosed {
			continue
		}
		leaseID := fmt.Sprintf("%s:%d:%s", p.RequestID, p.Sent+1, stage)
		var claim model.RoutingProbeClaim
		var won bool
		if slices.Contains(p.TrySoftOfflineGroups, p.Selected.Group) {
			claim, won, err = store.ClaimProbeExplicit(key, stage, leaseID, time.Time{})
		} else {
			claim, won, err = store.ClaimProbe(key, stage, leaseID, time.Time{})
		}
		if err != nil {
			release()
			c.Set("mowan_routing_health_unknown", true)
			return nil
		}
		if !won {
			release()
			if p.Binding.ChannelID > 0 && RequestPolicy(c).SessionMode == "strict" {
				return errors.New("strict_session_binding_unavailable")
			}
			return errors.New("routing_no_healthy_candidate")
		}
		claims = append(claims, claim)
	}
	p.Probes = claims
	c.Set("mowan_routing_probes", claims)
	return nil
}

func CurrentRoutingSubmission(c *gin.Context) *model.RoutingSubmission {
	value, ok := c.Get("mowan_routing_submission")
	if !ok {
		return nil
	}
	submission, _ := value.(*model.RoutingSubmission)
	return submission
}

func RoutingSubmissionUnknown(c *gin.Context, cause error) error {
	s := CurrentRoutingSubmission(c)
	if !IsSmartRouting(c) || s == nil {
		return cause
	}
	MarkRoutingBarrier(c, "submission_unknown")
	if s.State == "sending" {
		updated, err := model.RecordRoutingSubmissionOutcome(s.SubmissionID, s.Version, "unknown", "", nil, s.SendOwnerID)
		if err != nil {
			return fmt.Errorf("submission_id=%s state=unknown cause=%v persist=%w", s.SubmissionID, cause, err)
		}
		c.Set("mowan_routing_submission", updated)
	}
	return fmt.Errorf("submission_id=%s state=unknown: %w", s.SubmissionID, cause)
}

func PrepareRoutingProbeClaims(c *gin.Context) ([]model.RoutingProbeClaim, error) {
	p := RoutingPlanFor(c)
	if p == nil {
		return nil, nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if reason := p.stopReason(c); reason != "" {
		return nil, errors.New(reason)
	}
	if !c.GetBool("mowan_routing_probes_prepared") {
		if err := claimRoutingProbes(c, p); err != nil {
			return nil, err
		}
	}
	c.Set("mowan_routing_probes_prepared", true)
	return slices.Clone(p.Probes), nil
}

func ReleaseRoutingUnusedProbes(c *gin.Context) {
	p := RoutingPlanFor(c)
	if p == nil || c.GetBool("mowan_routing_send_claimed") {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, claim := range p.Probes {
		_, _ = (model.RoutingMetricsStore{DB: model.DB}).ReleaseProbe(claim, time.Time{})
	}
	p.Probes = nil
	c.Set("mowan_routing_probes_prepared", false)
}

func RoutingChannelKey(c *gin.Context, ch *model.Channel, key string, index int) (string, int, error) {
	p := RoutingPlanFor(c)
	if p == nil {
		return key, index, nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	pin, pinned, _ := GetChannelConstraints(c).ResolvedPin()
	if !pinned || pin.Source != dto.PinSourceOriginTask || p.Sent == 0 {
		return key, index, nil
	}
	for i, k := range ch.GetKeys() {
		status, known := ch.ChannelInfo.MultiKeyStatusList[i]
		if known && status != common.ChannelStatusEnabled {
			continue
		}
		digest := sha256.Sum256([]byte(k))
		if !p.Keys[fmt.Sprintf("%d:%s", ch.Id, hex.EncodeToString(digest[:]))] {
			return k, i, nil
		}
	}
	return "", 0, errors.New("origin_task_keys_exhausted")
}

func FinishRoutingSend(c *gin.Context, apiErr *types.NewAPIError, success bool, ttft time.Duration) {
	if !IsSmartRouting(c) {
		return
	}
	result := RoutingAttemptResult{Outcome: "ignored"}
	upstreamStatus, responseKnown := 0, false
	if apiErr != nil {
		upstreamStatus = apiErr.StatusCode
		var fact *routingUpstreamResponseError
		if errors.As(apiErr, &fact) {
			upstreamStatus, responseKnown = fact.statusCode, true
		} else {
			responseKnown = apiErr.GetErrorCode() == types.ErrorCodeBadResponseStatusCode || errors.Is(apiErr, websocket.ErrBadHandshake)
		}
	}
	if success {
		result.Outcome = "success"
	} else if responseKnown && (upstreamStatus == http.StatusTooManyRequests || upstreamStatus >= 500) && !IsViolationFeeCode(apiErr.GetErrorCode()) {
		result.Outcome = "failure"
		result.RateLimited = upstreamStatus == http.StatusTooManyRequests
	} else if c.Request.Context().Err() == nil && apiErr != nil && !errors.Is(apiErr, context.Canceled) && (apiErr.GetErrorCode() == types.ErrorCodeDoRequestFailed || apiErr.GetErrorCode() == types.ErrorCodeBadResponse || apiErr.GetErrorCode() == types.ErrorCodeBadResponseBody || apiErr.GetErrorCode() == types.ErrorCodeReadResponseBodyFailed) {
		result.Outcome = "failure"
	} else if apiErr == nil && c.Request.Context().Err() == nil && c.GetBool("mowan_routing_send_claimed") {
		result.Outcome = "failure"
	}
	if ttft > 0 {
		result.TTFT = ttft
	}
	if p := RoutingPlanFor(c); p != nil {
		for _, claim := range p.Probes {
			if claim.Stage == model.RoutingStageAttempt {
				copy := claim
				result.Probe = &copy
				break
			}
		}
	}
	if err := FinishRoutingAttempt(c, result); err != nil {
		common.SysError(common.LogText("routing attempt observation failed: %v", err))
	}
}

// ObserveRoutingStreamData excludes control, role-only, usage and heartbeat
// events. Unknown protocols retain no TTFT instead of inventing a token.
func ObserveRoutingStreamData(c *gin.Context, data string) {
	if IsSmartRouting(c) && routingStreamHasOutput(data) {
		MarkRoutingFirstData(c)
	}
}

func routingValueAt(value any, path ...string) any {
	for _, key := range path {
		object, ok := value.(map[string]any)
		if !ok {
			return nil
		}
		value = object[key]
	}
	return value
}
func routingStreamHasOutput(data string) bool {
	var event map[string]any
	if common.UnmarshalJsonStr(data, &event) != nil {
		return false
	}
	has := func(value any) bool { s, ok := value.(string); return ok && s != "" }
	choices, _ := event["choices"].([]any)
	for _, choice := range choices {
		for _, path := range [][]string{{"text"}, {"delta", "content"}, {"delta", "reasoning_content"}, {"delta", "reasoning"}, {"delta", "audio", "data"}} {
			if has(routingValueAt(choice, path...)) {
				return true
			}
		}
		calls, _ := routingValueAt(choice, "delta", "tool_calls").([]any)
		for _, call := range calls {
			if has(routingValueAt(call, "function", "arguments")) || has(routingValueAt(call, "function", "name")) {
				return true
			}
		}
	}
	switch event["type"] {
	case "response.output_text.delta", "response.audio.delta", "response.output_audio.delta", "response.audio_transcript.delta", "response.output_audio_transcript.delta", "response.function_call_arguments.delta":
		return has(event["delta"])
	case "content_block_delta":
		for _, key := range []string{"text", "partial_json", "thinking"} {
			if has(routingValueAt(event, "delta", key)) {
				return true
			}
		}
	case "text-generation":
		return has(event["text"])
	}
	candidates, _ := event["candidates"].([]any)
	for _, candidate := range candidates {
		parts, _ := routingValueAt(candidate, "content", "parts").([]any)
		for _, part := range parts {
			if has(routingValueAt(part, "text")) || has(routingValueAt(part, "inlineData", "data")) || has(routingValueAt(part, "functionCall", "name")) {
				return true
			}
		}
	}
	return false
}
