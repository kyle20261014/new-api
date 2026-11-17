package service

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/http/httptest"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	hostdto "github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/internal/mowan/smartrouting"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/billingexpr"
	"github.com/QuantumNous/new-api/pkg/jsplugin"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/setting/billing_setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// Identity and execution constraints are server-owned, never bound from JSON.
type CatalogRequest struct {
	UserID        int                         `json:"-"`
	TokenID       int                         `json:"-"`
	Token         *model.Token                `json:"-"`
	Model         string                      `json:"model,omitempty"`
	Search        string                      `json:"search,omitempty"`
	Category      string                      `json:"category,omitempty"`
	Page          int                         `json:"p,omitempty"`
	PageSize      int                         `json:"page_size,omitempty"`
	Request       *dto.GeneralOpenAIRequest   `json:"-"`
	Constraints   *hostdto.ChannelConstraints `json:"-"`
	RequestPath   string                      `json:"-"`
	ProtocolClass string                      `json:"-"`
}

type CatalogPrice struct {
	Label  string   `json:"label"`
	Amount *float64 `json:"amount,omitempty"`
	Min    *float64 `json:"min,omitempty"`
	Max    *float64 `json:"max,omitempty"`
	Unit   string   `json:"unit"`
	Rule   string   `json:"rule,omitempty"`
}
type CatalogQuote struct {
	Comparison     string         `json:"comparison"`
	Reason         string         `json:"reason,omitempty"`
	EstimatedTotal *float64       `json:"estimated_total,omitempty"`
	Currency       string         `json:"currency"`
	Prices         []CatalogPrice `json:"prices"`
	Cost           float64        `json:"-"`
	Basis          string         `json:"-"`
}
type CatalogMetrics struct {
	ProtocolClass     string   `json:"protocol_class"`
	ResponseKind      string   `json:"response_kind"`
	ResponseMS1h      *float64 `json:"response_ms_1h"`
	ResponseSamples1h int64    `json:"response_samples_1h"`
	Success24h        *float64 `json:"success_24h"`
	Samples24h        int64    `json:"samples_24h"`
	Stale             bool     `json:"stale"`
	UpdatedAt         *int64   `json:"updated_at"`
	SubmissionMS1h    *float64 `json:"submission_ms_1h,omitempty"`
}
type CatalogVariant struct {
	ProviderKey  string `json:"provider_key"`
	OperationKey string `json:"operation_key"`
	Health       string `json:"health"`
	CatalogMetrics
	Quote          CatalogQuote                   `json:"quote"`
	Key            model.RoutingMetricKey         `json:"-"`
	ChannelIDs     []int                          `json:"-"`
	Observation    []smartrouting.RankObservation `json:"-"`
	PluginKey      string                         `json:"-"`
	BillingModel   string                         `json:"-"`
	PriceAmbiguous bool                           `json:"-"`
}
type CatalogCandidate struct {
	Model       string           `json:"model"`
	DisplayName string           `json:"display_name"`
	Category    string           `json:"category"`
	Group       string           `json:"group"`
	Available   bool             `json:"available"`
	Health      string           `json:"health"`
	Quote       CatalogQuote     `json:"quote"`
	Metrics     CatalogMetrics   `json:"metrics"`
	Variants    []CatalogVariant `json:"variants,omitempty"`
	Reason      string           `json:"reason,omitempty"`
	ChannelIDs  []int            `json:"-"`
}
type CatalogResult struct {
	Items    []CatalogCandidate `json:"items"`
	Total    int                `json:"total"`
	Page     int                `json:"page"`
	PageSize int                `json:"page_size"`
}
type CatalogPreviewRequest struct {
	CatalogRequest
	Routing smartrouting.Policy `json:"routing"`
}
type CatalogPreviewResult struct {
	CatalogResult
	SelectedGroup    string `json:"selected_group,omitempty"`
	Reason           string `json:"reason"`
	AlgorithmVersion string `json:"algorithm_version"`
}

func catalogFinite(n float64) bool { return n >= 0 && !math.IsNaN(n) && !math.IsInf(n, 0) }
func catalogNumber(n float64) *float64 {
	if !catalogFinite(n) {
		return nil
	}
	return &n
}
func catalogUnknownMetrics(protocol string) CatalogMetrics {
	kind := "response"
	if protocol == "stream" {
		kind = "ttft"
	}
	if protocol == "async" {
		kind = "task_terminal"
	}
	return CatalogMetrics{ProtocolClass: protocol, ResponseKind: kind}
}

func catalogValidate(req *CatalogRequest) error {
	if req.UserID <= 0 {
		return errors.New("routing_identity_unavailable")
	}
	if req.Page == 0 {
		req.Page = 1
	}
	if req.PageSize == 0 {
		req.PageSize = 50
	}
	if req.Page < 1 || req.PageSize < 1 || req.PageSize > 100 || utf8.RuneCountInString(req.Search) > 128 || !utf8.ValidString(req.Search) {
		return errors.New("invalid routing catalog pagination or search")
	}
	if req.Model != "" && (strings.TrimSpace(req.Model) != req.Model || !utf8.ValidString(req.Model) || utf8.RuneCountInString(req.Model) > 255 || strings.ContainsAny(req.Model, "\x00\r\n")) {
		return errors.New("invalid routing catalog model")
	}
	if req.Category == "" {
		req.Category = "all"
	}
	if !slices.Contains([]string{"all", "text", "image", "video", "audio"}, req.Category) {
		return errors.New("invalid routing catalog category")
	}
	if req.Request != nil {
		if req.Model == "" || req.Request.Model != "" && req.Request.Model != req.Model {
			return errors.New("routing preview model mismatch")
		}
		// Same trust-boundary ceiling as relay/helper.ExceedsMaxTokensLimit; helper imports service.
		for _, n := range []*uint{req.Request.MaxTokens, req.Request.MaxCompletionTokens, req.Request.MinTokens} {
			if n != nil && *n > math.MaxInt32/2 {
				return errors.New("max_tokens is invalid")
			}
		}
		if req.Request.N != nil && (*req.Request.N < 1 || *req.Request.N > dto.MaxImageN) {
			return errors.New("n is invalid")
		}
	}
	return nil
}

