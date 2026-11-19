package smartrouting

import (
	"maps"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/QuantumNous/new-api/common"
)

const MaxPolicyBytes = 64 * 1024
const ContextKey = "mowan_token_routing"

type ModelPolicy struct {
	Strategy             string   `json:"strategy"`
	Groups               []string `json:"groups"`
	TrySoftOfflineGroups []string `json:"try_soft_offline_groups,omitempty"`
}

type Policy struct {
	SchemaVersion       int                    `json:"schema_version"`
	Mode                string                 `json:"mode"`
	Strategy            string                 `json:"strategy,omitempty"`
	FixedGroup          *string                `json:"fixed_group,omitempty"`
	Models              map[string]ModelPolicy `json:"models,omitempty"`
	ExcludedModels      []string               `json:"excluded_models,omitempty"`
	ExcludedGroups      []string               `json:"excluded_groups,omitempty"`
	ExcludedModelGroups map[string][]string    `json:"excluded_model_groups,omitempty"`
}

func DefaultPolicy() Policy { return Policy{SchemaVersion: 1, Mode: "smart", Strategy: "balanced"} }

// ParsePolicy rejects fields from inactive modes, including explicitly empty fields.
func ParsePolicy(data []byte) (Policy, error) {
	var p Policy
	if len(data) > MaxPolicyBytes {
		return p, common.NewMessage("Routing policy exceeds 64 KiB")
	}
	var fields map[string]common.RawMessage
	if err := common.Unmarshal(data, &fields); err != nil {
		return p, err
	}
	if fields == nil {
		return p, common.NewMessage("Routing policy must be an object")
	}
	if err := common.Unmarshal(data, &p); err != nil {
		return p, err
	}
	allowed := []string{"schema_version", "mode"}
	switch {
	case p.Mode == "fixed":
		allowed = append(allowed, "fixed_group")
	case p.Mode == "smart" && p.Strategy == "custom":
		allowed = append(allowed, "strategy", "models")
	case p.Mode == "smart":
		allowed = append(allowed, "strategy", "excluded_models", "excluded_groups", "excluded_model_groups")
	}
	for key := range fields {
		if !slices.Contains(allowed, key) {
			return p, common.NewMessage("Unknown or inactive routing field: {{field}}", map[string]any{"field": key})
		}
		if strings.TrimSpace(string(fields[key])) == "null" {
			return p, common.NewMessage("Routing fields cannot be null")
		}
	}
	if raw, ok := fields["models"]; ok {
		var models map[string]map[string]common.RawMessage
		if err := common.Unmarshal(raw, &models); err != nil {
			return p, err
		}
		for _, fields := range models {
			for key := range fields {
				if !slices.Contains([]string{"strategy", "groups", "try_soft_offline_groups"}, key) {
					return p, common.NewMessage("Unknown routing model field: {{field}}", map[string]any{"field": key})
				}
				if strings.TrimSpace(string(fields[key])) == "null" {
					return p, common.NewMessage("Routing fields cannot be null")
				}
			}
		}
	}
	return p, p.Validate()
}

func dynamicStrategy(s string) bool {
	return slices.Contains([]string{"balanced", "price", "speed", "success"}, s)
}

func validIdentifier(s string, limit int) bool {
	return s != "" && strings.TrimSpace(s) == s && utf8.ValidString(s) && utf8.RuneCountInString(s) <= limit && !strings.ContainsAny(s, "\x00\r\n")
}

func validateList(values []string, count, length int, groups bool) error {
	if len(values) > count {
		return common.NewMessage("Too many routing entries")
	}
	seen := make(map[string]bool, len(values))
	for _, value := range values {
		if !validIdentifier(value, length) || seen[value] || (groups && (value == "auto" || value == "all")) {
			return common.NewMessage("Invalid or duplicated routing identifier")
		}
		seen[value] = true
	}
	return nil
}

