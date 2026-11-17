package controller

import (
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/internal/mowan/smartrouting"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func GetRoutingSelf(c *gin.Context) {
	token, err := model.GetTokenByIds(c.GetInt("token_id"), c.GetInt("id"))
	if err != nil {
		tokenRoutingError(c, err)
		return
	}
	response, err := loadMaskedTokenResponse(model.DB.WithContext(c.Request.Context()), token)
	if err != nil {
		tokenRoutingError(c, err)
		return
	}
	// No owner identity, key material, IP list or limits are exposed by this DTO.
	common.ApiSuccess(c, gin.H{"routing": response.Routing, "routing_version": response.RoutingVersion, "allow_routing_self_write": response.AllowRoutingSelfWrite})
}

func PutRoutingSelf(c *gin.Context) {
	data, err := io.ReadAll(io.LimitReader(c.Request.Body, smartrouting.MaxPolicyBytes+1025))
	if err != nil || len(data) > smartrouting.MaxPolicyBytes+1024 {
		common.ApiErrorStatus(c, http.StatusBadRequest, common.NewMessage("Routing request is too large or unreadable"))
		return
	}
	var fields map[string]common.RawMessage
	if common.Unmarshal(data, &fields) != nil || len(fields) != 2 || fields["expected_version"] == nil || fields["routing"] == nil || strings.TrimSpace(string(fields["routing"])) == "null" || strings.TrimSpace(string(fields["expected_version"])) == "null" {
		common.ApiErrorStatus(c, http.StatusBadRequest, common.NewMessage("Only expected_version and routing are allowed"))
		return
	}
	var version int64
	if common.Unmarshal(fields["expected_version"], &version) != nil || version < 0 {
		common.ApiErrorStatus(c, http.StatusBadRequest, common.NewMessage("Routing version must be nonnegative"))
		return
	}
	policy, err := smartrouting.ParsePolicy(fields["routing"])
	if err != nil {
		tokenRoutingError(c, err)
		return
	}
	var row *model.TokenRouting
	err = model.DB.WithContext(c.Request.Context()).Transaction(func(tx *gorm.DB) error {
		// Same global order as funds and console changes: user, then token.
		var user model.User
		locked := tx
		if !common.UsingMainDatabase(common.DatabaseTypeSQLite) {
			locked = tx.Clauses(clause.Locking{Strength: "UPDATE"})
		}
		if err := locked.First(&user, "id = ?", c.GetInt("id")).Error; err != nil {
			return err
		}
		var token model.Token
		if err := locked.First(&token, "id = ? AND user_id = ?", c.GetInt("token_id"), user.Id).Error; err != nil {
			return err
		}
		previous, err := model.GetTokenRouting(tx, token.Id)
		if err != nil {
			return err
		}
		if previous == nil || !previous.AllowSelfWrite {
			return model.ErrRoutingSelfWriteDisabled
		}
		common.SetContextKey(c, constant.ContextKeyUserGroup, user.Group)
		if err := validateTokenRoutingPolicy(user.Group, tx, &token, &policy, previous, false); err != nil {
			return err
		}
		row, err = model.SaveTokenRoutingTx(tx, &token, &policy, version, nil, true, true, false)
		return err
	})
	if errors.Is(err, model.ErrRoutingSelfWriteDisabled) {
		common.ApiErrorStatus(c, http.StatusForbidden, common.NewMessage("routing_self_write_disabled"), gin.H{"code": "routing_self_write_disabled"})
		return
	}
	if err != nil {
		tokenRoutingError(c, err)
		return
	}
	params := tokenAuditParams(c)
	params["routing_version_from"], params["routing_version_to"] = version, row.Version
	common.SetContextKey(c, constant.ContextKeyTokenAuditSucceeded, true)
	common.ApiSuccess(c, gin.H{"routing": common.RawMessage(row.PolicyJSON), "routing_version": row.Version, "allow_routing_self_write": row.AllowSelfWrite})
}