func RoutingCatalog(ctx context.Context, req CatalogRequest) (CatalogResult, error) {
	if err := catalogValidate(&req); err != nil {
		return CatalogResult{}, err
	}
	items, err := routingCatalogItems(ctx, req)
	if err != nil {
		return CatalogResult{}, err
	}
	result := CatalogResult{Items: []CatalogCandidate{}, Total: len(items), Page: req.Page, PageSize: req.PageSize}
	if req.Page-1 > len(items)/req.PageSize {
		return result, nil
	}
	start := (req.Page - 1) * req.PageSize
	result.Items = items[start:min(start+req.PageSize, len(items))]
	return result, nil
}

func routingCatalogIdentity(db *gorm.DB, req CatalogRequest) (model.User, *model.Token, error) {
	var user model.User
	if err := db.Select("id", "group", "status").First(&user, "id = ?", req.UserID).Error; err != nil {
		return user, nil, fmt.Errorf("routing catalog user: %w", err)
	}
	if user.Status != common.UserStatusEnabled {
		return user, nil, errors.New("routing_identity_unavailable")
	}
	tokenID := req.TokenID
	if req.Token != nil {
		if tokenID != 0 && tokenID != req.Token.Id {
			return user, nil, errors.New("routing_token_owner_mismatch")
		}
		tokenID = req.Token.Id
	}
	if tokenID == 0 {
		return user, nil, nil
	}
	var token model.Token
	if err := db.Select("id", "user_id", "status", "expired_time", "remain_quota", "unlimited_quota", "model_limits_enabled", "model_limits").First(&token, "id = ? AND user_id = ?", tokenID, user.Id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return user, nil, errors.New("routing_token_owner_mismatch")
		}
		return user, nil, fmt.Errorf("routing catalog token: %w", err)
	}
	if token.Status != common.TokenStatusEnabled || token.ExpiredTime != -1 && token.ExpiredTime <= common.GetTimestamp() || !token.UnlimitedQuota && token.RemainQuota <= 0 {
		return user, nil, errors.New("routing_identity_unavailable")
	}
	return user, &token, nil
}

func routingCatalogItems(ctx context.Context, req CatalogRequest) ([]CatalogCandidate, error) {
	if model.DB == nil {
		return nil, errors.New("routing catalog database unavailable")
	}
	db := model.DB.WithContext(ctx)
	user, token, err := routingCatalogIdentity(db, req)
	if err != nil {
		return nil, err
	}
	if token != nil && req.Model != "" && !catalogTokenAllows(token, req.Model) {
		return nil, errors.New("routing_model_forbidden")
	}
	groups := make([]string, 0)
	for group := range GetUserUsableGroups(user.Group) {
		if group != "all" && IsUserSelectableGroup(user.Group, group) {
			groups = append(groups, group)
		}
	}
	sort.Strings(groups)
	var abilities []model.Ability
	query := db.Where("enabled = ?", true)
	if req.Model != "" {
		names := []string{req.Model}
		if normalized := ratio_setting.RoutingMatchModelName(req.Model); normalized != req.Model {
			names = append(names, normalized)
		}
		query = query.Where("model IN ?", names)
	}
	if err = query.Find(&abilities).Error; err != nil {
		return nil, fmt.Errorf("routing catalog abilities: %w", err)
	}
	ids := make([]int, 0)
	names := make([]string, 0)
	for _, a := range abilities {
		ids = append(ids, a.ChannelId)
		names = append(names, a.Model)
	}
	if len(ids) == 0 {
		return []CatalogCandidate{}, nil
	}
	var channels []model.Channel
	// Never load credentials or upstream addresses into this read-only view.
	if err = db.Select("id", "type", "status", "models", "model_mapping", "setting", "settings").Where("id IN ? AND status = ?", ids, common.ChannelStatusEnabled).Find(&channels).Error; err != nil {
		return nil, fmt.Errorf("routing catalog channels: %w", err)
	}
	byID := make(map[int]*model.Channel, len(channels))
	for i := range channels {
		byID[channels[i].Id] = &channels[i]
	}
	// Reuse public metadata, then resolve saved effective prices via the billing resolver.
	public := make(map[string]model.Pricing)
	for _, p := range model.GetPricing() {
		public[p.ModelName] = p
	}
	if req.Model != "" {
		names = append(names, req.Model)
	}
	generation := jsplugin.DefaultRegistry.Generation()
	for _, plugin := range generation.Plugins() {
		names = append(names, plugin.Meta.Models...)
	}
	sort.Strings(names)
	names = slices.Compact(names)
	snapshot, err := model.GetModelPricingSnapshot(names)
	if err != nil {
		return nil, fmt.Errorf("routing catalog pricing: %w", err)
	}
	prices := make(map[string]model.ModelPricingEntry)
	for _, p := range snapshot.Entries {
		prices[p.ModelName] = p
	}
	candidates := make(map[string]*CatalogCandidate)
	for _, a := range abilities {
		name := a.Model
		if req.Model != "" {
			name = req.Model
		}
		if token != nil && !catalogTokenAllows(token, name) {
			continue
		}
		p, visible := public[a.Model]
		if !visible {
			continue
		}
		category := catalogCategory(p)
		if req.Category != "all" && req.Category != category || req.Search != "" && !strings.Contains(strings.ToLower(name), strings.ToLower(req.Search)) {
			continue
		}
		ch := byID[a.ChannelId]
		if ch == nil {
			continue
		}
		if req.Constraints != nil {
			if pin, has, _ := req.Constraints.ResolvedPin(); has && pin.ChannelId != ch.Id {
				continue
			}
			if ok, _ := model.ChannelSatisfiesFilters(ch, name, req.Constraints.Filters); !ok {
				continue
			}
		}
		using := []string{a.Group}
		if a.Group == "all" {
			using = groups
		}
		for _, group := range using {
			if !slices.Contains(groups, group) {
				continue
			}
			id := name + "\x00" + group
			candidate := candidates[id]
			if candidate == nil {
				candidate = &CatalogCandidate{Model: name, DisplayName: name, Category: category, Group: group, Available: true, Health: "unknown", Metrics: catalogUnknownMetrics("unknown")}
				candidates[id] = candidate
			}
			candidate.ChannelIDs = append(candidate.ChannelIDs, ch.Id)
			variants := catalogChannelVariants(req, name, group, ch, p, generation)
			for _, v := range variants {
				index := slices.IndexFunc(candidate.Variants, func(old CatalogVariant) bool { return old.Key == v.Key })
				if index >= 0 {
					if candidate.Variants[index].BillingModel != v.BillingModel {
						candidate.Variants[index].PriceAmbiguous = true
					}
					candidate.Variants[index].ChannelIDs = append(candidate.Variants[index].ChannelIDs, ch.Id)
				} else {
					candidate.Variants = append(candidate.Variants, v)
				}
			}
		}
	}
	out := make([]CatalogCandidate, 0, len(candidates))
	now := time.Now()
	for _, candidate := range candidates {
		sort.Ints(candidate.ChannelIDs)
		candidate.ChannelIDs = slices.Compact(candidate.ChannelIDs)
		ratio := GetUserGroupRatio(user.Group, candidate.Group)
		for i := range candidate.Variants {
			v := &candidate.Variants[i]
			sort.Ints(v.ChannelIDs)
			v.ChannelIDs = slices.Compact(v.ChannelIDs)
			entry := prices[candidate.Model]
			if v.PluginKey != "" {
				configured := slices.ContainsFunc(entry.PluginVariants, func(p model.ModelPricingPluginVariant) bool {
					return p.PluginKey == v.PluginKey && p.Compatible && !p.Stale && p.Effective != ""
				})
				if e, exists := prices[v.BillingModel]; exists && !configured {
					entry = e
				}
			}
			v.Quote = catalogQuote(entry, *v, ratio, req)
			catalogFoldVariant(db, v, now)
		}
		sort.Slice(candidate.Variants, func(i, j int) bool {
			a, b := candidate.Variants[i], candidate.Variants[j]
			if a.ProviderKey != b.ProviderKey {
				return a.ProviderKey < b.ProviderKey
			}
			if a.OperationKey != b.OperationKey {
				return a.OperationKey < b.OperationKey
			}
			return a.ProtocolClass < b.ProtocolClass
		})
		catalogFoldCandidate(candidate)
		out = append(out, *candidate)
	}
	catalogComparable(out)
	sort.Slice(out, func(i, j int) bool {
		if out[i].Model != out[j].Model {
			return out[i].Model < out[j].Model
		}
		return out[i].Group < out[j].Group
	})
	return out, nil
}