func (p Policy) Validate() error {
	if p.SchemaVersion != 1 {
		return common.NewMessage("Unsupported routing schema version")
	}
	if p.Mode == "fixed" {
		// Empty is a stored legacy-inherit value; the mutation boundary must
		// additionally require that this key actually has that legacy snapshot.
		if p.FixedGroup == nil || (*p.FixedGroup != "" && !validIdentifier(*p.FixedGroup, 64)) || *p.FixedGroup == "auto" || *p.FixedGroup == "all" || p.Strategy != "" || p.Models != nil || p.ExcludedModels != nil || p.ExcludedGroups != nil || p.ExcludedModelGroups != nil {
			return common.NewMessage("Fixed routing requires one explicit group")
		}
		return nil
	}
	if p.Mode != "smart" || p.FixedGroup != nil {
		return common.NewMessage("Invalid routing mode")
	}
	if p.Strategy == "custom" {
		if len(p.Models) == 0 || len(p.Models) > 200 || p.ExcludedModels != nil || p.ExcludedGroups != nil || p.ExcludedModelGroups != nil {
			return common.NewMessage("Custom routing requires configured models")
		}
		strategy := ""
		for name, policy := range p.Models {
			if !validIdentifier(name, 255) || (!dynamicStrategy(policy.Strategy) && policy.Strategy != "manual") || len(policy.Groups) == 0 {
				return common.NewMessage("Invalid custom routing model")
			}
			if err := validateList(policy.Groups, 100, 64, true); err != nil {
				return err
			}
			if strategy != "" && strategy != policy.Strategy {
				return common.NewMessage("Custom routing requires one scheduling strategy")
			}
			strategy = policy.Strategy
			if err := validateList(policy.TrySoftOfflineGroups, 100, 64, true); err != nil {
				return err
			}
			for _, group := range policy.TrySoftOfflineGroups {
				if !slices.Contains(policy.Groups, group) {
					return common.NewMessage("Offline preferences require selected groups")
				}
			}
		}
		return nil
	}
	if !dynamicStrategy(p.Strategy) || p.Models != nil || len(p.ExcludedModelGroups) > 200 {
		return common.NewMessage("Invalid routing strategy")
	}
	if err := validateList(p.ExcludedModels, 200, 255, false); err != nil {
		return err
	}
	if err := validateList(p.ExcludedGroups, 100, 64, true); err != nil {
		return err
	}
	for name, groups := range p.ExcludedModelGroups {
		if !validIdentifier(name, 255) {
			return common.NewMessage("Invalid routing model identifier")
		}
		if err := validateList(groups, 100, 64, true); err != nil {
			return err
		}
	}
	return nil
}

// CanonicalJSON preserves manual order; unordered sets are serialized stably.
func (p Policy) CanonicalJSON() (string, error) {
	if err := p.Validate(); err != nil {
		return "", err
	}
	p.Models = maps.Clone(p.Models)
	p.ExcludedModelGroups = maps.Clone(p.ExcludedModelGroups)
	for name, policy := range p.Models {
		policy.TrySoftOfflineGroups = slices.Sorted(slices.Values(policy.TrySoftOfflineGroups))
		if policy.Strategy != "manual" {
			policy.Groups = slices.Sorted(slices.Values(policy.Groups))
		}
		p.Models[name] = policy
	}
	p.ExcludedModels = slices.Sorted(slices.Values(p.ExcludedModels))
	p.ExcludedGroups = slices.Sorted(slices.Values(p.ExcludedGroups))
	for name, groups := range p.ExcludedModelGroups {
		p.ExcludedModelGroups[name] = slices.Sorted(slices.Values(groups))
	}
	data, err := common.Marshal(p)
	if len(data) > MaxPolicyBytes {
		return "", common.NewMessage("Routing policy exceeds {{limit}} bytes", map[string]any{"limit": MaxPolicyBytes})
	}
	return string(data), err
}
