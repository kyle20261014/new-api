package controller

import (
	"errors"
	"io"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/internal/mowan/smartrouting"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
)

func routingCatalogQuery(c *gin.Context) (service.CatalogRequest, error) {
	req := service.CatalogRequest{UserID: c.GetInt("id"), Model: c.Query("model"), Search: c.Query("search"), Category: c.DefaultQuery("category", "all"), Page: 1, PageSize: 50}
	for key, target := range map[string]*int{"p": &req.Page, "page_size": &req.PageSize} {
		if raw, exists := c.GetQuery(key); exists {
			value, err := strconv.Atoi(raw)
			if err != nil || value < 1 || (key == "page_size" && value > 100) {
				return req, common.NewMessage("Invalid routing pagination")
			}
			*target = value
		}
	}
	if c.GetBool("mowan_routing_api_key_auth") {
		req.TokenID = c.GetInt("token_id")
	} else if raw, exists := c.GetQuery("token_id"); exists {
		value, err := strconv.Atoi(raw)
		if err != nil || value <= 0 {
			return req, common.NewMessage("Invalid token ID")
		}
		req.TokenID = value
	}
	if !slices.Contains([]string{"all", "text", "image", "video", "audio"}, req.Category) || len(req.Search) > 512 || len(req.Model) > 1020 {
		return req, common.NewMessage("Invalid routing search or category")
	}
	return req, nil
}

func routingCatalogError(c *gin.Context, err error) {
	var message *common.Message
	if errors.As(err, &message) || strings.HasPrefix(err.Error(), "invalid routing") || strings.HasPrefix(err.Error(), "routing preview model") || strings.HasPrefix(err.Error(), "max_tokens") || strings.HasPrefix(err.Error(), "n is invalid") {
		common.ApiErrorStatus(c, http.StatusBadRequest, common.NewMessage("Invalid routing catalog request"))
	} else if strings.Contains(err.Error(), "routing_token_owner_mismatch") || strings.Contains(err.Error(), "routing_identity_unavailable") || strings.Contains(err.Error(), "routing_model_forbidden") {
		common.ApiErrorStatus(c, http.StatusForbidden, common.NewMessage("Routing identity or model is unavailable"))
	} else {
		common.ApiErrorStatus(c, http.StatusServiceUnavailable, common.NewMessage("Routing catalog is unavailable"))
	}
}

func GetRoutingCandidates(c *gin.Context) {
	req, err := routingCatalogQuery(c)
	if err != nil {
		routingCatalogError(c, err)
		return
	}
	result, err := service.RoutingCatalog(c.Request.Context(), req)
	if err != nil {
		routingCatalogError(c, err)
		return
	}
	common.ApiSuccess(c, result)
}

func PreviewRouting(c *gin.Context) {
	data, err := io.ReadAll(io.LimitReader(c.Request.Body, 2<<20+1))
	if err != nil || len(data) > 2<<20 {
		common.ApiErrorStatus(c, http.StatusBadRequest, common.NewMessage("Routing preview request is too large or unreadable"))
		return
	}
	var fields map[string]common.RawMessage
	if common.Unmarshal(data, &fields) != nil || fields == nil || fields["routing"] == nil || fields["model"] == nil {
		common.ApiErrorStatus(c, http.StatusBadRequest, common.NewMessage("Routing preview requires routing and model"))
		return
	}
	for key := range fields {
		if key != "routing" && key != "model" && key != "request" {
			common.ApiErrorStatus(c, http.StatusBadRequest, common.NewMessage("Invalid routing preview field"))
			return
		}
	}
	policy, err := smartrouting.ParsePolicy(fields["routing"])
	if err != nil {
		tokenRoutingError(c, err)
		return
	}
	req, err := routingCatalogQuery(c)
	if err != nil {
		routingCatalogError(c, err)
		return
	}
	if common.Unmarshal(fields["model"], &req.Model) != nil || strings.TrimSpace(req.Model) == "" {
		common.ApiErrorStatus(c, http.StatusBadRequest, common.NewMessage("Routing preview model is required"))
		return
	}
	if raw := fields["request"]; raw != nil {
		if strings.TrimSpace(string(raw)) == "null" {
			common.ApiErrorStatus(c, http.StatusBadRequest, common.NewMessage("Invalid routing preview request"))
			return
		}
		req.Request = &dto.GeneralOpenAIRequest{}
		var requestFields map[string]common.RawMessage
		if common.Unmarshal(raw, &requestFields) != nil || requestFields == nil {
			common.ApiErrorStatus(c, http.StatusBadRequest, common.NewMessage("Invalid routing preview request"))
			return
		}
		allowed := []string{"model", "messages", "stream", "max_tokens", "max_completion_tokens", "min_tokens", "n", "temperature", "top_p", "stop", "seed", "presence_penalty", "frequency_penalty", "response_format", "tools", "tool_choice", "parallel_tool_calls", "user", "reasoning_effort", "stream_options", "metadata", "service_tier", "modalities", "audio", "web_search_options"}
		for field := range requestFields {
			if !slices.Contains(allowed, field) {
				common.ApiErrorStatus(c, http.StatusBadRequest, common.NewMessage("Invalid routing preview field"))
				return
			}
		}
		if common.Unmarshal(raw, req.Request) != nil {
			common.ApiErrorStatus(c, http.StatusBadRequest, common.NewMessage("Invalid routing preview request"))
			return
		}
	}
	result, err := service.RoutingCatalogPreview(c.Request.Context(), service.CatalogPreviewRequest{CatalogRequest: req, Routing: policy})
	if err != nil {
		routingCatalogError(c, err)
		return
	}
	common.ApiSuccess(c, result)
}