func catalogTokenAllows(token *model.Token, name string) bool {
	if !token.ModelLimitsEnabled {
		return true
	}
	limits := token.GetModelLimitsMap()
	return limits[name] || limits[ratio_setting.FormatMatchingModelName(name)] || limits[ratio_setting.RoutingMatchModelName(name)]
}

func catalogCategory(p model.Pricing) string {
	for _, endpoint := range p.SupportedEndpointTypes {
		if endpoint == constant.EndpointTypeOpenAIVideo {
			return "video"
		}
	}
	for _, endpoint := range p.SupportedEndpointTypes {
		if endpoint == constant.EndpointTypeImageGeneration {
			return "image"
		}
	}
	name := strings.ToLower(p.ModelName)
	if strings.Contains(name, "tts") || strings.Contains(name, "whisper") || strings.Contains(name, "audio") {
		return "audio"
	}
	return "text"
}

// RoutingOperationKey accepts only host-defined operations, never a client action label.
func RoutingOperationKey(path string) (string, error) {
	path = strings.TrimSuffix(path, "/")
	if mode, suffix, ok := strings.Cut(path, "/mj/"); ok && mode != "" && strings.HasPrefix(mode, "/") && strings.Count(mode, "/") == 1 {
		path = "/mj/" + suffix
	}
	switch path {
	case "/v1/chat/completions", "/pg/chat/completions", "/v1/completions", "/v1/messages":
		return "chat", nil
	case "/v1/responses":
		return "responses", nil
	case "/v1/responses/compact":
		return "responses.compact", nil
	case "/v1/images/generations":
		return "image.generate", nil
	case "/v1/images/edits":
		return "image.edit", nil
	case "/v1/videos", "/v1/video/generations":
		return "video.create", nil
	case "/v1/audio/speech":
		return "audio.speech", nil
	case "/v1/audio/transcriptions":
		return "audio.transcription", nil
	case "/v1/audio/translations":
		return "audio.translation", nil
	case "/v1/embeddings":
		return "embeddings", nil
	case "/v1/rerank", "/v2/rerank":
		return "rerank", nil
	case "/v1/moderations":
		return "moderations", nil
	case "/v1/realtime":
		return "realtime", nil
	case "/v1/tools/search":
		return "tools.search", nil
	case "/v1/tools/fetch":
		return "tools.fetch", nil
	case "/mj/submit/imagine":
		return "mj.imagine", nil
	case "/mj/submit/variation":
		return "mj.variation", nil
	case "/mj/submit/change", "/mj/submit/simple-change":
		return "mj.change", nil
	case "/mj/submit/action":
		return "mj.action", nil
	case "/mj/submit/shorten":
		return "mj.shorten", nil
	case "/mj/submit/modal":
		return "mj.modal", nil
	case "/mj/submit/edits":
		return "mj.edits", nil
	case "/mj/submit/video":
		return "mj.video", nil
	case "/mj/submit/upload-discord-images":
		return "mj.upload", nil
	case "/mj/submit/blend":
		return "mj.blend", nil
	case "/mj/submit/describe":
		return "mj.describe", nil
	case "/mj/submit/swap-face", "/mj/insight-face/swap":
		return "mj.swap_face", nil
	}
	if strings.HasPrefix(path, "/v1beta/models/") || strings.HasPrefix(path, "/v1/models/") {
		if strings.HasSuffix(path, ":generateContent") || strings.HasSuffix(path, ":streamGenerateContent") {
			return "chat", nil
		}
	}
	return "", errors.New("routing_operation_unresolved")
}

