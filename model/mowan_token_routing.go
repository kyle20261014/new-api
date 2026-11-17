package model

import (
	"errors"
	"math"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/internal/mowan/smartrouting"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var (
	ErrRoutingVersionConflict   = errors.New("routing_version_conflict")
	ErrRoutingSelfWriteDisabled = errors.New("routing_self_write_disabled")
)

type TokenRouting struct {
	TokenID        int    `gorm:"primaryKey;autoIncrement:false" json:"-"`
	SchemaVersion  int    `json:"-"`
	Mode           string `gorm:"type:varchar(16)" json:"-"`
	PolicyJSON     string `gorm:"type:text" json:"-"`
	LegacySnapshot string `gorm:"type:text" json:"-"`
	AllowSelfWrite bool   `gorm:"default:false" json:"-"`
	Version        int64  `json:"-"`
	CreatedAt      int64  `gorm:"autoCreateTime" json:"-"`
	UpdatedAt      int64  `gorm:"autoUpdateTime" json:"-"`
}

func (TokenRouting) TableName() string { return "mowan_token_routing" }

func MigrateTokenRouting(db *gorm.DB) error { return db.AutoMigrate(&TokenRouting{}) }

type LegacyTokenRouting struct {
	Group           string `json:"group"`
	AutoGroups      string `json:"auto_groups_raw"`
	CrossGroupRetry bool   `json:"cross_group_retry"`
}

func GetTokenRouting(db *gorm.DB, tokenID int) (*TokenRouting, error) {
	var row TokenRouting
	err := db.First(&row, "token_id = ?", tokenID).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if row.SchemaVersion != 1 || row.Version < 1 || (row.Mode != "legacy" && row.Mode != "fixed" && row.Mode != "smart") {
		return nil, errors.New("invalid stored routing configuration")
	}
	if row.Mode != "legacy" {
		p, err := smartrouting.ParsePolicy([]byte(row.PolicyJSON))
		if err != nil || p.Mode != row.Mode {
			return nil, errors.New("invalid stored routing policy")
		}
		if p.Mode == "fixed" && p.FixedGroup != nil && *p.FixedGroup == "" {
			original, err := parseLegacyRouting(row.LegacySnapshot)
			if err != nil || original.Group != "" {
				return nil, errors.New("invalid stored inherited routing policy")
			}
		}
	} else if row.PolicyJSON != "" {
		return nil, errors.New("invalid stored legacy routing policy")
	}
	if row.LegacySnapshot != "" {
		if _, err := parseLegacyRouting(row.LegacySnapshot); err != nil {
			return nil, err
		}
	}
	return &row, nil
}

func parseLegacyRouting(data string) (LegacyTokenRouting, error) {
	var snapshot LegacyTokenRouting
	var fields map[string]common.RawMessage
	if len(data) > smartrouting.MaxPolicyBytes || common.UnmarshalJsonStr(data, &fields) != nil || len(fields) != 3 || fields["group"] == nil || fields["auto_groups_raw"] == nil || fields["cross_group_retry"] == nil {
		return snapshot, errors.New("invalid stored legacy routing snapshot")
	}
	for _, value := range fields {
		if strings.TrimSpace(string(value)) == "null" {
			return snapshot, errors.New("invalid stored legacy routing snapshot")
		}
	}
	if common.UnmarshalJsonStr(data, &snapshot) != nil {
		return snapshot, errors.New("invalid stored legacy routing snapshot")
	}
	return snapshot, nil
}

// SaveTokenRoutingTx shares the token row lock with console writes/deletes.
// A nil policy explicitly restores the original legacy snapshot, never deletes a version.
func SaveTokenRoutingTx(tx *gorm.DB, token *Token, policy *smartrouting.Policy, expected int64, allowSelf *bool, self bool, captureLegacy bool, restoreLegacy bool) (*TokenRouting, error) {
	var user User
	if err := lockForUpdate(tx).First(&user, "id = ?", token.UserId).Error; err != nil {
		return nil, err
	}
	var authoritative Token
	if err := lockForUpdate(tx).First(&authoritative, "id = ? AND user_id = ?", token.Id, token.UserId).Error; err != nil {
		return nil, err
	}
	row, err := GetTokenRouting(tx, token.Id)
	if err != nil {
		return nil, err
	}
	current := int64(0)
	if row != nil {
		current = row.Version
	}
	if current != expected || current == math.MaxInt64 {
		return nil, ErrRoutingVersionConflict
	}
	if self {
		if row == nil || !row.AllowSelfWrite || allowSelf != nil || policy == nil || restoreLegacy {
			return nil, ErrRoutingSelfWriteDisabled
		}
		if authoritative.Status != common.TokenStatusEnabled || (authoritative.ExpiredTime != -1 && authoritative.ExpiredTime < common.GetTimestamp()) || (!authoritative.UnlimitedQuota && authoritative.RemainQuota <= 0) || user.Status != common.UserStatusEnabled {
			return nil, errors.New("routing_identity_unavailable")
		}
	}
	if row == nil {
		row = &TokenRouting{TokenID: token.Id, SchemaVersion: 1, Mode: "legacy"}
	}
	if captureLegacy && row.Mode == "legacy" && row.LegacySnapshot == "" && !restoreLegacy {
		data, err := common.Marshal(LegacyTokenRouting{authoritative.Group, authoritative.AutoGroups, authoritative.CrossGroupRetry})
		if err != nil {
			return nil, err
		}
		row.LegacySnapshot = string(data)
	}
	if policy != nil {
		if policy.Mode == "fixed" && policy.FixedGroup != nil && *policy.FixedGroup == "" {
			var original LegacyTokenRouting
			if row.LegacySnapshot == "" || common.UnmarshalJsonStr(row.LegacySnapshot, &original) != nil || original.Group != "" {
				return nil, common.NewMessage("Fixed routing requires one explicit group")
			}
		}
		row.PolicyJSON, err = policy.CanonicalJSON()
		if err != nil {
			return nil, err
		}
		row.Mode = policy.Mode
	} else if restoreLegacy {
		if row.LegacySnapshot == "" {
			return nil, errors.New("routing_legacy_snapshot_unavailable")
		}
		var snapshot LegacyTokenRouting
		if err := common.UnmarshalJsonStr(row.LegacySnapshot, &snapshot); err != nil {
			return nil, err
		}
		token.Group, token.AutoGroups, token.CrossGroupRetry = snapshot.Group, snapshot.AutoGroups, snapshot.CrossGroupRetry
		if err := tx.Model(&Token{}).Where("id = ? AND user_id = ?", token.Id, token.UserId).
			Select("group", "auto_groups", "cross_group_retry").Updates(token).Error; err != nil {
			return nil, err
		}
		row.Mode, row.PolicyJSON = "legacy", ""
	}
	if allowSelf != nil {
		row.AllowSelfWrite = *allowSelf
	}
	row.Version = current + 1
	if current == 0 {
		result := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(row)
		if result.Error != nil {
			return nil, result.Error
		}
		if result.RowsAffected != 1 {
			return nil, ErrRoutingVersionConflict
		}
	} else {
		result := tx.Model(&TokenRouting{}).Where("token_id = ? AND version = ?", row.TokenID, current).
			Select("mode", "policy_json", "legacy_snapshot", "allow_self_write", "version", "updated_at").Updates(row)
		if result.Error != nil {
			return nil, result.Error
		}
		if result.RowsAffected != 1 {
			return nil, ErrRoutingVersionConflict
		}
	}
	return row, nil
}