func RoutingObservationKey(c *gin.Context, modelName, group string, channel *model.Channel, protocolClass string) (model.RoutingMetricKey, error) {
	key := model.RoutingMetricKey{ClientModel: modelName, BusinessGroup: group, ProtocolClass: protocolClass}
	if c == nil || c.Request == nil || channel == nil || modelName == "" || group == "" || !slices.Contains([]string{"sync", "stream", "async", "responses_ws", "realtime"}, protocolClass) {
		return key, errors.New("routing_variant_unresolved")
	}
	key.ProviderKey = "builtin:" + strconv.Itoa(channel.Type) + ":v1"
	var generation *jsplugin.RoutingGeneration
	pluginKey := ""
	if value, ok := c.Get(jsplugin.ContextKeyPinnedPlugin); ok {
		if pin, ok := value.(jsplugin.PinnedPlugin); ok && pin.Plugin != nil && pin.Generation != nil {
			key.ProviderKey = "plugin:" + pin.Plugin.Meta.Key + ":" + strconv.FormatUint(pin.Generation.Number, 10)
			generation, pluginKey = pin.Generation, pin.Plugin.Meta.Key
		}
	}
	if generation == nil {
		if value, ok := c.Get("mowan_routing_task_endpoint"); ok {
			if endpoint, ok := value.(jsplugin.PinnedEndpoint); ok && endpoint.Generation != nil {
				for _, candidate := range endpoint.Candidates {
					if candidate.Plugin == nil {
						continue
					}
					registered, exists := endpoint.Generation.Get(candidate.Plugin.Meta.Key)
					if !exists || registered != candidate.Plugin {
						continue
					}
					matches, _ := model.ChannelSatisfiesFilters(channel, modelName, []hostdto.ChannelFilter{{Kind: hostdto.FilterTaskPluginIdentity, TaskPluginKey: candidate.Plugin.Meta.Key, TaskPluginChannelTypes: candidate.Plugin.Meta.ChannelTypes}})
					if !matches {
						continue
					}
					generation, pluginKey = endpoint.Generation, candidate.Plugin.Meta.Key
					key.ProviderKey = "plugin:" + pluginKey + ":" + strconv.FormatUint(generation.Number, 10)
					key.ProtocolClass = "async"
					break
				}
			}
		}
	}
	path := c.FullPath()
	if path == "" {
		path = c.Request.URL.Path
	}
	operation, err := catalogOperationKey(generation, pluginKey, c.Request.Method, path)
	if err != nil {
		return key, err
	}
	key.OperationKey = operation
	if channel.Type == constant.ChannelTypeTaskPlugin && strings.HasPrefix(key.ProviderKey, "builtin:") {
		return key, errors.New("routing_provider_unresolved")
	}
	return key, nil
}

func catalogOperationKey(g *jsplugin.RoutingGeneration, pluginKey, method, path string) (string, error) {
	if pluginKey != "" {
		for _, binding := range g.LookupDeclaredRoutes(method, path) {
			if binding.Plugin.Meta.Key == pluginKey && binding.Route.Type != jsplugin.RouteTypeQuery && binding.Route.Action != "" {
				return "native:" + binding.Route.Action, nil
			}
		}
	}
	return RoutingOperationKey(path)
}

func catalogChannelVariants(req CatalogRequest, name, group string, ch *model.Channel, p model.Pricing, g *jsplugin.RoutingGeneration) []CatalogVariant {
	type provider struct{ key, plugin, model string }
	providers := []provider{}
	billingModel := name
	if ch.ModelMapping != nil && *ch.ModelMapping != "" {
		var mapping map[string]string
		if common.UnmarshalJsonStr(*ch.ModelMapping, &mapping) != nil {
			return nil
		}
		seen := map[string]bool{}
		for mapping[billingModel] != "" && mapping[billingModel] != billingModel {
			if seen[billingModel] {
				return nil
			}
			seen[billingModel] = true
			billingModel = mapping[billingModel]
		}
	}
	for _, plugin := range g.Plugins() {
		_, declared, serves := model.ResolveTaskPluginModel(g, billingModel, plugin.Meta.Models)
		if !serves {
			_, declared, serves = model.ResolveTaskPluginModel(g, name, plugin.Meta.Models)
		}
		bound := slices.Contains(plugin.Meta.ChannelTypes, ch.Type) || (ch.Type == constant.ChannelTypeTaskPlugin || ch.Type == constant.ChannelTypeNewAPI) && ch.GetSetting().BindsTaskPlugin(plugin.Meta.Key)
		if serves && bound {
			providers = append(providers, provider{"plugin:" + plugin.Meta.Key + ":" + strconv.FormatUint(g.Number, 10), plugin.Meta.Key, declared})
		}
	}
	if ch.Type != constant.ChannelTypeTaskPlugin {
		providers = append(providers, provider{"builtin:" + strconv.Itoa(ch.Type) + ":v1", "", billingModel})
	}
	out := []CatalogVariant{}
	for _, provider := range providers {
		if provider.plugin != "" && (req.ProtocolClass == "responses_ws" || req.ProtocolClass == "realtime") {
			continue
		}
		allowed := true
		if req.Constraints != nil {
			for _, filter := range req.Constraints.Filters {
				switch filter.Kind {
				case hostdto.FilterTaskPluginIdentity:
					if filter.TaskPluginKey == "" {
						allowed = allowed && provider.plugin == ""
					} else {
						allowed = allowed && (provider.plugin == filter.TaskPluginKey || slices.Contains(filter.TaskPluginKeys, provider.plugin))
					}
				case hostdto.FilterTaskPluginEndpoint:
					if filter.TaskPluginRelayOnly {
						allowed = allowed && provider.plugin == ""
					} else if provider.plugin != "" {
						allowed = allowed && slices.Contains(filter.TaskPluginKeys, provider.plugin)
					}
				}
			}
		}
		if !allowed {
			continue
		}
		paths := []string{}
		if req.RequestPath != "" {
			paths = []string{req.RequestPath}
		} else {
			for _, endpoint := range common.GetEndpointTypesByChannelType(ch.Type, name) {
				if endpoint == constant.EndpointTypeOpenAIVideo {
					paths = append(paths, "/v1/videos")
					continue
				}
				if info, ok := common.GetDefaultEndpointInfo(endpoint); ok {
					paths = append(paths, info.Path)
				}
			}
			if len(paths) == 0 {
				for _, endpoint := range p.SupportedEndpointTypes {
					if info, ok := common.GetDefaultEndpointInfo(endpoint); ok {
						paths = append(paths, info.Path)
					}
				}
			}
			if provider.plugin != "" {
				paths = nil
				for _, definition := range jsplugin.HostProtocols() {
					for _, operation := range definition.Operations {
						for _, binding := range g.LookupEndpointCandidates("POST", operation.Path, provider.model) {
							if binding.Plugin.Meta.Key == provider.plugin {
								paths = append(paths, operation.Path)
							}
						}
					}
				}
				for _, binding := range g.Routes() {
					if binding.Plugin.Meta.Key == provider.plugin && binding.Route.Method == "POST" && binding.Route.Type != jsplugin.RouteTypeQuery && (len(binding.Route.Models) == 0 || slices.Contains(binding.Route.Models, provider.model)) {
						paths = append(paths, binding.Route.Path)
					}
				}
			}
		}
		for _, path := range paths {
			if provider.plugin != "" && req.RequestPath != "" {
				endpoint := slices.ContainsFunc(g.LookupEndpointCandidates("POST", path, provider.model), func(binding jsplugin.ProtocolBinding) bool { return binding.Plugin.Meta.Key == provider.plugin })
				native := slices.ContainsFunc(g.LookupDeclaredRoutes("POST", path), func(binding jsplugin.RouteBinding) bool { return binding.Plugin.Meta.Key == provider.plugin })
				if !endpoint && !native {
					continue
				}
			}
			operation, err := catalogOperationKey(g, provider.plugin, "POST", path)
			if err != nil {
				continue
			}
			protocols := []string{"sync"}
			if provider.plugin != "" || strings.HasPrefix(operation, "video.") || strings.HasPrefix(operation, "mj.") {
				protocols = []string{"async"}
			} else if operation == "chat" || operation == "responses" {
				protocols = []string{"sync", "stream"}
			}
			if req.ProtocolClass != "" && provider.plugin == "" {
				protocols = []string{req.ProtocolClass}
			}
			for _, protocol := range protocols {
				key := model.RoutingMetricKey{ClientModel: name, BusinessGroup: group, ProtocolClass: protocol, ProviderKey: provider.key, OperationKey: operation}
				if slices.ContainsFunc(out, func(v CatalogVariant) bool { return v.Key == key }) {
					continue
				}
				out = append(out, CatalogVariant{ProviderKey: key.ProviderKey, OperationKey: operation, Health: "unknown", CatalogMetrics: catalogUnknownMetrics(protocol), Key: key, ChannelIDs: []int{ch.Id}, PluginKey: provider.plugin, BillingModel: provider.model})
			}
		}
	}
	return out
}

func catalogQuote(entry model.ModelPricingEntry, v CatalogVariant, ratio float64, req CatalogRequest) CatalogQuote {
	q := CatalogQuote{Comparison: "unavailable", Reason: "price_unconfigured", Currency: "USD", Prices: []CatalogPrice{}}
	if v.PriceAmbiguous {
		q.Reason = "channel_pricing_ambiguous"
		return q
	}
	if !catalogFinite(ratio) {
		q.Reason = "group_ratio_invalid"
		return q
	}
	effective := entry.Effective
	expression, _ := effective["billing_setting.billing_expr"].(string)
	mode, _ := effective["billing_setting.billing_mode"].(string)
	task := v.PluginKey != ""
	if task {
		expression = ""
		for _, variant := range entry.PluginVariants {
			if variant.PluginKey == v.PluginKey && !variant.Stale && variant.Compatible {
				expression = variant.Effective
				break
			}
		}
		if expression == "" {
			q.Reason = "provider_price_unavailable"
			return q
		}
		mode = billing_setting.BillingModeTieredExpr
	}
	if mode == billing_setting.BillingModeTieredExpr {
		if expression == "" {
			return q
		}
		if _, err := billingexpr.CompileFromCache(expression); err != nil {
			q.Reason = "billing_expression_invalid"
			return q
		}
		unit := "USD (expression)"
		if task {
			unit = "USD (task usage)"
		}
		q.Prices = append(q.Prices, CatalogPrice{Label: "billing_expression", Unit: unit, Rule: expression + "; group_ratio=" + strconv.FormatFloat(ratio, 'g', -1, 64)})
		q.Basis = "expression:" + expression + ":" + strconv.FormatBool(task) + ":" + v.ProviderKey
		q.Comparison, q.Reason, q.Cost = "available", "same_billing_contract", ratio
		if req.Request != nil && !task && v.Key.OperationKey == "chat" {
			params, request, ok := catalogEstimate(req)
			if !ok {
				q.Comparison, q.Reason = "unavailable", "request_usage_unknown"
				return q
			}
			usageUnknown := len(billingexpr.UsedUsageKeys(expression)) > 0
			for variable := range billingexpr.UsedVars(expression) {
				if !slices.Contains([]string{"p", "c", "len", "tier", "fixed", "param", "has", "min", "max", "abs", "ceil", "floor"}, variable) {
					usageUnknown = true
				}
			}
			if usageUnknown {
				q.Comparison, q.Reason = "unavailable", "request_usage_unknown"
				return q
			}
			raw, _, err := billingexpr.RunExprWithRequest(expression, params, request)
			if err != nil || !catalogFinite(raw*ratio/1e6) {
				q.Comparison, q.Reason = "unavailable", "estimate_unavailable"
				return q
			}
			q.EstimatedTotal = catalogNumber(raw * ratio / 1e6)
			q.Cost = *q.EstimatedTotal
			q.Basis = "estimated_usd"
			q.Reason = "estimated_output_limit"
		}
		return q
	}
	add := func(label string, amount float64, unit string) {
		q.Prices = append(q.Prices, CatalogPrice{Label: label, Amount: catalogNumber(amount * ratio), Unit: unit})
	}
	if price, ok := effective["ModelPrice"].(float64); ok {
		if !catalogFinite(price) {
			return q
		}
		unit := "USD/request"
		if entry.BillingDetails.ImageCount {
			unit = "USD/image"
		}
		add("request", price, unit)
		if q.Prices[0].Amount == nil {
			q.Reason = "price_invalid"
			return q
		}
		q.Basis = "legacy:" + unit + ":" + strconv.FormatFloat(price, 'g', -1, 64) + ":" + v.ProviderKey
		q.Cost = ratio
		if price == 0 {
			q.Cost = 0
		}
		q.Comparison = "available"
		q.Reason = "same_billing_contract"
		if len(entry.BillingDetails.RequestRules) > 0 {
			for _, r := range entry.BillingDetails.RequestRules {
				q.Prices = append(q.Prices, CatalogPrice{Label: "request_rule", Amount: catalogNumber(r.Multiplier), Unit: "multiplier", Rule: r.Condition})
			}
			q.Basis += fmt.Sprint(entry.BillingDetails.RequestRules)
		}
		// Without validated image/provider quantities a unit price is not a request total.
		if req.Request != nil && unit == "USD/request" && len(entry.BillingDetails.RequestRules) == 0 {
			if _, _, ok := catalogEstimate(req); ok {
				q.EstimatedTotal = catalogNumber(price * ratio)
				q.Cost = price * ratio
				q.Basis = "estimated_usd"
				q.Reason = "estimated_request"
			}
		}
		return q
	}
	base, ok := effective["ModelRatio"].(float64)
	if !ok || !catalogFinite(common.QuotaPerUnit) || common.QuotaPerUnit == 0 || !catalogFinite(base) {
		return q
	}
	base = base * 1e6 / common.QuotaPerUnit
	add("input", base, "USD/1M tokens")
	for _, field := range []struct{ key, label string }{{"CompletionRatio", "output"}, {"CacheRatio", "cache_read"}, {"ImageRatio", "image_input"}} {
		if n, ok := effective[field.key].(float64); ok {
			add(field.label, base*n, "USD/1M tokens")
		}
	}
	if entry.CacheWriteMode != model.CacheWriteNone {
		if n, ok := effective["CreateCacheRatio"].(float64); ok {
			add("cache_write", base*n, "USD/1M tokens")
			if entry.CacheWriteMode == model.CacheWriteClaudeTTL {
				add("cache_write_1h", base*n*1.6, "USD/1M tokens")
			}
			if entry.CacheWriteMode == model.CacheWriteOpenAITTL {
				add("cache_write_12h", base*n*2, "USD/1M tokens")
			}
		}
	}
	if n := entry.BillingDetails.AudioInputPrice; n != nil {
		add("audio_input", *n, "USD/1M tokens")
	}
	if n := entry.BillingDetails.AudioOutputPrice; n != nil {
		add("audio_output", *n, "USD/1M tokens")
	}
	encoded, _ := common.Marshal(entry.ModelPricingDescription)
	q.Basis = "legacy:" + string(encoded) + ":" + v.ProviderKey
	q.Cost = ratio
	allZero := len(q.Prices) > 0
	for _, price := range q.Prices {
		if price.Amount == nil {
			q.Reason = "price_invalid"
			return q
		}
		allZero = allZero && *price.Amount == 0
	}
	if allZero {
		q.Cost = 0
	}
	q.Comparison = "available"
	q.Reason = "same_billing_contract"
	if req.Request != nil && v.Key.OperationKey == "chat" && !task {
		params, _, ok := catalogEstimate(req)
		completion, _ := effective["CompletionRatio"].(float64)
		if !ok || entry.BillingDetails.InvalidAudioPrice || entry.BillingDetails.ConflictingAudioPrices {
			q.Comparison, q.Reason = "unavailable", "request_usage_unknown"
		} else {
			q.EstimatedTotal = catalogNumber((params.P + params.C*completion) * base * ratio / 1e6)
			if q.EstimatedTotal != nil {
				q.Cost = *q.EstimatedTotal
				q.Basis = "estimated_usd"
				q.Reason = "estimated_output_limit"
			} else {
				q.Comparison, q.Reason = "unavailable", "estimate_unavailable"
			}
		}
	}
	return q
}

func catalogEstimate(req CatalogRequest) (billingexpr.TokenParams, billingexpr.RequestInput, bool) {
	var params billingexpr.TokenParams
	if req.Request == nil {
		return params, billingexpr.RequestInput{}, false
	}
	request := *req.Request
	request.Model = req.Model
	output := uint(1024)
	if request.MaxTokens != nil {
		output = *request.MaxTokens
	}
	if request.MaxCompletionTokens != nil {
		output = *request.MaxCompletionTokens
	}
	if output > math.MaxInt32/2 || len(request.Messages) == 0 || req.RequestPath != "" && !slices.Contains([]string{"/v1/chat/completions", "/pg/chat/completions"}, req.RequestPath) {
		return params, billingexpr.RequestInput{}, false
	}
	// Only known text contributes to a complete estimate; unknown content is not zero usage.
	for _, message := range request.Messages {
		if len(message.Tools) > 0 {
			return params, billingexpr.RequestInput{}, false
		}
		if _, ok := message.Content.(string); !ok {
			body, err := common.Marshal(message.Content)
			var content []struct {
				Type string  `json:"type"`
				Text *string `json:"text"`
			}
			if err != nil || common.Unmarshal(body, &content) != nil || len(content) == 0 {
				return params, billingexpr.RequestInput{}, false
			}
			for _, part := range content {
				if part.Type != dto.ContentTypeText || part.Text == nil {
					return params, billingexpr.RequestInput{}, false
				}
			}
		}
	}
	meta := request.GetTokenCountMeta()
	if len(meta.Files) > 0 || len(request.Tools) > 0 || len(request.Functions) > 0 || request.WebSearchOptions != nil || len(request.Audio) > 0 || len(request.Modalities) > 0 || len(request.ExtraBody) > 0 || len(request.SearchParameters) > 0 || len(request.EnableSearch) > 0 || len(request.SearchOptions) > 0 || len(request.WebSearch) > 0 || len(request.ServiceTier) > 0 || request.N != nil && *request.N != 1 {
		return params, billingexpr.RequestInput{}, false
	}
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/chat/completions", nil)
	common.SetContextKey(c, constant.ContextKeyOriginalModel, req.Model)
	count, err := CountRequestToken(c, meta, &relaycommon.RelayInfo{RelayFormat: types.RelayFormatOpenAI})
	if err != nil {
		return params, billingexpr.RequestInput{}, false
	}
	params = billingexpr.TokenParams{P: float64(count), Len: float64(count), C: float64(output)}
	body, err := common.Marshal(request)
	return params, billingexpr.RequestInput{Body: body}, err == nil
}

func catalogFoldVariant(db *gorm.DB, v *CatalogVariant, now time.Time) {
	store := model.RoutingMetricsStore{DB: db}
	stages := []model.RoutingMetricStage{model.RoutingStageAttempt}
	if v.Key.ProtocolClass == "async" {
		stages = append(stages, model.RoutingStageTaskTerminal)
	}
	v.Health = "unknown"
	for _, stage := range stages {
		observation := smartrouting.RankObservation{Key: smartrouting.RankKey{ClientModel: v.Key.ClientModel, BusinessGroup: v.Key.BusinessGroup, ProtocolClass: v.Key.ProtocolClass, ProviderKey: v.Key.ProviderKey, OperationKey: v.Key.OperationKey}, Stage: string(stage)}
		day, err := store.Fold(v.Key, stage, v.ChannelIDs, now.Add(-24*time.Hour), now)
		hour, hourErr := store.Fold(v.Key, stage, v.ChannelIDs, now.Add(-time.Hour), now)
		if err != nil || hourErr != nil {
			v.Observation = append(v.Observation, observation)
			continue
		}
		observation.Eligible, observation.Success, observation.SpeedSamples = day.Eligible, day.Success, day.SpeedSamples
		if day.SpeedSamples > 0 {
			observation.MeanMS = day.SpeedTotalMS / float64(day.SpeedSamples)
		}
		observation.Response1hSamples = hour.SpeedSamples
		if hour.SpeedSamples > 0 {
			observation.Response1hMeanMS = hour.SpeedTotalMS / float64(hour.SpeedSamples)
		}
		if day.LastObservationAt > 0 {
			observation.LastObservation = time.Unix(day.LastObservationAt, 0)
		}
		observation.ActiveKnown, observation.Active = true, day.Active
		health, healthErr := store.ReadHealth(v.Key, stage, day.Active, now)
		if healthErr == nil {
			observation.Sent60s, observation.Limited60s = health.Sent60s, health.RateLimited60s
		}
		status := "unknown"
		if healthErr == nil && health.Health.LastEventAt > 0 {
			status = "normal"
			if health.Congested {
				status = "congested"
			}
			if health.Health.State != model.RoutingHealthClosed {
				status = "soft_offline"
			}
		}
		if stage == model.RoutingStageAttempt {
			v.Health = status
		} else if status == "soft_offline" || v.Health == "normal" {
			v.Health = status
		}
		v.Observation = append(v.Observation, observation)
		if v.Key.ProtocolClass == "async" && stage == model.RoutingStageAttempt {
			if hour.SpeedSamples > 0 {
				v.SubmissionMS1h = catalogNumber(observation.Response1hMeanMS)
			}
			continue
		}
		v.Samples24h = day.Eligible
		v.ResponseSamples1h = hour.SpeedSamples
		if day.Eligible > 0 {
			v.Success24h = catalogNumber(float64(day.Success) / float64(day.Eligible))
		}
		if hour.SpeedSamples >= 5 {
			v.ResponseMS1h = catalogNumber(observation.Response1hMeanMS)
		}
		if day.LastObservationAt > 0 {
			updated := day.LastObservationAt
			v.UpdatedAt = &updated
			v.Stale = now.Sub(time.Unix(updated, 0)) > 10*time.Minute
		}
	}
}

func catalogFoldCandidate(c *CatalogCandidate) {
	c.Quote = CatalogQuote{Comparison: "unavailable", Reason: "variant_unresolved", Currency: "USD", Prices: []CatalogPrice{}}
	if len(c.Variants) == 0 {
		return
	}
	c.Quote = c.Variants[0].Quote
	if len(c.Variants) == 1 {
		c.Metrics = c.Variants[0].CatalogMetrics
		c.Health = c.Variants[0].Health
		return
	}
	allNormal, allOffline, hasOffline, hasKnown, allKnown := true, true, false, false, true
	for _, v := range c.Variants {
		allNormal = allNormal && v.Health == "normal"
		allOffline = allOffline && v.Health == "soft_offline"
		hasOffline = hasOffline || v.Health == "soft_offline"
		hasKnown = hasKnown || v.Health == "normal" || v.Health == "congested"
		allKnown = allKnown && v.Health != "unknown"
		if v.Quote.Basis != c.Quote.Basis || v.Quote.Comparison != "available" || v.Quote.EstimatedTotal != nil && c.Quote.EstimatedTotal != nil && *v.Quote.EstimatedTotal != *c.Quote.EstimatedTotal {
			c.Quote.Comparison = "unavailable"
			c.Quote.Reason = "mixed_provider_pricing"
			c.Quote.EstimatedTotal = nil
		}
	}
	c.Reason = "mixed_provider_operation"
	if c.Quote.Reason == "mixed_provider_pricing" {
		c.Quote.Prices = catalogPriceRanges(c.Variants)
	}
	c.Health = "unknown"
	if allNormal {
		c.Health = "normal"
	} else if allOffline {
		c.Health = "soft_offline"
	} else if hasOffline && hasKnown {
		c.Health = "degraded"
	} else if hasKnown && !hasOffline && allKnown {
		c.Health = "congested"
	}
}

func catalogPriceRanges(variants []CatalogVariant) []CatalogPrice {
	prices := []CatalogPrice{}
	for _, variant := range variants {
		for _, price := range variant.Quote.Prices {
			if price.Amount == nil {
				if !slices.ContainsFunc(prices, func(p CatalogPrice) bool {
					return p.Label == price.Label && p.Unit == price.Unit && p.Rule == price.Rule
				}) {
					prices = append(prices, price)
				}
				continue
			}
			i := slices.IndexFunc(prices, func(p CatalogPrice) bool {
				return p.Label == price.Label && p.Unit == price.Unit && p.Rule == price.Rule
			})
			if i < 0 {
				prices = append(prices, CatalogPrice{Label: price.Label, Min: catalogNumber(*price.Amount), Max: catalogNumber(*price.Amount), Unit: price.Unit, Rule: price.Rule})
			} else {
				p := &prices[i]
				if p.Min != nil && *price.Amount < *p.Min {
					p.Min = catalogNumber(*price.Amount)
				}
				if p.Max != nil && *price.Amount > *p.Max {
					p.Max = catalogNumber(*price.Amount)
				}
			}
		}
	}
	return prices
}

// Only equal contracts may compare by ratio. Different contracts need complete USD estimates.
func catalogComparable(items []CatalogCandidate) {
	basis := map[string]string{}
	mixed := map[string]bool{}
	for _, c := range items {
		if c.Quote.Comparison != "available" {
			continue
		}
		if old, ok := basis[c.Model]; ok && old != c.Quote.Basis {
			mixed[c.Model] = true
		} else {
			basis[c.Model] = c.Quote.Basis
		}
	}
	for i := range items {
		if mixed[items[i].Model] && items[i].Quote.EstimatedTotal == nil {
			items[i].Quote.Comparison = "unavailable"
			items[i].Quote.Reason = "different_billing_contract"
		}
	}
}

func RoutingCatalogPreview(ctx context.Context, req CatalogPreviewRequest) (CatalogPreviewResult, error) {
	out := CatalogPreviewResult{AlgorithmVersion: smartrouting.RankAlgorithmVersion}
	if err := catalogValidate(&req.CatalogRequest); err != nil {
		return out, err
	}
	if req.Model == "" {
		return out, errors.New("model is required")
	}
	if err := req.Routing.Validate(); err != nil {
		return out, err
	}
	if req.Request != nil {
		if req.RequestPath == "" {
			req.RequestPath = "/v1/chat/completions"
		}
		req.ProtocolClass = "sync"
		if req.Request.Stream != nil && *req.Request.Stream {
			req.ProtocolClass = "stream"
		}
	}
	items, err := routingCatalogItems(ctx, req.CatalogRequest)
	if err != nil {
		return out, err
	}
	strategy := req.Routing.Strategy
	order := []string{}
	tryOffline := false
	if req.Routing.Mode == "fixed" {
		strategy = "manual"
		group := *req.Routing.FixedGroup
		if group == "" {
			var user model.User
			if err = model.DB.Select("group").First(&user, "id = ?", req.UserID).Error; err != nil {
				return out, err
			}
			group = user.Group
		}
		order = []string{group}
	}
	if strategy == "custom" {
		policy, ok := req.Routing.Models[req.Model]
		if !ok {
			return out, errors.New("routing_model_not_configured")
		}
		strategy, order, tryOffline = policy.Strategy, policy.Groups, policy.TrySoftOffline
	}
	ranked := []smartrouting.RankCandidate{}
	byGroup := map[string]CatalogCandidate{}
	for _, c := range items {
		if len(order) > 0 && !slices.Contains(order, c.Group) || slices.Contains(req.Routing.ExcludedModels, c.Model) || slices.Contains(req.Routing.ExcludedGroups, c.Group) || slices.Contains(req.Routing.ExcludedModelGroups[c.Model], c.Group) || c.Health == "soft_offline" && !tryOffline {
			continue
		}
		byGroup[c.Group] = c
		ranked = append(ranked, catalogRankCandidate(c))
	}
	if strategy == "manual" {
		sort.SliceStable(ranked, func(i, j int) bool {
			return slices.Index(order, ranked[i].Group) < slices.Index(order, ranked[j].Group)
		})
	}
	ranking, err := smartrouting.Rank(smartrouting.RankRequest{Strategy: strategy, ClientModel: req.Model, RequestID: common.GetUUID(), Now: time.Now(), Candidates: ranked})
	if err != nil {
		return out, err
	}
	out.CatalogResult = CatalogResult{Items: []CatalogCandidate{}, Total: len(ranked), Page: 1, PageSize: len(ranked)}
	out.Reason = ranking.FallbackReason
	for _, r := range ranking.Candidates {
		out.Items = append(out.Items, byGroup[r.Group])
	}
	if len(out.Items) > 0 {
		out.SelectedGroup = out.Items[0].Group
		if out.Reason == "" {
			out.Reason = "strategy_selected"
		}
	} else {
		out.Reason = "routing_no_candidate"
	}
	return out, nil
}
func catalogRankCandidate(c CatalogCandidate) smartrouting.RankCandidate {
	out := smartrouting.RankCandidate{Group: c.Group, Comparable: c.Quote.Comparison == "available", Cost: c.Quote.Cost}
	for _, v := range c.Variants {
		out.Observations = append(out.Observations, v.Observation...)
	}
	return out
}

// RoutingRankCandidates preserves the supplied group set and saved manual order.
func RoutingRankCandidates(c *gin.Context, clientModel string, groups []string) ([]smartrouting.RankCandidate, error) {
	if c == nil || c.Request == nil {
		return nil, errors.New("routing request unavailable")
	}
	req := CatalogRequest{UserID: common.GetContextKeyInt(c, constant.ContextKeyUserId), TokenID: common.GetContextKeyInt(c, constant.ContextKeyTokenId), Model: clientModel, Category: "all", Constraints: GetChannelConstraints(c)}
	if c.Request != nil {
		req.RequestPath = c.Request.URL.Path
	}
	req.ProtocolClass = RoutingProtocolClass(c)
	// Distribution precedes relay validation: a cached body is not yet trusted billing input.
	if c.Request.Method == "POST" && slices.Contains([]string{"/v1/chat/completions", "/pg/chat/completions"}, req.RequestPath) &&
		slices.Contains([]string{"sync", "stream"}, req.ProtocolClass) && strings.HasPrefix(c.Request.Header.Get("Content-Type"), "application/json") {
		var request dto.GeneralOpenAIRequest
		if common.UnmarshalBodyReusable(c, &request) == nil && request.Model == clientModel {
			req.Request = &request
			if catalogValidate(&req) != nil {
				req.Request = nil
			}
		}
	}
	items, err := routingCatalogItems(c.Request.Context(), req)
	if err != nil {
		return nil, err
	}
	byGroup := map[string]CatalogCandidate{}
	for _, item := range items {
		byGroup[item.Group] = item
	}
	out := make([]smartrouting.RankCandidate, 0, len(groups))
	for _, group := range groups {
		if item, ok := byGroup[group]; ok {
			out = append(out, catalogRankCandidate(item))
		} else {
			out = append(out, smartrouting.RankCandidate{Group: group})
		}
	}
	return out, nil
}
